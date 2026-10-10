// 本文件用受控 Worker 和真实 M1 扫描验证 HRPlus 安全边界暂停、队列保留、原计数恢复及收尾等待。
package positionrunner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"goodhr5/local-agent-go/internal/browser"
	"goodhr5/local-agent-go/internal/cloudapi"
	"goodhr5/local-agent-go/internal/localai"
	"goodhr5/local-agent-go/internal/localdb"
	"goodhr5/local-agent-go/internal/planmodel"
	"goodhr5/local-agent-go/internal/platformcore"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// planScanWorker 只替换页面事实，候选人扫描、打招呼主流程和 SQLite 均使用实际实现。
type planScanWorker struct {
	fakeWorker
	anchorsChanged bool
	panicStart     bool
}

// Start 在故障场景模拟启动边界意外中断，其余复用既有受控 Worker。
func (w *planScanWorker) Start(ctx context.Context) (browser.WorkerStatus, error) {
	if w.panicStart {
		panic("fixture panic")
	}
	return w.fakeWorker.Start(ctx)
}

// Call 返回三个不同真实格式的候选人 ID，其余复用既有受控 Worker。
func (w *planScanWorker) Call(ctx context.Context, path string, payload any) (map[string]any, error) {
	switch path {
	case "/api/v1/boss/candidates/capture-anchors":
		w.calls = append(w.calls, path)
		return map[string]any{"data": map[string]any{"valid": true, "anchors": mapValue(payload)["anchors"], "signature": "fixture-job-filter"}}, nil
	case "/api/v1/boss/candidates/check-anchors":
		w.calls = append(w.calls, path)
		return map[string]any{"data": map[string]any{"matched": !w.anchorsChanged, "reason": "fixture-list-changed"}}, nil
	case "/api/v1/boss/candidates/rewind":
		w.calls = append(w.calls, path)
		return map[string]any{"data": map[string]any{"rewound": true}}, nil
	}
	if path == "/api/v1/boss/candidates/extract" {
		w.calls = append(w.calls, path)
		items := []any{}
		for i := 0; i < 3; i++ {
			items = append(items, map[string]any{"id": fmt.Sprintf("fixture-geek-%d", i), "name": "候选人", "candidate_name": "候选人", "status": "scanned", "raw_text": "本科 5年", "fields": map[string]any{"name": "候选人", "basic_info": "本科 5年"}})
		}
		return map[string]any{"data": map[string]any{"candidates": items}}, nil
	}
	return w.fakeWorker.Call(ctx, path, payload)
}

// TestPlanScanDelegatesAtSafeBoundary 验证主扫描挂起后只允许一个其他角色，收尾前不能恢复或释放父占用。
func TestPlanScanDelegatesAtSafeBoundary(t *testing.T) {
	for _, changed := range []bool{false, true} {
		t.Run(fmt.Sprintf("changed-%v", changed), func(t *testing.T) {
			child, permit, snapshot, worker := planScanFixture(t)
			if err := child.SuspendAtBoundary(); !errors.Is(err, ErrPlanBrowserCleanup) {
				t.Fatal("尚未进入安全边界也挂起", err)
			}
			if _, err := child.ScanStep(permit, snapshot); err != nil {
				t.Fatal(err)
			}
			if err := child.SuspendAtBoundary(); err != nil || child.Valid() || !child.parent.Valid() {
				t.Fatal("挂起取消了父占用或没有让出角色", err)
			}
			checkpoint, err := child.parent.runner.db.LoadActionCheckpoint(t.Context(), child.state.options.LocalRunID)
			if err != nil {
				t.Fatal(err)
			}
			other := checkpoint
			other.ItemRunID = permit.Run.Items[1].ID
			other.CloudRunID = "70000000-0000-0000-0000-000000000002"
			other.PositionID = "other-position"
			other.TaskType = "auto_reply"
			other.AccountBound = false
			if _, err = child.parent.runner.db.EnsurePlanActionRun(t.Context(), other); err != nil {
				t.Fatal(err)
			}
			message, err := child.parent.BorrowItem(other.OwnerScope, other.ItemRunID, StartOptions{TaskType: "auto_reply", CloudRunID: other.CloudRunID})
			if err != nil {
				t.Fatal("不能继承父占用处理其他角色", err)
			}
			if err = child.ResumeAtBoundary(); !errors.Is(err, ErrPlanBrowserCleanup) {
				t.Fatal("其他角色未收尾仍恢复主扫描", err)
			}
			if err = child.parent.Release(true); !errors.Is(err, ErrPlanBrowserCleanup) {
				t.Fatal("有挂起和消息引用仍释放父占用", err)
			}
			if err = message.ReleaseAfterCleanup(true); err != nil {
				t.Fatal(err)
			}
			worker.anchorsChanged = changed
			if err = child.ResumeAtBoundary(); err != nil || !child.Valid() {
				t.Fatal("其他角色收尾后没有恢复原句柄", err)
			}
			step, err := child.ScanStep(permit, snapshot)
			if err != nil || step.Greeted != 2 || step.Scanned != 3 {
				t.Fatal("恢复重置计数或重复处理上一人", step, err)
			}
			extracts, rewinds := 0, 0
			for _, path := range worker.calls {
				if path == "/api/v1/boss/candidates/extract" {
					extracts++
				}
				if path == "/api/v1/boss/candidates/rewind" {
					rewinds++
				}
			}
			if (!changed && (extracts != 1 || rewinds != 0)) || (changed && (extracts != 2 || rewinds != 1)) {
				t.Fatal("锚点匹配与变化没有采用不同恢复路径", extracts, rewinds)
			}
		})
	}
}

