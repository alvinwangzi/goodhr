// 本文件使用实际 Windows 本地程序进程和同一 SQLite 验证登录、收据及定时安排恢复；招聘执行只使用隔离页面。
package positionrunner

import (
	"context"
	"encoding/json"
	"fmt"
	"goodhr5/local-agent-go/internal/cloudapi"
	"goodhr5/local-agent-go/internal/config"
	"goodhr5/local-agent-go/internal/localdb"
	"goodhr5/local-agent-go/internal/planmodel"
	"goodhr5/local-agent-go/internal/planoperations"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

// TestNativeAgentReceiptProcessRestart 验证退出前仍待上传，重启后设备绑定恢复原事实且不创建新发送。
func TestNativeAgentReceiptProcessRestart(t *testing.T) {
	runNativeReceiptRestart(t, false)
}

// TestNativeAgentProtectedLoginRestart 验证第二个真实进程不经过网页绑定也能复验并恢复受保护登录。
func TestNativeAgentProtectedLoginRestart(t *testing.T) {
	runNativeReceiptRestart(t, true)
}

// TestNativeAgentScheduledQueueRestart 验证网页不参与的实际程序重启继续定时领取，账号忙队列再次重启仍保留原请求。
func TestNativeAgentScheduledQueueRestart(t *testing.T) { runNativeReceiptRestart(t, true, true) }

// runNativeReceiptRestart 保留原补传验收，并可选择重启后完全依赖本地受保护会话。
func runNativeReceiptRestart(t *testing.T, protectedRestore bool, scheduled ...bool) {
	if os.Getenv("HRPLUS_M1_NATIVE_WORKER_TEST") != "1" && os.Getenv("HRPLUS_M2_NATIVE_AGENT_TEST") != "1" {
		t.Skip("需要显式启用 Windows 进程重启验收")
	}
	directory := t.TempDir()
	cfg := &config.Config{DataDir: filepath.Join(directory, "data")}
	var calls atomic.Int32
	var planCalls atomic.Int32
	var accept atomic.Bool
	var mu sync.Mutex
	var receipts []cloudapi.ReGreetReceiptRequest
	var planBodies []cloudapi.PlanRunUpdateRequest
	var planPermit planmodel.Permit
	scheduledRestore := len(scheduled) > 0 && scheduled[0]
	executeScheduled := len(scheduled) > 1 && scheduled[1]
	var executed planmodel.Permit
	executionReplies := map[string][]byte{}
	reportGate := nativeRestartReportGate{}
	var scheduledPlan planmodel.Plan
	var scheduledClaims []cloudapi.PlanClaimRequest
	var scheduledCalls atomic.Int32
	if protectedRestore {
		raw, e := os.ReadFile(filepath.Join("..", "planmodel", "testdata", "permit.json"))
		if e != nil {
			t.Fatal(e)
		}
		if e = json.Unmarshal(raw, &planPermit); e != nil {
			t.Fatal(e)
		}
		if scheduledRestore {
			rawConfig, _ := json.Marshal(planPermit.Run.Snapshot)
			var config planmodel.Config
			if e = json.Unmarshal(rawConfig, &config); e != nil {
				t.Fatal(e)
			}
			config.Schedule.Windows = config.Schedule.Windows[:1]
			config.Schedule.Windows[0].StartMinute, config.Schedule.Windows[0].EndMinute = 0, 1440
			if executeScheduled {
				config.Items = config.Items[:1]
				config.Items[0].PositionID = "native-java"
				config.Items[0].Actions = []string{"greeting"}
				config.Items[0].PrioritizeReply = false
			}
			scheduledPlan = planmodel.Plan{ID: planPermit.Run.PlanID, UserEmail: "restart@example.com", MachineID: "fixture-machine", Version: 1, StateSequence: 1, ActivationID: "30000000-0000-0000-0000-000000000002", State: "enabled", Config: config, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
			if e = scheduledPlan.Validate(); e != nil {
				t.Fatal(e)
			}
		}
	}
	cloud := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if executeScheduled {
			mu.Lock()
			handled := nativeRestartExecutionCloud(t, w, r, &executed, executionReplies, &reportGate)
			mu.Unlock()
			if handled {
				return
			}
		}
		if scheduledRestore && r.URL.Path == "/api/execution-plans/"+scheduledPlan.ID+"/wait" {
			var fact cloudapi.PlanWaitFact
			if e := json.NewDecoder(r.Body).Decode(&fact); e != nil {
				t.Error(e)
			}
			if r.Header.Get("Authorization") != "Bearer fixture-token" || fact.ActivationID != scheduledPlan.ActivationID || fact.MachineID != scheduledPlan.MachineID {
				t.Error("定时排队没有原登录或指定电脑")
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "wait": fact})
			return
		}
		switch r.URL.Path {
		case "/api/execution-plan-runs/claim":
			if !scheduledRestore {
				t.Error("补传测试不应新领取计划")
			}
			var claim cloudapi.PlanClaimRequest
			if e := json.NewDecoder(r.Body).Decode(&claim); e != nil {
				t.Error(e)
			}
			if r.Header.Get("Authorization") != "Bearer fixture-token" || claim.PlanID != scheduledPlan.ID || claim.ActivationID != scheduledPlan.ActivationID || !claim.LocalReserved {
				t.Error("后台定时领取来源不正确")
			}
			mu.Lock()
			scheduledClaims = append(scheduledClaims, claim)
			mu.Unlock()
			scheduledCalls.Add(1)
			if executeScheduled {
				mu.Lock()
				executed = planmodel.Permit{Run: planmodel.Run{ID: claim.RunID, PlanID: claim.PlanID, ActivationID: claim.ActivationID, ExecutionDate: claim.ExecutionDate, ConfigVersion: claim.ExpectedVersion, Sequence: 1, State: "starting", Snapshot: scheduledPlan.Config, OwnerID: claim.OwnerID, Items: []planmodel.ItemRun{{ID: "50000000-0000-0000-0000-000000000099", ItemID: scheduledPlan.Config.Items[0].ID, Order: 0, Snapshot: scheduledPlan.Config.Items[0], State: "pending", Actions: map[string]planmodel.ActionProgress{"greeting": {State: "pending"}}}}}, Owner: planmodel.Owner{OwnerID: claim.OwnerID, OwnerType: "plan", MachineID: claim.MachineID, State: "starting"}}
				_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "permit": executed})
				mu.Unlock()
				return
			}
			w.WriteHeader(http.StatusConflict)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": "受控账号忙", "error_code": "EXECUTION_BUSY"})
		case "/api/execution-plan-runs/" + planPermit.Run.ID + "/release":
			if !protectedRestore {
				t.Error("M1 原生验收不应发送计划请求")
			}
			if r.Header.Get("Authorization") != "Bearer fixture-token" {
				t.Error("计划补传缺少原登录证明")
			}
			var input cloudapi.PlanRunUpdateRequest
			if e := json.NewDecoder(r.Body).Decode(&input); e != nil {
				t.Error(e)
			}
			mu.Lock()
			planBodies = append(planBodies, input)
			mu.Unlock()
			planCalls.Add(1)
			if !accept.Load() {
				w.WriteHeader(503)
				_ = json.NewEncoder(w).Encode(map[string]any{"error": "受控计划网络失败"})
				return
			}
			result := planPermit
			result.Run.Sequence = input.Sequence
			result.Run.State = input.State
			result.Owner.MachineID = input.MachineID
			result.Owner.State = "released"
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "permit": result})
		case "/api/auth/me":
			_ = json.NewEncoder(w).Encode(map[string]any{"user": map[string]any{"email": "restart@example.com"}})
		case "/api/execution-plans":
			if r.Method != http.MethodGet || r.Header.Get("Authorization") != "Bearer fixture-token" {
				t.Error("计划同步未使用只读请求和原登录证明")
			}
			plans := []any{}
			if scheduledRestore && accept.Load() {
				plans = append(plans, scheduledPlan)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "plans": plans})
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
	planScope := cloudapi.SessionOwnerScope(cloud.URL, "restart@example.com")
	planUpdate := cloudapi.PlanRunUpdateRequest{RunID: planPermit.Run.ID, Action: "release", RequestID: "60000000-0000-0000-0000-000000000007", OwnerID: planPermit.Owner.OwnerID, MachineID: "fixture-machine", Credential: "fixture-native-plan-secret-0123456789", Sequence: 2, State: "waiting_window", CleanupConfirmed: true}
	if protectedRestore {
		if _, err = planoperations.New(db).StageUpdate(t.Context(), planScope, planPermit.Run.PlanID, planUpdate); err != nil {
			t.Fatal(err)
		}
	}
	_ = db.Close()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	executable := filepath.Join(directory, "hrplus-m1-restart.exe")
	executionLedger := filepath.Join(directory, "execution-ledger.json")
	var cachedBrowser string
	if executeScheduled {
		cachedBrowser = filepath.Join(os.Getenv("APPDATA"), "HRPlus", "runtime", "cloakbrowser", "chrome.exe")
		binary, e := os.ReadFile(cachedBrowser)
		if e != nil {
			t.Fatal("缺少已缓存浏览器", e)
		}
		destination := filepath.Join(cfg.DataDir, "runtime", "cloakbrowser")
		if e = os.MkdirAll(destination, 0755); e != nil {
			t.Fatal(e)
		}
		if e = os.WriteFile(filepath.Join(destination, "chrome.exe"), binary, 0755); e != nil {
			t.Fatal(e)
		}
	}
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
	start := func(bind bool) *exec.Cmd {
		command := exec.Command(executable, "--host", "127.0.0.1", "--port", fmt.Sprint(port), "--data-dir", cfg.DataDir, "--open-console=false")
		command.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
		command.Env = append(os.Environ(), "GOODHR_APP_ENV=dev", "GOODHR_CLOUD_API_BASE="+cloud.URL, "GOODHR_CONSOLE_URL=http://127.0.0.1:1", "GOODHR_CONSOLE_MANIFEST_URL=")
		if executeScheduled {
			command.Dir = root
			preload := filepath.ToSlash(filepath.Join(root, "worker-node", "test", "fixtures", "worker-native-preload.mjs"))
			command.Env = append(command.Env, "NODE_OPTIONS=--import=file:///"+preload, "HRPLUS_M2_NATIVE_BROWSER_SOURCE="+cachedBrowser, "HRPLUS_M1_FIXTURE_MODE=m2-plans", "HRPLUS_M1_FIXTURE_LEDGER="+executionLedger, "GOODHR_WORKER_PORT_END="+fmt.Sprint(port+1))
		}
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
		if bind {
			if _, err := worker.CallOnce(t.Context(), "/api/v1/session/bind", map[string]any{"token": "fixture-token"}); err != nil {
				_ = command.Process.Kill()
				_ = command.Wait()
				t.Fatal(err)
			}
		}
		return command
	}
	first := start(true)
	deadline := time.Now().Add(10 * time.Second)
	for (calls.Load() == 0 || (protectedRestore && planCalls.Load() == 0)) && time.Now().Before(deadline) {
		time.Sleep(100 * time.Millisecond)
	}
	_ = first.Process.Kill()
	_ = first.Wait()
	if calls.Load() == 0 || (protectedRestore && planCalls.Load() == 0) {
		t.Fatal("退出前没有实际补传尝试")
	}
	accept.Store(true)
	second := start(!protectedRestore)
	defer func() {
		if executeScheduled {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			_, _ = worker.CallOnce(ctx, "/api/v1/browser/stop", map[string]any{})
			_, _ = worker.CallOnce(ctx, "/api/v1/worker/stop", map[string]any{})
		}
		_ = second.Process.Kill()
		_ = second.Wait()
	}()
	deadline = time.Now().Add(12 * time.Second)
	for (calls.Load() < 2 || (protectedRestore && planCalls.Load() < 2)) && time.Now().Before(deadline) {
		time.Sleep(100 * time.Millisecond)
	}
	if calls.Load() < 2 || (protectedRestore && planCalls.Load() < 2) {
		t.Fatal("进程重启未恢复补传")
	}
	if scheduledRestore && !executeScheduled {
		deadline = time.Now().Add(12 * time.Second)
		for scheduledCalls.Load() == 0 && time.Now().Before(deadline) {
			time.Sleep(100 * time.Millisecond)
		}
		if scheduledCalls.Load() == 0 {
			t.Fatal("登录自动恢复后没有后台定时领取")
		}
		previousCalls := scheduledCalls.Load()
		_ = second.Process.Kill()
		_ = second.Wait()
		second = start(false)
		deadline = time.Now().Add(12 * time.Second)
		for scheduledCalls.Load() <= previousCalls && time.Now().Before(deadline) {
			time.Sleep(100 * time.Millisecond)
		}
		if scheduledCalls.Load() <= previousCalls {
			t.Fatal("账号忙队列在再次进程重启后没有继续")
		}
		mu.Lock()
		originalClaim, _ := json.Marshal(scheduledClaims[0])
		for _, claim := range scheduledClaims {
			actual, _ := json.Marshal(claim)
			if string(actual) != string(originalClaim) {
				t.Error("忙等待再次重启改变原领取编号、日期或凭证")
			}
		}
		mu.Unlock()
	}
	mu.Lock()
	for _, receipt := range receipts {
		wanted, _ := json.Marshal(original)
		actual, _ := json.Marshal(receipt)
		if string(wanted) != string(actual) {
			t.Error("重启改变了原编号或发送事实")
		}
	}
	if protectedRestore {
		for _, body := range planBodies {
			wanted, _ := json.Marshal(planUpdate)
			actual, _ := json.Marshal(body)
			if string(wanted) != string(actual) {
				t.Error("重启改变了原计划释放请求")
			}
		}
	}
	mu.Unlock()
	db, err = localdb.Open(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if executeScheduled {
		deadline = time.Now().Add(60 * time.Second)
		var final planmodel.Run
		for time.Now().Before(deadline) {
			mu.Lock()
			runID := executed.Run.ID
			mu.Unlock()
			if runID != "" {
				final, err = db.PlanRunSnapshot(t.Context(), planScope, runID)
				if err == nil && final.State == "completed" {
					break
				}
			}
			time.Sleep(100 * time.Millisecond)
		}
		if final.State != "completed" || final.CurrentItem != 1 || final.Items[0].Actions["greeting"].Count != 1 || scheduledCalls.Load() != 1 {
			entries, _ := db.ListPositionLogs("native-java", 40)
			for _, entry := range entries {
				t.Log(entry.Message)
			}
			if raw, e := os.ReadFile(executionLedger); e == nil {
				t.Log("实际受控页面台账", string(raw))
			}
			raw, _ := os.ReadFile(filepath.Join(cfg.DataDir, "runtime", "logs", "local-agent.log"))
			for _, line := range strings.Split(string(raw), "\n") {
				if strings.Contains(line, "执行计划") {
					t.Log(line)
				}
			}
			t.Fatal("实际重启定时计划未完成", err, final.State)
		}
		checkpoint, e := db.LoadActionCheckpoint(t.Context(), final.Items[0].ID)
		if e != nil || checkpoint.Greeted != 1 || checkpoint.CloudRunID != final.Items[0].TaskRunID {
			t.Fatal("重启动作没有原 TaskRun 检查点", e)
		}
		raw, e := os.ReadFile(executionLedger)
		var actual struct {
			Greets []string `json:"greetOrder"`
			Sends  []int    `json:"sendOrder"`
		}
		if e != nil || json.Unmarshal(raw, &actual) != nil || len(actual.Greets) != 1 || actual.Greets[0] != "java-person" || len(actual.Sends) != 0 {
			t.Fatal("实际程序没有按计划执行一次或额外发消息", e)
		}
		firstLedger := string(raw)
		var report localdb.PlanReportRecord
		deadline = time.Now().Add(12 * time.Second)
		for time.Now().Before(deadline) {
			report, err = db.PlanReportSnapshot(t.Context(), planScope, final.ID)
			mu.Lock()
			attempted := len(reportGate.Summaries) > 0
			mu.Unlock()
			if err == nil && attempted {
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
		mu.Lock()
		offlineAttempted := len(reportGate.Summaries) > 0
		mu.Unlock()
		if err != nil || !offlineAttempted || report.UploadState != "pending" || report.Report.Items[0].Actions["greeting"].Confirmed != 1 {
			t.Fatal("离线报告没有保存原实际结果", err)
		}
		_, _ = worker.CallOnce(t.Context(), "/api/v1/browser/stop", map[string]any{})
		_, _ = worker.CallOnce(t.Context(), "/api/v1/worker/stop", map[string]any{})
		_ = second.Process.Kill()
		_ = second.Wait()
		_ = db.Close()
		mu.Lock()
		reportGate.Ready = true
		mu.Unlock()
		second = start(false)
		db, err = localdb.Open(cfg)
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		deadline = time.Now().Add(12 * time.Second)
		var confirmed localdb.PlanReportRecord
		for time.Now().Before(deadline) {
			confirmed, err = db.PlanReportSnapshot(t.Context(), planScope, final.ID)
			if err == nil && confirmed.UploadState == "confirmed" {
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
		if err != nil || confirmed.UploadState != "confirmed" || confirmed.BodyHash != report.BodyHash || !confirmed.Report.GeneratedAt.Equal(report.Report.GeneratedAt) || scheduledCalls.Load() != 1 {
			t.Fatal("再次实际重启没有补传原报告或重复领取已结束任务", err)
		}
		mu.Lock()
		for _, summary := range reportGate.Summaries {
			if string(summary) != string(reportGate.Summaries[0]) {
				t.Error("断网重启重新生成报告内容")
			}
		}
		mu.Unlock()
		raw, err = os.ReadFile(executionLedger)
		if err != nil || string(raw) != firstLedger || json.Unmarshal(raw, &actual) != nil || len(actual.Greets) != 1 {
			t.Fatal("报告补传再次执行了招聘动作", err)
		}
	}
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
	if protectedRestore {
		deadline = time.Now().Add(3 * time.Second)
		for {
			operation, e := db.PlanOperation(t.Context(), planScope, planUpdate.RequestID)
			if e != nil {
				t.Fatal(e)
			}
			if operation.State == "confirmed" {
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("实际进程重启后计划原回执未确认")
			}
			time.Sleep(50 * time.Millisecond)
		}
	}
}
