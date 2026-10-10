// 本文件使用 Windows 加密、临时 SQLite 与受控云端验证 HRPlus 后台触发和收尾生命周期，不启动真实招聘浏览器。
package app

import (
	"context"
	"encoding/json"
	"goodhr5/local-agent-go/internal/cloudapi"
	"goodhr5/local-agent-go/internal/planmodel"
	"goodhr5/local-agent-go/internal/planoperations"
	"goodhr5/local-agent-go/internal/protectedsession"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// backgroundPlanFixture 用实际 M1 配置失败或取消提供可控退出点，所有请求仅访问隔离云端。
func backgroundPlanFixture(t *testing.T, holdPreparation bool, extraPlans ...planmodel.Plan) (*Server, protectedsession.Session, *atomic.Int32, *atomic.Int32, *atomic.Bool, <-chan struct{}) {
	return backgroundPlanFixtureConfigured(t, holdPreparation, nil, extraPlans...)
}

// backgroundPlanFixtureConfigured 可在受控任务准备回执之前移动测试时钟，不修改真实页面或业务库。
func backgroundPlanFixtureConfigured(t *testing.T, holdPreparation bool, afterPrepare func(), extraPlans ...planmodel.Plan) (*Server, protectedsession.Session, *atomic.Int32, *atomic.Int32, *atomic.Bool, <-chan struct{}) {
	t.Helper()
	s := newCapabilityTestServer(t)
	s.planExecutionWake = make(chan struct{}, 1)
	s.planNow = func() time.Time { return time.Date(2026, 10, 10, 1, 0, 0, 0, time.UTC) }
	raw, err := os.ReadFile(filepath.Join("..", "planmodel", "testdata", "permit.json"))
	if err != nil {
		t.Fatal(err)
	}
	var permit planmodel.Permit
	if err = json.Unmarshal(raw, &permit); err != nil {
		t.Fatal(err)
	}
	date := time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC)
	plan := planmodel.Plan{ID: permit.Run.PlanID, TenantID: "fixture-team", UserEmail: "fixture@example.com", MachineID: "machine-A", Version: 1, StateSequence: 1, ActivationID: permit.Run.ActivationID, State: "enabled", Config: permit.Run.Snapshot, CreatedAt: date, UpdatedAt: date}
	var mu sync.Mutex
	claims, releases := &atomic.Int32{}, &atomic.Int32{}
	stopped := &atomic.Bool{}
	preparing := make(chan struct{})
	var notified sync.Once
	var scope string
	cloud := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer fixture-plan-execution" {
			t.Error("后台使用了其他登录证明")
		}
		if r.URL.Path == "/api/subscription/status" {
			notified.Do(func() { close(preparing) })
			if holdPreparation {
				<-r.Context().Done()
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"subscription": map[string]any{"active": false}})
			return
		}
		mu.Lock()
		defer mu.Unlock()
		switch {
		case r.URL.Path == "/api/auth/me":
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "user": map[string]any{"email": plan.UserEmail, "tenant_id": plan.TenantID}})
		case r.URL.Path == "/api/execution-plans":
			copy := plan
			if stopped.Load() {
				copy.State = "stopped"
				copy.StopRequested = true
				copy.StateSequence++
			}
			allPlans := append([]planmodel.Plan{copy}, extraPlans...)
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "plans": allPlans})
		case r.URL.Path == "/api/execution-plan-runs/claim":
			var input cloudapi.PlanClaimRequest
			if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
				t.Error(err)
			}
			if operation, err := s.db.PlanOperation(t.Context(), scope, input.RequestID); err != nil || operation.Kind != "claim" {
				t.Error("领取前没有保存原请求", err)
			}
			claims.Add(1)
			permit.Run.ID, permit.Run.OwnerID = input.RunID, input.OwnerID
			permit.Owner.OwnerID = input.OwnerID
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "permit": permit})
		case strings.HasSuffix(r.URL.Path, "/prepare"):
			permit.Run.Sequence++
			permit.Run.Items[0].TaskRunID = "70000000-0000-0000-0000-000000000001"
			if afterPrepare != nil {
				afterPrepare()
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "permit": permit})
		case r.URL.Path == "/api/positions/same-job":
			_ = json.NewEncoder(w).Encode(map[string]any{"position": map[string]any{"id": "same-job", "name": "fixture", "platform_id": "boss"}})
		case strings.HasSuffix(r.URL.Path, "/release"):
			var input cloudapi.PlanRunUpdateRequest
			if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
				t.Error(err)
			}
			releases.Add(1)
			permit.Run.Sequence, permit.Run.State, permit.Run.CurrentItem = input.Sequence, input.State, input.CurrentItem
			for index, item := range input.Items {
				permit.Run.Items[index].State, permit.Run.Items[index].Actions = item.State, item.Actions
				if input.State != "waiting_window" && input.State != "completed" && (item.State == "pending" || item.State == "running") {
					permit.Run.Items[index].State = "stopped"
				}
				for action, p := range permit.Run.Items[index].Actions {
					if input.State != "waiting_window" && input.State != "completed" && p.State == "active" {
						p.State = "stopped"
						permit.Run.Items[index].Actions[action] = p
					}
				}
			}
			permit.Owner.State = "released"
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "permit": permit})
		default:
			t.Error("后台访问了未预期接口", r.URL.Path)
			w.WriteHeader(500)
		}
	}))
	t.Cleanup(cloud.Close)
	s.cfg.CloudAPIBase = cloud.URL
	scope = cloudapi.SessionOwnerScope(cloud.URL, plan.UserEmail)
	identity := protectedsession.Session{Token: "fixture-plan-execution", UserEmail: plan.UserEmail, TenantID: plan.TenantID, MachineID: plan.MachineID, CloudBase: cloud.URL}
	return s, identity, claims, releases, stopped, preparing
}

