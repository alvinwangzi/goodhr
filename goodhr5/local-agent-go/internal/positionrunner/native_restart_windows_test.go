// 本文件用两个实际 Windows 本地程序进程和同一 SQLite 验证已确认收据在进程退出、重新登录后恢复，不打开招聘页面。
package positionrunner

import (
	"context"
	"encoding/json"
	"fmt"
	"goodhr5/local-agent-go/internal/cloudapi"
	"goodhr5/local-agent-go/internal/config"
	"goodhr5/local-agent-go/internal/localdb"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

// TestNativeAgentReceiptProcessRestart 验证退出前仍待上传，重启后设备绑定恢复原事实且不创建新发送。
func TestNativeAgentReceiptProcessRestart(t *testing.T) {
	if os.Getenv("HRPLUS_M1_NATIVE_WORKER_TEST") != "1" {
		t.Skip("需要显式启用 Windows 进程重启验收")
	}
	directory := t.TempDir()
	cfg := &config.Config{DataDir: filepath.Join(directory, "data")}
	var calls atomic.Int32
	var accept atomic.Bool
	var mu sync.Mutex
	var receipts []cloudapi.ReGreetReceiptRequest
	cloud := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/auth/me":
			_ = json.NewEncoder(w).Encode(map[string]any{"user": map[string]any{"email": "restart@example.com"}})
		case "/api/agents/bind":
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
		case "/api/positions/position/re-greet-report":
			var receipt cloudapi.ReGreetReceiptRequest
			_ = json.NewDecoder(r.Body).Decode(&receipt)
			mu.Lock()
			receipts = append(receipts, receipt)
			mu.Unlock()
			calls.Add(1)
			if !accept.Load() {
				w.WriteHeader(503)
				_ = json.NewEncoder(w).Encode(map[string]any{"error": "受控网络失败"})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"receipt": cloudapi.ReGreetReceiptResponse{OperationID: receipt.OperationID, ResultCount: receipt.BaseCount + 1, SentAt: receipt.SentAt, ReceivedAt: time.Now()}})
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer cloud.Close()
	db, err := localdb.Open(cfg)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.SaveSettings(map[string]any{"machine_id": "fixture-machine"})
	if err != nil {
		t.Fatal(err)
	}
	cp, err := db.CreateActionRun(t.Context(), localdb.ActionCheckpoint{PositionID: "position", Platform: "boss", ProfileScope: "legacy"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.BindActionRunAccount(t.Context(), cp.RunID, "restart-scope", cloudapi.SessionOwnerScope(cloud.URL, "restart@example.com"))
	if err != nil {
		t.Fatal(err)
	}
	record, err := db.PrepareAutoReply(t.Context(), localdb.AutoReplyRecord{ProfileScope: "restart-scope", Platform: "boss", ConversationID: "123-0", InboundFingerprint: "basis", PositionID: "position", RunID: "original-task", ContextFingerprint: "context", ReplyFingerprint: "message"})
	if err != nil {
		t.Fatal(err)
	}
	if err = db.TransitionAutoReply(t.Context(), record.ID, "prepared", "sending", ""); err != nil {
		t.Fatal(err)
	}
	original := cloudapi.ReGreetReceiptRequest{MachineID: "fixture-machine", OperationID: record.ID, Platform: "boss", PlatformCandidateID: "opaque", Success: true, RunID: "original-task", BaseContactAt: time.Now().Add(-time.Hour), SentAt: time.Now().UTC().Truncate(time.Microsecond), MessageText: "仅虚构原发送事实"}
	payload, _ := json.Marshal(struct {
		PositionID string `json:"position_id"`
		cloudapi.ReGreetReceiptRequest
	}{"position", original})
	if err = db.ConfirmReGreetAndQueue(t.Context(), localdb.ReGreetOutbox{OperationID: record.ID, ProfileScope: record.ProfileScope, Platform: "boss", CandidateID: "opaque", APIBase: cloud.URL, Payload: payload}); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	executable := filepath.Join(directory, "hrplus-m1-restart.exe")
	build := exec.Command("go", "build", "-o", executable, "./cmd/goodhr-local-agent")
	build.Dir = root
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build %v %s", err, output)
	}
	// 两个相邻空端口同时检查，避免程序启动时清理其他 Worker。
	var port int
	for attempts := 0; attempts < 20; attempts++ {
		first, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		port = first.Addr().(*net.TCPAddr).Port
		second, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port+1))
		_ = first.Close()
		if err == nil {
			_ = second.Close()
			break
		}
		port = 0
	}
	if port == 0 {
		t.Fatal("没有独立验收端口")
	}
	worker := nativeHTTPWorker{base: fmt.Sprintf("http://127.0.0.1:%d", port)}
	start := func() *exec.Cmd {
		command := exec.Command(executable, "--host", "127.0.0.1", "--port", fmt.Sprint(port), "--data-dir", cfg.DataDir, "--open-console=false")
		command.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
		command.Env = append(os.Environ(), "GOODHR_APP_ENV=dev", "GOODHR_CLOUD_API_BASE="+cloud.URL, "GOODHR_CONSOLE_URL=http://127.0.0.1:1", "GOODHR_CONSOLE_MANIFEST_URL=")
		if err := command.Start(); err != nil {
			t.Fatal(err)
		}
		deadline := time.Now().Add(10 * time.Second)
		for {
			if _, err := worker.Start(t.Context()); err == nil {
				break
			}
			if time.Now().After(deadline) {
				_ = command.Process.Kill()
				_ = command.Wait()
				t.Fatal("实际本地程序未启动")
			}
			time.Sleep(100 * time.Millisecond)
		}
		if _, err := worker.CallOnce(t.Context(), "/api/v1/session/bind", map[string]any{"token": "fixture-token"}); err != nil {
			_ = command.Process.Kill()
			_ = command.Wait()
			t.Fatal(err)
		}
		return command
	}
	first := start()
	deadline := time.Now().Add(10 * time.Second)
	for calls.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(100 * time.Millisecond)
	}
	_ = first.Process.Kill()
	_ = first.Wait()
	if calls.Load() == 0 {
		t.Fatal("退出前没有实际补传尝试")
	}
	accept.Store(true)
	second := start()
	defer func() { _ = second.Process.Kill(); _ = second.Wait() }()
	deadline = time.Now().Add(12 * time.Second)
	for calls.Load() < 2 && time.Now().Before(deadline) {
		time.Sleep(100 * time.Millisecond)
	}
	if calls.Load() < 2 {
		t.Fatal("进程重启未恢复补传")
	}
	mu.Lock()
	for _, receipt := range receipts {
		wanted, _ := json.Marshal(original)
		actual, _ := json.Marshal(receipt)
		if string(wanted) != string(actual) {
			t.Error("重启改变了原编号或发送事实")
		}
	}
	mu.Unlock()
	db, err = localdb.Open(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	deadline = time.Now().Add(3 * time.Second)
	var pending bool
	for {
		pending, err = db.PendingReGreetForCandidate(context.Background(), record.ProfileScope, "boss", "opaque")
		if err != nil || !pending || time.Now().After(deadline) {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if err != nil || pending {
		t.Fatalf("重启回执未持久确认 pending=%t err=%v", pending, err)
	}
}
