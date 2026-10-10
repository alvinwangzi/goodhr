// 本文件用独立 Windows 测试进程被终止后的 DPAPI 和 SQLite 原请求恢复验证 HRPlus 补传，不操作业务程序。
package planoperations

import (
	"bufio"
	"encoding/json"
	"goodhr5/local-agent-go/internal/cloudapi"
	"goodhr5/local-agent-go/internal/config"
	"goodhr5/local-agent-go/internal/localdb"
	"goodhr5/local-agent-go/internal/planmodel"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// processDispatchInput 为原测试运行构造完整状态事实，不使用业务凭证或候选人信息。
func processDispatchInput(run planmodel.Run) cloudapi.PlanRunUpdateRequest {
	input := cloudapi.PlanRunUpdateRequest{RunID: run.ID, Action: "status", RequestID: "60000000-0000-0000-0000-000000000010", OwnerID: run.OwnerID, MachineID: "machine-A", Credential: strings.Repeat("fixture-secret-", 4), Sequence: run.Sequence, State: run.State, CurrentItem: run.CurrentItem, EndReason: run.EndReason}
	for _, item := range run.Items {
		input.Items = append(input.Items, planmodel.ItemUpdate{ID: item.ID, ItemID: item.ItemID, State: item.State, Actions: item.Actions})
	}
	return input
}

// TestMessageDispatchProcessHelper 仅在父测试明确传入隔离数据目录时保存原请求并等待被终止。
func TestMessageDispatchProcessHelper(t *testing.T) {
	if os.Getenv("HRPLUS_DISPATCH_PROCESS_HELPER") != "1" {
		return
	}
	cfg := &config.Config{DataDir: os.Getenv("HRPLUS_DISPATCH_PROCESS_DIR")}
	if cfg.DataDir == "" {
		t.Fatal("缺少隔离目录")
	}
	db, err := localdb.Open(cfg)
	if err != nil {
		t.Fatal(err)
	}
	var permit planmodel.Permit
	if json.Unmarshal([]byte(os.Getenv("HRPLUS_DISPATCH_PROCESS_PERMIT")), &permit) != nil {
		t.Fatal("缺少测试运行")
	}
	scope := os.Getenv("HRPLUS_DISPATCH_PROCESS_SCOPE")
	if err := db.SavePlanRunSnapshot(t.Context(), scope, permit.Run); err != nil {
		t.Fatal(err)
	}
	next := permit.Run
	next.Sequence++
	next.State = "running"
	raw, _ := json.Marshal(map[string]any{"Schema": 1, "OwnerScope": scope, "Run": next, "PagesSinceScan": 2})
	if _, err := New(db).StageUpdate(t.Context(), scope, next.PlanID, processDispatchInput(next), raw); err != nil {
		t.Fatal(err)
	}
	// 不关闭数据库，让父测试实际终止此进程并验证已提交 WAL 可恢复。
	if _, err := os.Stdout.WriteString("dispatch-staged\n"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(30 * time.Second)
	t.Fatal("父测试未在期限内终止辅助进程")
}

// TestMessageDispatchKilledProcess 验证强制终止拥有原请求的测试进程后，新进程仍按原编号共同补传。
func TestMessageDispatchKilledProcess(t *testing.T) {
	_, _, permit, _ := operationFixture(t)
	next := permit.Run
	next.Sequence++
	next.State = "running"
	var posts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/auth/me" {
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "user": map[string]any{"email": "fixture@example.com"}})
			return
		}
		if r.URL.Path != "/api/execution-plan-runs/"+next.ID+"/status" {
			t.Error("补传目标不匹配", r.URL.Path)
		}
		var input cloudapi.PlanRunUpdateRequest
		if json.NewDecoder(r.Body).Decode(&input) != nil || input.RequestID != processDispatchInput(next).RequestID || input.Sequence != next.Sequence {
			t.Error("中断后原请求被替换")
		}
		posts.Add(1)
		result := permit
		result.Run, result.Owner.State = next, "running"
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "permit": result})
	}))
	defer server.Close()
	scope := cloudapi.SessionOwnerScope(server.URL, "fixture@example.com")
	cfg := &config.Config{DataDir: t.TempDir()}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	rawPermit, _ := json.Marshal(permit)
	cmd := exec.Command(executable, "-test.run=^TestMessageDispatchProcessHelper$", "-test.timeout=40s")
	cmd.Env = append(os.Environ(), "HRPLUS_DISPATCH_PROCESS_HELPER=1", "HRPLUS_DISPATCH_PROCESS_DIR="+cfg.DataDir, "HRPLUS_DISPATCH_PROCESS_SCOPE="+scope, "HRPLUS_DISPATCH_PROCESS_PERMIT="+string(rawPermit))
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if cmd.ProcessState == nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	})
	ready := make(chan bool, 1)
	go func() {
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			if scanner.Text() == "dispatch-staged" {
				ready <- true
				return
			}
		}
		ready <- false
	}()
	select {
	case ok := <-ready:
		if !ok {
			t.Fatal("辅助进程未完成原请求提交")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("辅助进程提交超时")
	}
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Wait(); err == nil {
		t.Fatal("测试进程并未被强制终止")
	}
	if posts.Load() != 0 {
		t.Fatal("中断前不应发送状态")
	}
	reopened, err := localdb.Open(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	store := New(reopened)
	input, err := store.OriginalUpdate(t.Context(), scope, processDispatchInput(next).RequestID)
	if err != nil || input.Sequence != next.Sequence {
		t.Fatal("进程终止后原请求不能解密", err)
	}
	if sent, err := store.UploadNext(t.Context(), cloudapi.New(server.URL), Authority{Token: "fixture-token", OwnerScope: scope, StillCurrent: func() bool { return true }}); err != nil || !sent {
		t.Fatal("新进程补传失败", err)
	}
	dispatch, err := reopened.PlanMessageDispatch(t.Context(), scope, next.ID)
	var saved struct {
		PagesSinceScan int
		Run            planmodel.Run
	}
	if err != nil || json.Unmarshal(dispatch, &saved) != nil || saved.PagesSinceScan != 2 || saved.Run.Sequence != next.Sequence {
		t.Fatal("进程终止丢失原轮换", err)
	}
	op, err := reopened.PlanOperation(t.Context(), scope, input.RequestID)
	if err != nil || op.State != "confirmed" || posts.Load() != 1 {
		t.Fatal("新进程未共同确认原回执", err)
	}
}
