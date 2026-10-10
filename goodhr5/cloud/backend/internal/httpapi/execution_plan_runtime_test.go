// 本文件验证 HRPlus 当前计划只读投影的日期、跨电脑占用和原配置边界，不启动招聘任务。
package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// TestPlanRuntimeAPI 验证实际路由认证与只读方法，读取不能变更计划状态或创建运行。
func TestPlanRuntimeAPI(t *testing.T) {
	server := mustNewServer(t)
	routes := server.Routes()
	email := "runtime-api@example.com"
	token := loginForTest(t, routes, email)
	tenant := ""
	if store := server.positions.auth.tenantStore; store != nil {
		value, err := store.GetOrCreateTenant(email)
		if err != nil {
			t.Fatal(err)
		}
		tenant = value.ID
	}
	plan, err := server.executionPlans.store.Save(t.Context(), ExecutionPlan{TenantID: tenant, UserEmail: email, MachineID: "A", Config: postgresPlanConfig()}, 0)
	if err != nil {
		t.Fatal(err)
	}
	read := func(method, credential string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, "/api/execution-plans/"+plan.ID+"/runtime", nil)
		if credential != "" {
			req.Header.Set("Authorization", "Bearer "+credential)
		}
		res := httptest.NewRecorder()
		routes.ServeHTTP(res, req)
		return res
	}
	res := read(http.MethodGet, token)
	var payload struct {
		Runtime ExecutionPlanRuntimeView `json:"runtime"`
	}
	if res.Code != 200 || json.Unmarshal(res.Body.Bytes(), &payload) != nil || payload.Runtime.Plan.ID != plan.ID || payload.Runtime.WaitReason != "stopped" || len(payload.Runtime.Runs) != 0 {
		t.Fatal("当前状态接口未只读原计划", res.Code, res.Body.String())
	}
	if res := read(http.MethodPost, token); res.Code != 405 {
		t.Fatal("状态读取接受写命令", res.Code)
	}
	if res := read(http.MethodGet, ""); res.Code != 401 {
		t.Fatal("状态读取无需登录", res.Code)
	}
	other := loginForTest(t, routes, "runtime-other@example.com")
	if res := read(http.MethodGet, other); res.Code != 404 {
		t.Fatal("状态读取泄露另一账号", res.Code)
	}
	again, err := server.executionPlans.store.Get(t.Context(), tenant, email, plan.ID)
	if err != nil || again.StateSequence != plan.StateSequence || again.State != plan.State {
		t.Fatal("读取改变执行状态", err)
	}
}

// TestPlanRuntimeStores 对内存和隔离 PostgreSQL 核对一致快照及跨账号、跨团队读取隔离。
func TestPlanRuntimeStores(t *testing.T) {
	for _, kind := range []string{"memory", "postgres"} {
		t.Run(kind, func(t *testing.T) {
			var store ExecutionPlanStore = NewMemoryExecutionPlanStore()
			if kind == "postgres" {
				store = NewPostgresExecutionPlanStore(planPostgresFixture(t))
			}
			unique, _ := newExecutionPlanID()
			plan := createArmedPlanFixture(t, store, unique+"@example.com")
			permit, err := store.ClaimRun(t.Context(), "", plan.UserEmail, planRunClaimFixture(t, plan))
			if err != nil {
				t.Fatal(err)
			}
			view, err := store.RuntimeSnapshot(t.Context(), "", plan.UserEmail, plan.ID)
			if err != nil || view.Plan.Version != plan.Version || len(view.Runs) != 1 || view.Runs[0].ID != permit.Run.ID || view.AccountOwner == nil || view.AccountOwner.OwnerID != permit.Owner.OwnerID {
				t.Fatal("当前运行和占用未一致读取", view, err)
			}
			view.Runs[0].Snapshot.Items[0].Actions[0] = "changed"
			again, err := store.RuntimeSnapshot(t.Context(), "", plan.UserEmail, plan.ID)
			if err != nil || again.Runs[0].Sequence != permit.Run.Sequence || again.Runs[0].Snapshot.Items[0].Actions[0] == "changed" {
				t.Fatal("只读投影修改原运行", err)
			}
			for _, scope := range [][2]string{{"", "other@example.com"}, {"other-team", plan.UserEmail}} {
				if _, err := store.RuntimeSnapshot(t.Context(), scope[0], scope[1], plan.ID); !errors.Is(err, ErrNotFound) {
					t.Fatal("当前运行跨账号或团队读取", err)
				}
			}
		})
	}
}

// runtimeViewFixture 保留重复岗位编排，使用明确的上海工作时间测试名义安排。
func runtimeViewFixture(t *testing.T) ExecutionPlanRuntimeSnapshot {
	t.Helper()
	store := NewMemoryExecutionPlanStore()
	plan := createArmedPlanFixture(t, store, "runtime@example.com")
	plan.Config.Schedule = ExecutionPlanSchedule{Cycle: "daily", Timezone: "Asia/Shanghai", Windows: []ExecutionPlanWindow{{Order: 0, StartMinute: 540, EndMinute: 720}, {Order: 1, StartMinute: 810, EndMinute: 1200}}}
	return ExecutionPlanRuntimeSnapshot{Plan: plan, Runs: []ExecutionPlanRun{}}
}