// TestPlanScanWindowResumeKeepsCounts 验证交还后用新的父占用恢复同一项，旧数量不重复计入累计值。
func TestPlanScanWindowResumeKeepsCounts(t *testing.T) {
	child, permit, snapshot, _ := planScanFixture(t)
	if _, err := child.ScanStep(permit, snapshot); err != nil {
		t.Fatal(err)
	}
	if err := child.ReleaseAfterCleanup(true); err != nil {
		t.Fatal(err)
	}
	if err := child.parent.Release(true); err != nil {
		t.Fatal(err)
	}
	checkpoint, err := child.parent.runner.db.LoadActionCheckpoint(t.Context(), child.state.options.LocalRunID)
	if err != nil {
		t.Fatal(err)
	}
	parent, err := child.parent.runner.ReservePlanBrowser(t.Context(), permit.Run.ID)
	if err != nil {
		t.Fatal(err)
	}
	resumed, err := parent.BorrowItem(checkpoint.OwnerScope, checkpoint.ItemRunID, child.state.options)
	if err != nil {
		t.Fatal(err)
	}
	permit.Owner.OwnerID = "40000000-0000-0000-0000-000000000099"
	permit.Run.OwnerID = permit.Owner.OwnerID
	resumed.ownerID = permit.Owner.OwnerID
	t.Cleanup(func() { _ = resumed.ReleaseAfterCleanup(true); _ = parent.Release(true) })
	step, err := resumed.ScanStep(permit, snapshot)
	if err != nil || step.Greeted != 2 || step.Scanned != 3 {
		t.Fatal("新窗口把原项从零执行或重复读取计数", step, err)
	}
	position, err := parent.runner.db.GetPosition(snapshot.Position.ID)
	if err != nil || position.GreetedCount != 2 || position.ScannedCount != 3 {
		t.Fatal("恢复把历史数量再次累计", err, position.GreetedCount, position.ScannedCount)
	}
}

// CallOnce 使用受控真实页面事实，但不改变 M1 单次发送的编排保护。
func (w *planScanWorker) CallOnce(ctx context.Context, path string, payload any) (map[string]any, error) {
	return w.Call(ctx, path, payload)
}