// TestBackgroundPreparationCrossesWindow 验证岗位准备回执跨过名义结束后，保存等待下午而不是错误停止当天计划。
func TestBackgroundPreparationCrossesWindow(t *testing.T) {
	clock := &atomic.Int64{}
	clock.Store(time.Date(2026, 10, 10, 1, 0, 0, 0, time.UTC).UnixNano())
	s, identity, _, _, _, _ := backgroundPlanFixtureConfigured(t, false, func() { clock.Store(time.Date(2026, 10, 10, 4, 0, 1, 0, time.UTC).UnixNano()) })
	s.planNow = func() time.Time { return time.Unix(0, clock.Load()).UTC() }
	if err := s.commitProtectedSession(t.Context(), 0, identity); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	if err := s.processPlanExecutions(ctx); err != nil {
		t.Fatal(err)
	}
	s.planExecutionMu.Lock()
	active := s.activePlanExecution
	s.planExecutionMu.Unlock()
	select {
	case <-active.done:
	case <-time.After(5 * time.Second):
		t.Fatal("跨窗口准备没有退出")
	}
	if err := s.processPlanExecutions(ctx); err == nil {
		t.Fatal("释放未确认就显示结算")
	}
	op, err := s.db.NextPlanUpdate(t.Context(), active.execution.Request.OwnerScope)
	if err != nil {
		t.Fatal(err)
	}
	original, err := planoperations.New(s.db).OriginalUpdate(t.Context(), active.execution.Request.OwnerScope, op.RequestID)
	if err != nil || original.State != "waiting_window" || original.CurrentItem != 0 || !active.execution.Held.Reservation.CleanupReleased() {
		t.Fatal("准备跨窗口被当成当天失败", err)
	}
}

// TestBackgroundPlanQueuesDuringActiveWork 验证长任务期间到点的另一计划保留原触发时间，不抢占页面。
func TestBackgroundPlanQueuesDuringActiveWork(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "planmodel", "testdata", "permit.json"))
	if err != nil {
		t.Fatal(err)
	}
	var permit planmodel.Permit
	if err := json.Unmarshal(raw, &permit); err != nil {
		t.Fatal(err)
	}
	permit.Run.Snapshot.Schedule.Windows = permit.Run.Snapshot.Schedule.Windows[:1]
	permit.Run.Snapshot.Schedule.Windows[0].StartMinute = 600
	date := time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC)
	extra := planmodel.Plan{ID: "10000000-0000-0000-0000-000000000099", TenantID: "fixture-team", UserEmail: "fixture@example.com", MachineID: "machine-A", Version: 1, StateSequence: 1, ActivationID: "30000000-0000-0000-0000-000000000099", State: "enabled", Config: permit.Run.Snapshot, CreatedAt: date, UpdatedAt: date}
	s, identity, claims, _, _, preparing := backgroundPlanFixture(t, true, extra)
	clock := &atomic.Int64{}
	clock.Store(time.Date(2026, 10, 10, 1, 0, 0, 0, time.UTC).UnixNano())
	s.planNow = func() time.Time { return time.Unix(0, clock.Load()).UTC() }
	if err := s.commitProtectedSession(t.Context(), 0, identity); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer func() { cancel(); s.shutdownPlanExecution(ctx) }()
	if err := s.processPlanExecutions(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case <-preparing:
	case <-time.After(5 * time.Second):
		t.Fatal("原计划没有进入准备")
	}
	clock.Store(time.Date(2026, 10, 10, 2, 0, 5, 0, time.UTC).UnixNano())
	if err := s.processPlanExecutions(ctx); err != nil {
		t.Fatal(err)
	}
	waiting, err := s.db.WaitingPlanRequests(t.Context(), cloudapi.SessionOwnerScope(identity.CloudBase, identity.UserEmail))
	want := time.Date(2026, 10, 10, 2, 0, 0, 0, time.UTC)
	if err != nil || len(waiting) != 1 || waiting[0].PlanID != extra.ID || !waiting[0].TriggeredAt.Equal(want) || claims.Load() != 1 {
		t.Fatal("长任务期间漏排队、重排时间或抢占原运行", err)
	}
}

