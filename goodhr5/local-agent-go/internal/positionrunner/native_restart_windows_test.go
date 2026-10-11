// 本文件使用实际 Windows 本地程序进程和同一 SQLite 验证登录、收据及定时安排恢复；招聘执行只使用隔离页面。
package positionrunner

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/google/uuid"
	"goodhr5/local-agent-go/internal/cloudapi"
	"goodhr5/local-agent-go/internal/config"
	"goodhr5/local-agent-go/internal/localdb"
	"goodhr5/local-agent-go/internal/planmodel"
	"goodhr5/local-agent-go/internal/planoperations"
	"goodhr5/local-agent-go/internal/platformcore"
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
	var expired atomic.Bool
	var expiredChecks atomic.Int32
	var mu sync.Mutex
	var receipts []cloudapi.ReGreetReceiptRequest
	var planBodies []cloudapi.PlanRunUpdateRequest
	var planPermit planmodel.Permit
	scheduledRestore := len(scheduled) > 0 && scheduled[0]
	executeScheduled := len(scheduled) > 1 && scheduled[1]
	var executed planmodel.Permit
	var yesterday planmodel.Run
	var unknownOriginal localdb.ActionCheckpoint
	var executionLedger string
	archivedRuns := map[string]planmodel.Permit{}
	archivedReplies := map[string]map[string][]byte{}
	archivedReports := map[string]*nativeRestartReportGate{}
	executionReplies := map[string][]byte{}
	reportGate := nativeRestartReportGate{}
	lifecycle := os.Getenv("HRPLUS_M2_ACCEPTANCE_SCENARIO") == "stop-edit"
	realWindows := os.Getenv("HRPLUS_M2_ACCEPTANCE_SCENARIO") == "across-windows"
	preparingSecond := make(chan struct{})
	finishHeldPreparation := make(chan struct{})
	var prepareOnce sync.Once
	var scheduledPlan planmodel.Plan
	var scheduledClaims []cloudapi.PlanClaimRequest
	var scheduledClaimTimes []time.Time
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
				configureInstalledPlanScenario(t, &config)
			}
			scheduledPlan = planmodel.Plan{ID: planPermit.Run.PlanID, UserEmail: "restart@example.com", MachineID: "fixture-machine", Version: 1, StateSequence: 1, ActivationID: "30000000-0000-0000-0000-000000000002", State: "enabled", Config: config, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
			if e = scheduledPlan.Validate(); e != nil {
				t.Fatal(e)
			}
		}
	}
	cloud := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if expired.Load() {
			if r.URL.Path == "/api/auth/me" {
				expiredChecks.Add(1)
			} else {
				t.Errorf("过期登录仍访问云端业务接口 %s", r.URL.Path)
			}
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.URL.Path != "/api/platforms/config/" && r.Header.Get("Authorization") != "Bearer fixture-token" {
			w.WriteHeader(http.StatusUnauthorized)
			return // 固定端口不向原主控台登录返回虚构账号、计划或许可。
		}
		if r.URL.Path == "/api/execution-plan-events" {
			w.WriteHeader(http.StatusServiceUnavailable)
			return // 固定端口期间主控台旧事件连接保持不可用，不伪造事件确认。
		}
		if executeScheduled {
			mu.Lock()
			for id, original := range archivedRuns {
				if strings.HasPrefix(r.URL.Path, "/api/execution-plan-runs/"+id) {
					handled := nativeRestartExecutionCloud(t, w, r, &original, archivedReplies[id], archivedReports[id])
					archivedRuns[id] = original
					mu.Unlock()
					if handled {
						return
					}
					mu.Lock()
				}
			}
			hold := lifecycle && scheduledPlan.Version == 1 && len(executed.Run.Items) > 1 && r.URL.Path == "/api/execution-plan-runs/"+executed.Run.ID+"/items/"+executed.Run.Items[1].ID+"/prepare"
			mu.Unlock()
			if hold {
				prepareOnce.Do(func() { close(preparingSecond) })
				select {
				case <-r.Context().Done():
				case <-finishHeldPreparation:
				}
				return
			}
			mu.Lock()
			if os.Getenv("HRPLUS_M2_ACCEPTANCE_SCENARIO") == "timed-compound" {
				raw, _ := os.ReadFile(executionLedger)
				var ledger struct {
					Greets []string `json:"greetOrder"`
				}
				_ = json.Unmarshal(raw, &ledger)
				reportGate.ScanStarted = len(ledger.Greets) > 0
			}
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
		case "/api/execution-plans/" + scheduledPlan.ID + "/confirm-stop":
			mu.Lock()
			var input cloudapi.PlanStopRequest
			if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
				t.Error(err)
			}
			if !lifecycle || input.Validate() != nil || input.ExpectedVersion != scheduledPlan.Version || input.ActivationID != scheduledPlan.ActivationID || input.MachineID != scheduledPlan.MachineID || executed.Owner.State != "released" || executed.Run.State != "stopped" {
				t.Error("停止确认未等待原页面及占用收尾")
				w.WriteHeader(409)
				mu.Unlock()
				return
			}
			scheduledPlan.StopRequested = false
			scheduledPlan.StateSequence++
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "plan": scheduledPlan})
			mu.Unlock()
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
			scheduledClaimTimes = append(scheduledClaimTimes, time.Now())
			mu.Unlock()
			scheduledCalls.Add(1)
			if executeScheduled {
				mu.Lock()
				if realWindows && executed.Run.ID == claim.RunID && executed.Run.State == "waiting_window" && executed.Owner.State == "released" {
					if executed.Run.Items[0].Actions["greeting"].Count != 1 || executed.Run.CurrentItem != 0 {
						t.Error("跨窗口没有保留第一项真实进度")
					}
					executed.Run.Sequence++
					executed.Run.State = "starting"
					executed.Run.OwnerID = claim.OwnerID
					executed.Owner = planmodel.Owner{OwnerID: claim.OwnerID, OwnerType: "plan", MachineID: claim.MachineID, State: "starting"}
					_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "permit": executed})
					mu.Unlock()
					return
				}
				if lifecycle && executed.Run.ID != "" && executed.Run.ID != claim.RunID {
					raw, _ := json.Marshal(executed)
					var original planmodel.Permit
					_ = json.Unmarshal(raw, &original)
					archivedRuns[original.Run.ID] = original
					archivedReplies[original.Run.ID] = executionReplies
					reports := reportGate
					archivedReports[original.Run.ID] = &reports
					executionReplies = map[string][]byte{}
					reportGate = nativeRestartReportGate{}
				}
				executed = planmodel.Permit{Run: planmodel.Run{ID: claim.RunID, PlanID: claim.PlanID, ActivationID: claim.ActivationID, ExecutionDate: claim.ExecutionDate, ConfigVersion: claim.ExpectedVersion, Sequence: 1, State: "starting", Snapshot: scheduledPlan.Config, OwnerID: claim.OwnerID, Items: []planmodel.ItemRun{{ID: "50000000-0000-0000-0000-000000000099", ItemID: scheduledPlan.Config.Items[0].ID, Order: 0, Snapshot: scheduledPlan.Config.Items[0], State: "pending", Actions: map[string]planmodel.ActionProgress{"greeting": {State: "pending"}}}}}, Owner: planmodel.Owner{OwnerID: claim.OwnerID, OwnerType: "plan", MachineID: claim.MachineID, State: "starting"}}
				executed.Run.Items = nil
				for index, item := range scheduledPlan.Config.Items {
					actions := map[string]planmodel.ActionProgress{}
					for _, action := range item.Actions {
						actions[action] = planmodel.ActionProgress{State: "pending"}
					}
					itemRunID := fmt.Sprintf("50000000-0000-0000-0000-%012d", index+99)
					if lifecycle {
						itemRunID = uuid.NewSHA1(uuid.NameSpaceOID, []byte(claim.RunID+":"+item.ID)).String()
					}
					executed.Run.Items = append(executed.Run.Items, planmodel.ItemRun{ID: itemRunID, ItemID: item.ID, Order: index, Snapshot: item, State: "pending", Actions: actions})
				}
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
			mu.Lock()
			plans := []any{}
			if scheduledRestore && accept.Load() {
				plans = append(plans, scheduledPlan)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "plans": plans})
			mu.Unlock()
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
	startNativeAcceptanceCloud(t, cloud)
	defer cloud.Close()
	defer cloud.CloseClientConnections()
	defer close(finishHeldPreparation)
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
	if os.Getenv("HRPLUS_M2_ACCEPTANCE_SCENARIO") == "unknown-send" {
		unknownOriginal, err = db.CreateActionRun(t.Context(), localdb.ActionCheckpoint{PositionID: "fixture-old-position", Platform: "boss", ProfileScope: platformcore.ReplyHash("profile:default")})
		if err != nil {
			t.Fatal(err)
		}
		if err = db.SaveActionCandidate(t.Context(), unknownOriginal, "opaque-C", "processing", "greet_sending"); err != nil {
			t.Fatal(err)
		}
	}
	if os.Getenv("HRPLUS_M2_ACCEPTANCE_SCENARIO") == "next-day-offline-report" {
		yesterday = planPermit.Run
		yesterday.ID, yesterday.ActivationID = uuid.NewString(), scheduledPlan.ActivationID
		yesterday.Snapshot = scheduledPlan.Config
		zone, err := time.LoadLocation(scheduledPlan.Config.Schedule.Timezone)
		if err != nil {
			t.Fatal(err)
		}
		yesterday.ExecutionDate = time.Now().In(zone).AddDate(0, 0, -1).Format("2006-01-02")
		yesterday.State, yesterday.CurrentItem, yesterday.EndReason = "incomplete", 0, "window_ended"
		finished := time.Now().Add(-24 * time.Hour).UTC()
		yesterday.FinishedAt = &finished
		item := scheduledPlan.Config.Items[0]
		yesterday.Items = []planmodel.ItemRun{{ID: uuid.NewString(), ItemID: item.ID, Order: 0, Snapshot: item, TaskRunID: uuid.NewString(), State: "stopped", Actions: map[string]planmodel.ActionProgress{"greeting": {State: "stopped", Count: 1}}}}
		if err := db.SavePlanRunSnapshot(t.Context(), planScope, yesterday); err != nil {
			t.Fatal(err)
		}
		previous := planmodel.Permit{Run: yesterday, Owner: planmodel.Owner{OwnerID: yesterday.OwnerID, OwnerType: "plan", MachineID: scheduledPlan.MachineID, State: "released"}}
		archivedRuns[yesterday.ID] = previous
		archivedReplies[yesterday.ID] = map[string][]byte{}
		archivedReports[yesterday.ID] = &nativeRestartReportGate{}
	}
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
	executable := nativeAcceptanceExecutable(t, root, directory)
	executionLedger = filepath.Join(directory, "execution-ledger.json")
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
			mode := "m2-plans"
			if nativeCompoundScenario() {
				mode = "triple-job"
			}
			if os.Getenv("HRPLUS_M2_ACCEPTANCE_SCENARIO") == "timed-compound" {
				mode = "triple-timed-job"
			}
			command.Env = append(command.Env, "NODE_OPTIONS=--import=file:///"+preload, "HRPLUS_M2_NATIVE_BROWSER_SOURCE="+cachedBrowser, "HRPLUS_M1_FIXTURE_MODE="+mode, "HRPLUS_M1_FIXTURE_LEDGER="+executionLedger, "GOODHR_WORKER_PORT_END="+fmt.Sprint(port+1))
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
	if os.Getenv("HRPLUS_M2_ACCEPTANCE_SCENARIO") == "expired-login" {
		expired.Store(true)
	}
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
	if expired.Load() {
		beforeReceipts, beforeUpdates := calls.Load(), planCalls.Load()
		time.Sleep(6 * time.Second)
		response, err := http.Get(worker.base + "/api/v1/session/status")
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		var status struct {
			Data struct {
				Verified  bool   `json:"verified"`
				UserEmail string `json:"user_email"`
			} `json:"data"`
		}
		if err = json.NewDecoder(response.Body).Decode(&status); err != nil || response.StatusCode != 200 || status.Data.Verified || status.Data.UserEmail != "" || expiredChecks.Load() == 0 || calls.Load() != beforeReceipts || planCalls.Load() != beforeUpdates || scheduledCalls.Load() != 0 {
			t.Fatal("失效登录仍恢复原身份或继续业务操作", err, status.Data)
		}
		t.Log("已安装程序拒绝过期登录；实际云端核对发生，补传与计划领取均未继续")
		return
	}
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
	if executeScheduled && os.Getenv("HRPLUS_M2_ACCEPTANCE_SCENARIO") == "manual-compat" {
		if _, err := worker.CallOnce(t.Context(), "/api/v1/local/positions/native-java/run", map[string]any{"token": "fixture-token", "task_type": "greeting"}); err != nil {
			t.Fatal("安装版原手动入口启动失败", err)
		}
		deadline = time.Now().Add(30 * time.Second)
		var ledger struct {
			Greets []string `json:"greetOrder"`
		}
		for time.Now().Before(deadline) {
			raw, _ := os.ReadFile(executionLedger)
			_ = json.Unmarshal(raw, &ledger)
			if len(ledger.Greets) > 0 {
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
		if strings.Join(ledger.Greets, "|") != "java-person" || scheduledCalls.Load() != 0 {
			t.Fatal("手动入口未实际发送或被计划抢占", ledger.Greets)
		}
		result, err := worker.CallOnce(t.Context(), "/api/v1/local/positions/native-java/stop", map[string]any{"token": "fixture-token", "run_id": "70000000-0000-0000-0000-000000000555", "force": true})
		data, _ := result["data"].(map[string]any)
		if err != nil || data["running"] != false || data["stop_cloud_confirmed"] != true {
			t.Fatal("原手动任务强制停止没有完成真实收尾及云端确认", err, result)
		}
		position, err := db.GetPosition("native-java")
		if err != nil || position.Status != "stopped" || position.GreetedCount != 1 {
			t.Fatal("手动停止丢失原岗位事实", err, position.Status, position.GreetedCount)
		}
		t.Log("原手动入口实际打招呼一次，强制停止确认原任务及Worker收尾，计划领取零")
		return
	}
	if executeScheduled && os.Getenv("HRPLUS_M2_ACCEPTANCE_SCENARIO") == "day-incomplete" {
		deadline = time.Now().Add(8 * time.Minute)
		var incomplete planmodel.Run
		for time.Now().Before(deadline) {
			mu.Lock()
			id := executed.Run.ID
			mu.Unlock()
			if id != "" {
				incomplete, err = db.PlanRunSnapshot(t.Context(), planScope, id)
			}
			if err == nil && incomplete.State == "incomplete" {
				break
			}
			time.Sleep(time.Second)
		}
		if incomplete.State != "incomplete" || incomplete.CurrentItem != 0 || incomplete.Items[0].Actions["greeting"].Count != 1 || incomplete.Items[1].TaskRunID != "" || scheduledCalls.Load() != 1 {
			t.Fatal("真实日末未按原进度结算", err, incomplete.State)
		}
		window := scheduledPlan.Config.Schedule.Windows[0]
		grace, err := db.PlanWindowGrace(t.Context(), planScope, scheduledPlan.ID, incomplete.ExecutionDate, window.StartMinute, window.EndMinute, func() time.Duration { t.Fatal("日末随机收尾未持久保存"); return 0 })
		day, _ := time.Parse("2006-01-02", incomplete.ExecutionDate)
		intervals, intervalErr := scheduledPlan.Config.Schedule.OnDate(day)
		if err != nil || intervalErr != nil || len(intervals) != 1 || grace < 3*time.Minute || grace > 6*time.Minute || time.Now().Before(intervals[0].End.Add(grace)) {
			t.Fatal("日末结算早于原随机边界", err, intervalErr, grace)
		}
		deadline = time.Now().Add(15 * time.Second)
		var report localdb.PlanReportRecord
		for time.Now().Before(deadline) {
			report, err = db.PlanReportSnapshot(t.Context(), planScope, incomplete.ID)
			mu.Lock()
			attempted := len(reportGate.Summaries) > 0
			mu.Unlock()
			if err == nil && attempted {
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
		mu.Lock()
		uploadAttempted := len(reportGate.Summaries) > 0
		mu.Unlock()
		if err != nil || !uploadAttempted || report.UploadState != "pending" || report.Report.Items[0].Actions["greeting"].Confirmed != 1 || len(report.Report.Items) != 2 {
			t.Fatal("未完成日报没有保留实际数量及未开始后项", err)
		}
		mu.Lock()
		stillEnabled := scheduledPlan.State == "enabled"
		mu.Unlock()
		if !stillEnabled {
			t.Fatal("日末未完成关闭了周期计划")
		}
		raw, _ := os.ReadFile(executionLedger)
		var ledger struct {
			Greets []string `json:"greetOrder"`
		}
		if json.Unmarshal(raw, &ledger) != nil || strings.Join(ledger.Greets, "|") != "java-person" {
			t.Fatal("日末出现后项或重复动作")
		}
		t.Log("真实最后窗口结算未完成；原计数和未执行销售保留，报告断网留存，周期计划仍启用；随机收尾", grace)
		return
	}
	if scenario := os.Getenv("HRPLUS_M2_ACCEPTANCE_SCENARIO"); executeScheduled && (scenario == "unknown-platform" || scenario == "unsupported-messages") {
		deadline = time.Now().Add(30 * time.Second)
		var rejected planmodel.Run
		for time.Now().Before(deadline) {
			mu.Lock()
			id := executed.Run.ID
			mu.Unlock()
			if id != "" {
				rejected, err = db.PlanRunSnapshot(t.Context(), planScope, id)
			}
			if err == nil && rejected.State == "blocked" {
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
		if rejected.State != "blocked" || rejected.EndReason == "" || scheduledCalls.Load() != 1 {
			t.Fatal("不支持的平台未拒绝原执行", err, rejected.State)
		}
		if _, err := os.Stat(executionLedger); !os.IsNotExist(err) {
			t.Fatal("拒绝平台仍产生招聘页面动作", err)
		}
		t.Log("安装程序拒绝平台/动作能力，页面台账不存在，原任务失败记录保留")
		return
	}
	if executeScheduled && realWindows {
		deadline = time.Now().Add(9 * time.Minute)
		var waiting planmodel.Run
		for time.Now().Before(deadline) {
			mu.Lock()
			id := executed.Run.ID
			mu.Unlock()
			if id != "" {
				waiting, err = db.PlanRunSnapshot(t.Context(), planScope, id)
				if err == nil && waiting.State == "waiting_window" {
					break
				}
			}
			time.Sleep(time.Second)
		}
		if waiting.State != "waiting_window" || waiting.Items[0].Actions["greeting"].Count != 1 || waiting.CurrentItem != 0 || waiting.Items[1].TaskRunID != "" {
			t.Fatal("真实首窗口未安全收尾或提前激活下一岗位", err, waiting.State)
		}
		firstWindow := scheduledPlan.Config.Schedule.Windows[0]
		grace, err := db.PlanWindowGrace(t.Context(), planScope, scheduledPlan.ID, waiting.ExecutionDate, firstWindow.StartMinute, firstWindow.EndMinute, func() time.Duration { t.Fatal("实装未保存原随机延迟"); return 0 })
		if err != nil || grace < 3*time.Minute || grace > 6*time.Minute {
			t.Fatal("随机收尾记录不正确", err, grace)
		}
		day, _ := time.Parse("2006-01-02", waiting.ExecutionDate)
		intervals, err := scheduledPlan.Config.Schedule.OnDate(day)
		if err != nil {
			t.Fatal(err)
		}
		if len(intervals) != 2 {
			t.Fatal("跨窗口日期没有两段安排")
		}
		if time.Now().Before(intervals[0].End.Add(grace)) {
			t.Fatal("真实收尾早于原随机边界")
		}
		firstTask := waiting.Items[0].TaskRunID
		deadline = time.Now().Add(6 * time.Minute)
		var final planmodel.Run
		for time.Now().Before(deadline) {
			final, err = db.PlanRunSnapshot(t.Context(), planScope, waiting.ID)
			if err == nil && final.State == "completed" {
				break
			}
			time.Sleep(time.Second)
		}
		if final.State != "completed" || final.ID != waiting.ID || final.CurrentItem != 2 || final.Items[0].TaskRunID != firstTask || final.Items[0].Actions["greeting"].Count != 1 || final.Items[1].Actions["greeting"].Count != 1 || scheduledCalls.Load() != 2 {
			t.Fatal("实际后续窗口没有沿原任务续跑", err, final.State)
		}
		mu.Lock()
		legalResume := len(scheduledClaimTimes) == 2 && !scheduledClaimTimes[1].Before(intervals[1].Start)
		mu.Unlock()
		if !legalResume {
			t.Fatal("第二次领取早于合法窗口或领取次数不正确")
		}
		raw, _ := os.ReadFile(executionLedger)
		var ledger struct {
			Greets []string `json:"greetOrder"`
		}
		if json.Unmarshal(raw, &ledger) != nil || strings.Join(ledger.Greets, "|") != "java-person|sales-person" {
			t.Fatalf("跨窗口重复或错岗位打招呼: %s", raw)
		}
		t.Log("真实跨窗口完成；原运行、第一项任务和计数保留；随机收尾", grace)
		return
	}
	if executeScheduled && lifecycle {
		select {
		case <-preparingSecond:
		case <-time.After(60 * time.Second):
			t.Fatal("旧批次没有到第二项准备点")
		}
		mu.Lock()
		oldRunID := executed.Run.ID
		oldTaskID := executed.Run.Items[0].TaskRunID
		if executed.Run.Items[0].Actions["greeting"].Count != 1 || executed.Run.Items[1].TaskRunID != "" {
			t.Fatal("停止边界不是第一项确认后、第二项动作前")
		}
		scheduledPlan.State = "stopped"
		scheduledPlan.StopRequested = true
		scheduledPlan.StateSequence++
		mu.Unlock()
		deadline = time.Now().Add(20 * time.Second)
		confirmed := false
		for time.Now().Before(deadline) {
			mu.Lock()
			confirmed = !scheduledPlan.StopRequested && executed.Run.State == "stopped" && executed.Owner.State == "released"
			mu.Unlock()
			if confirmed {
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
		if !confirmed {
			t.Fatal("用户停止未确认原批次收尾")
		}
		original, err := db.PlanRunSnapshot(t.Context(), planScope, oldRunID)
		if err != nil || original.State != "stopped" || original.Items[0].TaskRunID != oldTaskID || original.Items[0].Actions["greeting"].Count != 1 {
			t.Fatal("原运行及已确认计数丢失", err)
		}
		mu.Lock()
		scheduledPlan.Config.Items[0], scheduledPlan.Config.Items[1] = scheduledPlan.Config.Items[1], scheduledPlan.Config.Items[0]
		for i := range scheduledPlan.Config.Items {
			scheduledPlan.Config.Items[i].Order = i
		}
		scheduledPlan.Version++
		scheduledPlan.StateSequence++
		scheduledPlan.ActivationID = uuid.NewString()
		scheduledPlan.State = "enabled"
		mu.Unlock()
		deadline = time.Now().Add(90 * time.Second)
		var fresh planmodel.Run
		for time.Now().Before(deadline) {
			mu.Lock()
			id := executed.Run.ID
			mu.Unlock()
			if id != "" && id != oldRunID {
				fresh, err = db.PlanRunSnapshot(t.Context(), planScope, id)
				if err == nil && fresh.State == "completed" {
					break
				}
			}
			time.Sleep(100 * time.Millisecond)
		}
		if fresh.State != "completed" || fresh.CurrentItem != 2 || fresh.ID == oldRunID || fresh.ConfigVersion != 2 || fresh.Items[0].Snapshot.PositionID != "native-sales" || fresh.Items[0].Actions["greeting"].Count != 1 || scheduledCalls.Load() != 2 {
			t.Fatalf("编辑后的新批次未从第一项执行: %v %+v", err, fresh)
		}
		original, err = db.PlanRunSnapshot(t.Context(), planScope, oldRunID)
		if err != nil || original.State != "stopped" || original.Items[0].TaskRunID != oldTaskID || original.Items[0].Actions["greeting"].Count != 1 {
			t.Fatal("新批次覆盖原历史", err)
		}
		t.Log("实装停止确认→交换岗位→新批次第一项销售；原Java任务及计数保留")
		return
	}
	if executeScheduled && os.Getenv("HRPLUS_M2_ACCEPTANCE_SCENARIO") == "outside-window" {
		result, err := worker.CallOnce(t.Context(), "/api/v1/local/execution-plans/"+scheduledPlan.ID+"/start", map[string]any{"token": "fixture-token", "expected_version": scheduledPlan.Version, "activation_id": scheduledPlan.ActivationID})
		data, _ := result["data"].(map[string]any)
		if err != nil || data["status"] != "waiting_time" {
			t.Fatalf("时段外立即开始未保持等待: %v %+v", err, result)
		}
		time.Sleep(6 * time.Second)
		if scheduledCalls.Load() != 0 {
			t.Fatal("时段外仍领取了执行许可")
		}
		if _, err = os.Stat(executionLedger); !os.IsNotExist(err) {
			t.Fatal("时段外仍打开了招聘页面或产生动作", err)
		}
		t.Log("时段外立即开始：waiting_time，许可领取=0，招聘页面动作=0")
		return
	}
	if executeScheduled {
		expectedFirstGreet := 1
		compound := nativeCompoundScenario()
		if compound {
			expectedFirstGreet = 3
		}
		if unknownOriginal.RunID != "" {
			expectedFirstGreet = 2
		}
		deadline = time.Now().Add(150 * time.Second)
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
		if final.State != "completed" || final.CurrentItem != len(scheduledPlan.Config.Items) || final.Items[0].Actions["greeting"].Count != int64(expectedFirstGreet) || scheduledCalls.Load() != 1 {
			mu.Lock()
			t.Log("夹具原结束原因", executed.Run.EndReason)
			mu.Unlock()
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
		expectedScope := platformcore.ReplyHash("m1-account:" + platformcore.ReplyHash("profile:default") + ":boss:" + planScope + ":901")
		if e != nil || checkpoint.Greeted != expectedFirstGreet || checkpoint.CloudRunID != final.Items[0].TaskRunID || checkpoint.ProfileScope != expectedScope || !checkpoint.AccountBound || checkpoint.Platform != "boss" {
			t.Fatal("重启动作没有原 TaskRun 检查点", e)
		}
		raw, e := os.ReadFile(executionLedger)
		var actual struct {
			Greets   []string `json:"greetOrder"`
			Sends    []int    `json:"sendOrder"`
			Timeline []string `json:"timeline"`
		}
		expectedGreets, expectedSends := 1, 0
		firstGreet := "java-person"
		if compound {
			expectedGreets, expectedSends, firstGreet = 3, 2, "opaque-C"
		}
		if unknownOriginal.RunID != "" {
			expectedGreets, firstGreet = 2, "opaque-D"
		}
		if len(scheduledPlan.Config.Items) == 4 {
			expectedGreets, expectedSends = 2, 0
		}
		if e != nil || json.Unmarshal(raw, &actual) != nil || len(actual.Greets) != expectedGreets || actual.Greets[0] != firstGreet || len(actual.Sends) != expectedSends {
			entries, _ := db.ListPositionLogs("native-java", 100)
			for _, entry := range entries {
				if strings.Contains(entry.Message, "复打") {
					t.Log(entry.Message)
				}
			}
			t.Log("原动作计数", final.Items[0].Actions, "原复打统计", checkpoint.ReGreetStats)
			t.Fatalf("实际程序动作与原计划不符 err=%v 台账=%s", e, raw)
		}
		if compound {
			expectedTimeline := "message:123|message:124|greet:opaque-C|greet:opaque-D|greet:opaque-E"
			expectedGreetOrder := "opaque-C|opaque-D|opaque-E"
			if os.Getenv("HRPLUS_M2_ACCEPTANCE_SCENARIO") == "timed-compound" {
				expectedTimeline = "greet:opaque-C|message:123|message:124|greet:opaque-D|greet:opaque-E"
			}
			if unknownOriginal.RunID != "" {
				expectedTimeline = "message:123|message:124|greet:opaque-D|greet:opaque-E"
				expectedGreetOrder = "opaque-D|opaque-E"
			}
			if final.Items[0].Actions["auto_reply"].Count != 1 || final.Items[0].Actions["re_greet"].Count != 1 || fmt.Sprint(actual.Sends) != "[123 124]" || strings.Join(actual.Greets, "|") != expectedGreetOrder || strings.Join(actual.Timeline, "|") != expectedTimeline {
				t.Fatalf("三动作数量或优先穿插不正确: %v", actual.Timeline)
			}
			t.Logf("三动作确认数量：打招呼=%d，回复=1，复打=1；页面台账 %v", expectedFirstGreet, actual.Timeline)
		}
		if yesterday.ID != "" {
			prior, err := db.PlanRunSnapshot(t.Context(), planScope, yesterday.ID)
			mu.Lock()
			attempts := len(archivedReports[yesterday.ID].Summaries)
			mu.Unlock()
			if err != nil || prior.State != "incomplete" || prior.Items[0].Actions["greeting"].Count != 1 || final.ID == yesterday.ID || final.ExecutionDate == yesterday.ExecutionDate || final.Items[0].TaskRunID == yesterday.Items[0].TaskRunID || attempts == 0 {
				t.Fatal("昨日未完成报告丢失、沿用旧任务或离线报告阻止今日执行", err, attempts)
			}
			t.Log("受控昨日记录保留；昨日实际日报上传被503拒绝，今日新运行从第一项开始并确认一次")
			// 今日执行已经完成后才恢复昨日报告网络，随后继续核对今日原报告的断网重启。
			mu.Lock()
			archivedReports[yesterday.ID].Ready = true
			mu.Unlock()
		}
		if len(scheduledPlan.Config.Items) == 4 {
			if strings.Join(actual.Timeline, "|") != "greet:Java|select:销售|greet:销售|reply:Java|reply:销售" {
				t.Fatalf("四项页面顺序不正确: %v", actual.Timeline)
			}
			tasks := map[string]bool{}
			for _, item := range final.Items {
				if item.TaskRunID == "" || tasks[item.TaskRunID] {
					t.Fatal("重复岗位没有独立原任务")
				}
				tasks[item.TaskRunID] = true
				for _, action := range item.Snapshot.Actions {
					if item.Actions[action].Count != 1 {
						t.Fatal("单项数量不正确", item.Order, action, item.Actions[action])
					}
				}
			}
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
		if err != nil || !offlineAttempted || report.UploadState != "pending" || report.Report.Items[0].Actions["greeting"].Confirmed != int64(expectedFirstGreet) {
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
		if err != nil || string(raw) != firstLedger || json.Unmarshal(raw, &actual) != nil || len(actual.Greets) != expectedGreets {
			t.Fatal("报告补传再次执行了招聘动作", err)
		}
		if unknownOriginal.RunID != "" {
			state, err := db.ActionCandidateStatus(t.Context(), unknownOriginal.RunID, unknownOriginal.PositionID, "opaque-C")
			if err != nil || state != "processing" {
				t.Fatal("原未知发送保护被改写", err, state)
			}
			t.Log("原未知候选人opaque-C未重发、不计成功，历史保护保留；其他候选人正常执行")
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