// planScanFixture 建立原计划、云端所有者、真实 M1 检查点和本地父子占用。
func planScanFixture(t *testing.T, keywords ...[]string) (*PlanItemReservation, planmodel.Permit, PositionRuntimeSnapshot, *planScanWorker) {
	t.Helper()
	cloud := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		value := map[string]any{"ok": true}
		if r.URL.Path == "/api/auth/me" {
			value["user"] = map[string]any{"email": "fixture@example.com"}
		}
		_ = json.NewEncoder(w).Encode(value)
	}))
	t.Cleanup(cloud.Close)
	worker := &planScanWorker{}
	r := newTestRunner(t, openRunnerTestDB(t), worker)
	selectedKeywords := []string{"本科"}
	if len(keywords) > 0 {
		selectedKeywords = keywords[0]
	}
	position, err := r.db.CreatePosition(map[string]any{"name": "本地岗位运行", "platform_id": "boss", "mode": "keyword", "match_limit": 3, "position_snapshot": map[string]any{"name": "本地岗位运行", "keywords": selectedKeywords, "common_config": map[string]any{"mode_default": "keyword"}}})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join("..", "planmodel", "testdata", "permit.json"))
	if err != nil {
		t.Fatal(err)
	}
	var permit planmodel.Permit
	if err = json.Unmarshal(raw, &permit); err != nil {
		t.Fatal(err)
	}
	permit.Run.Snapshot.Items[0].PositionID = position.ID
	permit.Run.Snapshot.Items[0].Actions = []string{"greeting"}
	permit.Run.Snapshot.Items[0].PrioritizeReply = false
	permit.Run.Items[0].Snapshot = permit.Run.Snapshot.Items[0]
	permit.Run.Items[0].TaskRunID = "70000000-0000-0000-0000-000000000001"
	permit.Run.Items[0].State = "running"
	permit.Run.Items[0].Actions = map[string]planmodel.ActionProgress{"greeting": {State: "active"}}
	permit.Run.State, permit.Owner.State = "running", "running"
	ownerScope := CloudOwnerScope(cloud.URL, "fixture@example.com")
	checkpoint, err := r.db.EnsurePlanActionRun(t.Context(), localdb.ActionCheckpoint{PlanRunID: permit.Run.ID, ItemRunID: permit.Run.Items[0].ID, CloudRunID: permit.Run.Items[0].TaskRunID, OwnerScope: ownerScope, PositionID: position.ID, Platform: "boss", ProfileScope: platformcore.ReplyHash("profile:default"), TaskType: "greeting", PositionSnapshot: position.PositionSnapshot})
	if err != nil {
		t.Fatal(err)
	}
	parent, err := r.ReservePlanBrowser(t.Context(), permit.Run.ID)
	if err != nil {
		t.Fatal(err)
	}
	options := StartOptions{TaskType: "greeting", CloudRunID: checkpoint.CloudRunID, MachineID: permit.Owner.MachineID, Token: "fixture-token", CloudAPIBase: cloud.URL, EnableGreet: true, PageReadyDelay: 1, detailOpenProbabilitySet: true}
	child, err := parent.BorrowItem(ownerScope, checkpoint.ItemRunID, options)
	if err != nil {
		t.Fatal(err)
	}
	child.ownerID = permit.Owner.OwnerID
	snapshot := PositionRuntimeSnapshot{Position: position, Options: child.state.options, PlatformConfig: cloudapi.PlatformConfig{"auth": map[string]any{"pages": []any{map[string]any{"url": "https://www.zhipin.com/web/chat/recommend", "entry": true}}}, "position": map[string]any{"current": map[string]any{"selector": ".fixture-job"}}}}
	t.Cleanup(func() { _ = child.ReleaseAfterCleanup(true); _ = parent.Release(true) })
	return child, permit, snapshot, worker
}

// TestPlanScanFilteredBatchYields 验证整批初筛跳过仍交还调度，不等待更多候选人后才检查消息。
func TestPlanScanFilteredBatchYields(t *testing.T) {
	child, permit, snapshot, worker := planScanFixture(t, []string{"金融"})
	step, err := child.ScanStep(permit, snapshot)
	if err != nil || step.Done || step.Scanned != 3 || step.Skipped != 3 || step.Greeted != 0 {
		t.Fatal("全跳过批次没有保存后交还", step, err)
	}
	for _, path := range worker.calls {
		if path == "/api/v1/boss/candidates/greet" {
			t.Fatal("初筛未通过仍发送招呼")
		}
	}
}

