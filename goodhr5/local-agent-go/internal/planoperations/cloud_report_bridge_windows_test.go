// 本文件显式启动 HRPlus 实际云端测试路由和专用 PostgreSQL 数据库，验证本地 SQLite 与真实 SDK 原报告补传。
package planoperations

import (
	"bytes"
	"encoding/json"
	"goodhr5/local-agent-go/internal/cloudapi"
	"goodhr5/local-agent-go/internal/config"
	"goodhr5/local-agent-go/internal/localdb"
	"goodhr5/local-agent-go/internal/planmodel"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// TestCloudReportSDKBridge 验证原电脑跨配置/软删除补传以及真实摘要 hash/时间回执，不操作招聘页面或真实邮件。
func TestCloudReportSDKBridge(t *testing.T) {
	if os.Getenv("HRPLUS_M2_CLOUD_REPORT_BRIDGE_TEST") != "1" {
		t.Skip("需要显式启用实际云端/本地 SDK 联调")
	}
	dsn, err := url.Parse(os.Getenv("GOODHR_EXECUTION_PLAN_TEST_PG_DSN"))
	if err != nil || dsn.Host != "127.0.0.1:25499" || dsn.Path != "/hrplus_m2_test" {
		t.Fatal("联调只能使用明确的回环 M2 隔离数据库")
	}
	directory := t.TempDir()
	backend, err := filepath.Abs(filepath.Join("..", "..", "..", "cloud", "backend"))
	if err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(directory, "cloud-report-bridge.test.exe")
	build := exec.Command("go", "test", "-c", "-o", binary, "./internal/httpapi")
	build.Dir = backend
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("编译实际云端联调入口失败：%v\n%s", err, output)
	}
	fixturePath := filepath.Join(directory, "bridge.json")
	cmd := exec.Command(binary, "-test.run=^TestExecutionPlanSDKBridgeHelper$", "-test.timeout=90s")
	cmd.Dir = filepath.Join(backend, "internal", "httpapi")
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	cmd.Env = append(os.Environ(), "HRPLUS_M2_SDK_BRIDGE_HELPER=1", "HRPLUS_M2_SDK_BRIDGE_FILE="+fixturePath)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	var exitError error
	go func() { exitError = cmd.Wait(); close(done) }()
	t.Cleanup(func() {
		_, _ = stdin.Write([]byte("done\n"))
		_ = stdin.Close()
		select {
		case <-done:
			if exitError != nil {
				t.Errorf("云端夹具退出失败：%v\n%s", exitError, output.String())
			}
		case <-time.After(10 * time.Second):
			_ = cmd.Process.Kill()
			<-done
			t.Error("夹具未完成自身清理")
		}
	})
	var fixture struct {
		BaseURL  string           `json:"base_url"`
		Token    string           `json:"token"`
		Email    string           `json:"email"`
		MachineA string           `json:"machine_a"`
		MachineB string           `json:"machine_b"`
		Summary  planmodel.Report `json:"summary"`
	}
	deadline := time.NewTimer(15 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		raw, err := os.ReadFile(fixturePath)
		if err == nil && json.Unmarshal(raw, &fixture) == nil {
			break
		}
		select {
		case <-ticker.C:
		case <-done:
			t.Fatalf("实际云端夹具提前退出：%v\n%s", exitError, output.String())
		case <-deadline.C:
			t.Fatal("实际云端夹具初始化超时，退出前检查原进程和清理")
		}
	}
	if err := fixture.Summary.Validate(); err != nil {
		t.Fatal("跨模块原摘要不完整", err)
	}
	client := cloudapi.New(fixture.BaseURL)
	localConfig := &config.Config{DataDir: filepath.Join(directory, "local")}
	db, err := localdb.Open(localConfig)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	scope := cloudapi.SessionOwnerScope(fixture.BaseURL, fixture.Email)
	if _, err := db.SavePlanReportSnapshot(t.Context(), scope, fixture.Summary); err != nil {
		t.Fatal(err)
	}
	store := New(db)
	authority := Authority{Token: fixture.Token, OwnerScope: scope, StillCurrent: func() bool { return true }}
	// 已绑定 B 的失败不能确认原 A 报告或删除本地原 outbox。
	if sent, err := store.UploadNextReport(t.Context(), client, fixture.MachineB, authority); err == nil || sent {
		t.Fatal("B 电脑确认了 A 的原报告", err)
	}
	if _, err := db.NextPlanReportUpload(t.Context(), scope); err != nil {
		t.Fatal("拒绝上传后原报告丢失", err)
	}
	hook := func(path string) {
		req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, fixture.BaseURL+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+fixture.Token)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		if res.StatusCode != 200 {
			t.Fatal("夹具变更失败", path, res.StatusCode)
		}
	}
	hook("/__fixture/switch-device")
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = localdb.Open(localConfig)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store = New(db)
	if sent, err := store.UploadNextReport(t.Context(), client, fixture.MachineA, authority); err != nil || !sent {
		t.Fatal("改到 B 后真实 SDK 无法确认 A 的原报告", err)
	}
	if sent, err := store.UploadNextReport(t.Context(), client, fixture.MachineA, authority); err != nil || sent {
		t.Fatal("已确认原报告重复补传", err)
	}
	hook("/__fixture/delete")
	receipt, err := client.UploadExecutionPlanReport(t.Context(), fixture.Token, fixture.MachineA, fixture.Summary, "confirmed")
	if err != nil || receipt.Summary.GeneratedAt != fixture.Summary.GeneratedAt || receipt.NotificationState != "not_configured" {
		t.Fatal("软删除后实际 SDK 原内容确认失败", err, receipt.NotificationState)
	}
}