// waitBackgroundCondition 仅等待本测试已启动的后台服务状态，不凭固定休眠判断任务完成。
func waitBackgroundCondition(t *testing.T, check func() bool) {
	t.Helper()
	deadline := time.After(10 * time.Second)
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		if check() {
			return
		}
		select {
		case <-deadline:
			t.Fatal("隔离后台服务未达到预期状态")
		case <-ticker.C:
		}
	}
}

// TestBackgroundPlanLifecycle 验证无登录不领取，网页无关的后台触发进入实际 M1，失败后原释放确认并完成排队。
func TestBackgroundPlanLifecycle(t *testing.T) {
	s, identity, claims, releases, _, _ := backgroundPlanFixture(t, false)
	if err := s.processPlanExecutions(t.Context()); err != nil || claims.Load() != 0 {
		t.Fatal("未登录后台领取任务", err)
	}
	if err := s.commitProtectedSession(t.Context(), 0, identity); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done, uploads := make(chan struct{}), make(chan struct{})
	go func() { defer close(done); s.runPlanExecutions(ctx) }()
	go func() { defer close(uploads); s.runPlanUploads(ctx) }()
	defer func() { cancel(); <-done; <-uploads }()
	waitBackgroundCondition(t, func() bool {
		if releases.Load() != 1 {
			return false
		}
		s.planExecutionMu.Lock()
		defer s.planExecutionMu.Unlock()
		return s.activePlanExecution == nil
	})
	if claims.Load() != 1 {
		t.Fatal("后台重复领取", claims.Load())
	}
	if waiting, err := s.db.WaitingPlanRequests(t.Context(), cloudapi.SessionOwnerScope(identity.CloudBase, identity.UserEmail)); err != nil || len(waiting) != 0 {
		t.Fatal("结算后仍排队", err)
	}
}

// TestBackgroundPlanRemoteStop 验证长任务准备期间仍检查云端停止，退出后保留 stopped 原释放。
func TestBackgroundPlanRemoteStop(t *testing.T) {
	s, identity, _, releases, stopped, preparing := backgroundPlanFixture(t, true)
	if err := s.commitProtectedSession(t.Context(), 0, identity); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	if err := s.processPlanExecutions(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case <-preparing:
	case <-time.After(5 * time.Second):
		t.Fatal("实际 M1 未进入准备")
	}
	stopped.Store(true)
	if err := s.processPlanExecutions(ctx); err != nil {
		t.Fatal(err)
	}
	s.planExecutionMu.Lock()
	active := s.activePlanExecution
	s.planExecutionMu.Unlock()
	select {
	case <-active.done:
	case <-time.After(5 * time.Second):
		t.Fatal("停止后旧任务未退出")
	}
	if err := s.processPlanExecutions(ctx); err == nil {
		t.Fatal("原释放未确认就报告已结算")
	}
	if !active.execution.Held.Reservation.CleanupReleased() {
		t.Fatal("停止后本地父引用未清理")
	}
	a := s.planAuthority(active.identity, active.version)
	if sent, err := planoperations.New(s.db).UploadNext(t.Context(), cloudapi.New(identity.CloudBase), a); err != nil || !sent {
		t.Fatal("停止原释放未确认", err)
	}
	if err := s.processPlanExecutions(ctx); err != nil {
		t.Fatal(err)
	}
	if releases.Load() != 1 {
		t.Fatal("停止释放重复或丢失")
	}
}

// TestBackgroundPlanShutdown 验证服务关闭取消旧任务，并在退出前持久保存原释放，不要求云端在线确认。
func TestBackgroundPlanShutdown(t *testing.T) {
	s, identity, _, _, _, preparing := backgroundPlanFixture(t, true)
	if err := s.commitProtectedSession(t.Context(), 0, identity); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	if err := s.processPlanExecutions(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case <-preparing:
	case <-time.After(5 * time.Second):
		t.Fatal("未开始准备")
	}
	cancel()
	s.shutdownPlanExecution(ctx)
	if operation, err := s.db.NextPlanUpdate(t.Context(), cloudapi.SessionOwnerScope(identity.CloudBase, identity.UserEmail)); err != nil || operation.Kind != "release" {
		t.Fatal("关闭丢失原释放", err)
	}
}