// TestPlanRuntimeNominalWindows 验证当前窗口不当成已开始，午休、远期生效和到期不虚构安排。
func TestPlanRuntimeNominalWindows(t *testing.T) {
	for _, test := range []struct{ clock, want, reason string }{{"2026-10-10T00:00:00Z", "2026-10-10T09:00:00+08:00", "waiting_time"}, {"2026-10-10T01:00:00Z", "2026-10-10T09:00:00+08:00", "waiting_start"}, {"2026-10-10T04:00:00Z", "2026-10-10T13:30:00+08:00", "waiting_time"}, {"2026-10-10T12:00:00Z", "2026-10-11T09:00:00+08:00", "waiting_time"}} {
		snapshot := runtimeViewFixture(t)
		now, _ := time.Parse(time.RFC3339, test.clock)
		view, err := buildPlanRuntimeView(snapshot, now)
		if err != nil || view.NominalAt == nil || view.NominalAt.Format(time.RFC3339) != test.want || view.WaitReason != test.reason {
			t.Fatal(test, view, err)
		}
	}
	snapshot := runtimeViewFixture(t)
	snapshot.Plan.Config.Schedule.StartDate = "2030-01-01"
	now, _ := time.Parse(time.RFC3339, "2026-10-10T01:00:00Z")
	view, err := buildPlanRuntimeView(snapshot, now)
	if err != nil || view.NominalAt == nil || view.NominalAt.Format("2006-01-02") != "2030-01-01" {
		t.Fatal("远期日期被截断", view, err)
	}
	snapshot.Plan.Config.Schedule.EndDate = "2029-12-31"
	snapshot.Plan.Config.Schedule.StartDate = ""
	now, _ = time.Parse(time.RFC3339, "2030-01-01T01:00:00Z")
	view, err = buildPlanRuntimeView(snapshot, now)
	if err != nil || view.NominalAt != nil || view.WaitReason != "no_future_window" {
		t.Fatal("过期配置仍安排", view, err)
	}
}

// TestPlanRuntimeCompletedAndBusy 验证当天完成从次日第一项开始，一次性无次日，另一电脑占用只显示等待。
func TestPlanRuntimeCompletedAndBusy(t *testing.T) {
	snapshot := runtimeViewFixture(t)
	now, _ := time.Parse(time.RFC3339, "2026-10-10T02:00:00Z")
	snapshot.Runs = []ExecutionPlanRun{{ID: "original-run", ActivationID: snapshot.Plan.ActivationID, ConfigVersion: snapshot.Plan.Version, ExecutionDate: "2026-10-10", State: "completed"}}
	view, err := buildPlanRuntimeView(snapshot, now)
	if err != nil || view.NominalAt == nil || view.NominalAt.Format("2006-01-02") != "2026-10-11" {
		t.Fatal("当天完成还安排下午", view, err)
	}
	snapshot.Plan.Config.Schedule.Cycle = "once"
	snapshot.Plan.Config.Schedule.OnceDate = "2026-10-10"
	view, err = buildPlanRuntimeView(snapshot, now)
	if err != nil || view.NominalAt != nil || view.WaitReason != "no_future_window" {
		t.Fatal("一次性完成虚构后续", view, err)
	}
	snapshot = runtimeViewFixture(t)
	snapshot.AccountOwner = &AccountExecutionOwner{OwnerID: "other-task", OwnerType: "manual", MachineID: "B", State: "draining"}
	view, err = buildPlanRuntimeView(snapshot, now)
	if err != nil || view.WaitReason != "account_busy" || view.CurrentRun != nil {
		t.Fatal("另一电脑占用误报执行", view, err)
	}
	snapshot.Runs = []ExecutionPlanRun{{ID: "old-active", ActivationID: snapshot.Plan.ActivationID, ConfigVersion: snapshot.Plan.Version, ExecutionDate: "2026-10-09", State: "running"}}
	view, err = buildPlanRuntimeView(snapshot, now)
	if err != nil || view.WaitReason != "recovery_required" || view.CurrentRun == nil || view.NominalAt != nil {
		t.Fatal("旧占用误显示新日已执行", view, err)
	}
	snapshot.Plan.StopRequested = true
	view, err = buildPlanRuntimeView(snapshot, now)
	if err != nil || view.WaitReason != "stopping" || view.CurrentRun == nil || view.NominalAt != nil {
		t.Fatal("停止收尾丢失原运行或继续安排", view, err)
	}
	snapshot = runtimeViewFixture(t)
	snapshot.Plan.Config.Schedule.Cycle = "weekly"
	snapshot.Plan.Config.Schedule.Weekdays = []int{1}
	view, err = buildPlanRuntimeView(snapshot, now)
	if err != nil || view.NominalAt == nil || view.NominalAt.Format("2006-01-02") != "2026-10-12" {
		t.Fatal("每周配置安排到错误日期", view, err)
	}
}