// TestPlanScanStepPausesAndKeepsQueue 验证每人安全完成后停止领取，继续时复用同一浏览器和队列。
func TestPlanScanStepPausesAndKeepsQueue(t *testing.T) {
	child, permit, snapshot, worker := planScanFixture(t)
	for want := 1; want <= 3; want++ {
		step, err := child.ScanStep(permit, snapshot)
		if err != nil || step.Done || step.Greeted != want || step.Scanned != 3 || step.Remaining != 3-want {
			t.Fatal("扫描未在本项安全边界交还", step, err)
		}
		if !child.parent.Valid() || !child.Valid() {
			t.Fatal("暂停扫描释放了原占用")
		}
	}
	step, err := child.ScanStep(permit, snapshot)
	if err != nil || !step.Done || step.Greeted != 3 {
		t.Fatal("达到本项上限未结束", step, err)
	}
	starts, extracts, greets := 0, 0, 0
	for _, path := range worker.calls {
		switch path {
		case "start":
			starts++
		case "/api/v1/boss/candidates/extract":
			extracts++
		case "/api/v1/boss/candidates/greet":
			greets++
		}
	}
	if starts != 1 || extracts != 1 || greets != 3 {
		t.Fatal("恢复步骤重新启动页面或重复提取", starts, extracts, greets)
	}
}

// TestPlanScanCleanupCancelsPausedActor 验证暂停在边界的协程取消并完成收尾后才交还引用。
func TestPlanScanCleanupCancelsPausedActor(t *testing.T) {
	child, permit, snapshot, _ := planScanFixture(t)
	if _, err := child.ScanStep(permit, snapshot); err != nil {
		t.Fatal(err)
	}
	if err := child.ReleaseAfterCleanup(true); err != nil || !child.scan.finished || !child.parent.Valid() {
		t.Fatal("交还引用时仍有扫描协程或释放了父占用", err)
	}
	checkpoint, err := child.parent.runner.db.LoadActionCheckpoint(t.Context(), child.state.options.LocalRunID)
	if err != nil || checkpoint.Greeted != 1 || len(checkpoint.Queue) != 2 {
		t.Fatal("收尾取消丢失本项进度", err)
	}
}

// TestPlanScanRestoredLimitDoesNotOpenPage 验证已有成功数量达到上限后直接结束，不重置数量或启动页面。
func TestPlanScanRestoredLimitDoesNotOpenPage(t *testing.T) {
	child, permit, snapshot, worker := planScanFixture(t)
	checkpoint, err := child.parent.runner.db.LoadActionCheckpoint(t.Context(), child.state.options.LocalRunID)
	if err != nil {
		t.Fatal(err)
	}
	checkpoint.Greeted, checkpoint.Scanned = 3, 12
	if err = child.parent.runner.db.SaveActionCheckpoint(t.Context(), checkpoint); err != nil {
		t.Fatal(err)
	}
	step, err := child.ScanStep(permit, snapshot)
	if err != nil || !step.Done || step.Greeted != 3 || step.Scanned != 12 || len(worker.calls) != 0 {
		t.Fatal("恢复重置数量或提前打开页面", step, err)
	}
}

// TestPlanScanPanicReturnsTerminalFrame 验证意外中断会返回明确失败终态，不让父流程等待遗失的边界。
func TestPlanScanPanicReturnsTerminalFrame(t *testing.T) {
	child, permit, snapshot, worker := planScanFixture(t)
	worker.panicStart = true
	step, err := child.ScanStep(permit, snapshot)
	if err == nil || !step.Done || !child.parent.Valid() {
		t.Fatal("意外中断未报告或清除了父占用", step, err)
	}
	if err = child.ReleaseAfterCleanup(true); err != nil {
		t.Fatal("终态后子引用不能收尾", err)
	}
}

// TestPlanBackgroundAIHasNoPageOverlay 验证计划后台预评分不更新页面，页面切换期间仍可安全计算数据。
func TestPlanBackgroundAIHasNoPageOverlay(t *testing.T) {
	worker := &planScanWorker{}
	runner := newTestRunner(t, openRunnerTestDB(t), worker)
	client := localai.New(localdb.AIConfig{})
	actual, cleanup := runner.aiClientForCall(t.Context(), platformExecutor{runner: runner, positionID: "fixture", noOverlay: true}, client, "后台预评分", "候选人", "受控计算")
	cleanup()
	if actual != client || len(worker.calls) != 0 {
		t.Fatal("后台预评分调用了页面操作")
	}
}
