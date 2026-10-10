// 本文件验证 HRPlus 原排队时间幂等、等待时长和隔离数据库事实，不领取招聘执行权。
package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"github.com/lib/pq"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

// TestPlanWaitNotifications 验证首次排队及手动占用提交通知原作用域，幂等排队不重复写事件。
func TestPlanWaitNotifications(t *testing.T) {
	db := planPostgresFixture(t)
	store := NewPostgresExecutionPlanStore(db)
	unique, _ := newExecutionPlanID()
	plan := createArmedPlanFixture(t, store, unique+"@example.com")
	listener := pq.NewListener(os.Getenv("GOODHR_EXECUTION_PLAN_TEST_PG_DSN"), time.Second, time.Second, nil)
	defer listener.Close()
	if err := listener.Listen("hrplus_plan_changes"); err != nil {
		t.Fatal(err)
	}
	waitEvent := func(expected bool) {
		deadline := time.NewTimer(200 * time.Millisecond)
		defer deadline.Stop()
		for {
			select {
			case event := <-listener.Notify:
				if event != nil && event.Extra == planEventScope("", plan.UserEmail) {
					if !expected {
						t.Fatal("幂等重试仍发变更通知")
					}
					return
				}
			case <-deadline.C:
				if expected {
					t.Fatal("提交后缺少原作用域变更通知")
				}
				return
			}
		}
	}
	loc, _ := time.LoadLocation(plan.Config.Schedule.Timezone)
	window := plan.Config.Schedule.Windows[0]
	request, _ := newExecutionPlanID()
	at := time.Date(2026, 10, 10, window.StartMinute/60, window.StartMinute%60, 0, 0, loc)
	fact := ExecutionPlanWait{PlanID: plan.ID, RequestID: request, ActivationID: plan.ActivationID, ConfigVersion: plan.Version, MachineID: plan.MachineID, TriggeredAt: at}
	if _, err := store.RecordWait(t.Context(), "", plan.UserEmail, fact); err != nil {
		t.Fatal(err)
	}
	waitEvent(true)
	if _, err := store.RecordWait(t.Context(), "", plan.UserEmail, fact); err != nil {
		t.Fatal(err)
	}
	waitEvent(false)
	owner, _ := newExecutionPlanID()
	claimID, _ := newExecutionPlanID()
	claim := AccountExecutionClaim{UserEmail: plan.UserEmail, OwnerID: owner, RequestID: claimID, OwnerType: "manual", MachineID: "B", Credential: strings.Repeat("fixture-only-", 4), LocalReserved: true}
	accounts := NewPostgresAccountExecutionStore(db)
	if _, err := accounts.Claim(t.Context(), claim); err != nil {
		t.Fatal(err)
	}
	waitEvent(true)
	claim.RequestID, _ = newExecutionPlanID()
	if err := accounts.Release(t.Context(), claim, true); err != nil {
		t.Fatal(err)
	}
	waitEvent(true)
}

// TestPlanWaitAPI 验证原绑定电脑、登录和路由身份，原排队回执不创建运行。
func TestPlanWaitAPI(t *testing.T) {
	server := mustNewServer(t)
	routes := server.Routes()
	email := "wait-api@example.com"
	token := loginForTest(t, routes, email)
	bindPositionDeviceForTest(t, routes, token)
	tenant := ""
	if tenants := server.auth.tenantStore; tenants != nil {
		value, err := tenants.GetOrCreateTenant(email)
		if err != nil {
			t.Fatal(err)
		}
		tenant = value.ID
	}
	plan, err := server.executionPlans.store.Save(t.Context(), ExecutionPlan{TenantID: tenant, UserEmail: email, MachineID: positionTestMachineID, Config: postgresPlanConfig()}, 0)
	if err != nil {
		t.Fatal(err)
	}
	plan, err = server.executionPlans.store.Intent(t.Context(), tenant, email, plan.ID, planIntentFixture(t, "arm"))
	if err != nil {
		t.Fatal(err)
	}
	loc, _ := time.LoadLocation(plan.Config.Schedule.Timezone)
	window := plan.Config.Schedule.Windows[0]
	request, _ := newExecutionPlanID()
	input := ExecutionPlanWait{PlanID: plan.ID, RequestID: request, ActivationID: plan.ActivationID, ConfigVersion: plan.Version, MachineID: plan.MachineID, TriggeredAt: time.Date(2026, 10, 10, window.StartMinute/60, window.StartMinute%60, 0, 0, loc)}
	post := func(value ExecutionPlanWait, credential string) *httptest.ResponseRecorder {
		raw, _ := json.Marshal(value)
		req := httptest.NewRequest(http.MethodPost, "/api/execution-plans/"+plan.ID+"/wait", bytes.NewReader(raw))
		if credential != "" {
			req.Header.Set("Authorization", "Bearer "+credential)
		}
		res := httptest.NewRecorder()
		routes.ServeHTTP(res, req)
		return res
	}
	if res := post(input, ""); res.Code != 401 {
		t.Fatal("排队无需登录", res.Code)
	}
	wrong := input
	wrong.MachineID = "unbound-B"
	if res := post(wrong, token); res.Code != 403 {
		t.Fatal("非绑定电脑记录排队", res.Code)
	}
	if res := post(input, token); res.Code != 200 {
		t.Fatal("原排队未确认", res.Code, res.Body.String())
	}
	runs, err := server.executionPlans.store.ListRuns(t.Context(), tenant, email, plan.ID)
	if err != nil || len(runs) != 0 {
		t.Fatal("排队接口开始了任务", err)
	}
}

// TestPlanWaitStores 对内存和真实 PostgreSQL 验证原窗口事实及修改、越权、停止后的拒绝。
func TestPlanWaitStores(t *testing.T) {
	for _, kind := range []string{"memory", "postgres"} {
		t.Run(kind, func(t *testing.T) {
			var store ExecutionPlanStore = NewMemoryExecutionPlanStore()
			if kind == "postgres" {
				store = NewPostgresExecutionPlanStore(planPostgresFixture(t))
			}
			id, _ := newExecutionPlanID()
			plan := createArmedPlanFixture(t, store, id+"@example.com")
			loc, _ := time.LoadLocation(plan.Config.Schedule.Timezone)
			window := plan.Config.Schedule.Windows[0]
			at := time.Date(2026, 10, 10, window.StartMinute/60, window.StartMinute%60, 0, 0, loc)
			queued := at.Add(30 * time.Second)
			request, _ := newExecutionPlanID()
			input := ExecutionPlanWait{PlanID: plan.ID, RequestID: request, ActivationID: plan.ActivationID, ConfigVersion: plan.Version, MachineID: plan.MachineID, TriggeredAt: at, QueuedAt: &queued}
			saved, err := store.RecordWait(t.Context(), "", plan.UserEmail, input)
			if err != nil || !samePlanWait(saved, input) {
				t.Fatal("原排队不能保存", err)
			}
			if _, err := store.RecordWait(t.Context(), "", plan.UserEmail, input); err != nil {
				t.Fatal("原编号不能重试", err)
			}
			changed := input
			changed.TriggeredAt = at.Add(time.Hour)
			if _, err := store.RecordWait(t.Context(), "", plan.UserEmail, changed); !errors.Is(err, ErrExecutionPlanRequest) {
				t.Fatal("原触发被替换", err)
			}
			changed = input
			another := queued.Add(time.Second)
			changed.QueuedAt = &another
			if _, err := store.RecordWait(t.Context(), "", plan.UserEmail, changed); !errors.Is(err, ErrExecutionPlanRequest) {
				t.Fatal("实际入队时刻被替换", err)
			}
			if _, err := store.RecordWait(t.Context(), "other-team", plan.UserEmail, input); !errors.Is(err, ErrNotFound) {
				t.Fatal("其他团队保存排队", err)
			}
			snapshot, err := store.RuntimeSnapshot(t.Context(), "", plan.UserEmail, plan.ID)
			if err != nil || len(snapshot.Runs) != 0 || len(snapshot.Waits) != 1 {
				t.Fatal("登记排队创建运行或丢失原事实", err)
			}
			view, err := buildPlanRuntimeView(snapshot, queued.Add(time.Minute))
			if err != nil || view.Waiting == nil || view.WaitSeconds == nil || *view.WaitSeconds != 60 || view.WaitReason != "queued" {
				t.Fatal("等待时长使用上报时刻", view, err)
			}
			stop := planIntentFixture(t, "stop")
			stop.ActivationID = plan.ActivationID
			if _, err := store.Intent(t.Context(), "", plan.UserEmail, plan.ID, stop); err != nil {
				t.Fatal(err)
			}
			if _, err := store.RecordWait(t.Context(), "", plan.UserEmail, input); !errors.Is(err, ErrExecutionPlanRequest) {
				t.Fatal("停止后继续登记排队", err)
			}
		})
	}
}

// TestLegacyWaitTimeUnknown 验证历史未知入队时刻不补造等待时长。
func TestLegacyWaitTimeUnknown(t *testing.T) {
	snapshot := runtimeViewFixture(t)
	at := time.Date(2026, 10, 10, 1, 0, 0, 0, time.UTC)
	snapshot.Waits = []ExecutionPlanWait{{PlanID: snapshot.Plan.ID, ActivationID: snapshot.Plan.ActivationID, ConfigVersion: snapshot.Plan.Version, TriggeredAt: at}}
	view, err := buildPlanRuntimeView(snapshot, at.Add(time.Minute))
	if err != nil || view.Waiting == nil || view.WaitSeconds != nil {
		t.Fatal("旧记录虚构入队时间", view, err)
	}
	view, err = buildPlanRuntimeView(snapshot, at.Add(12*time.Hour))
	if err != nil || view.WaitReason != "queue_day_missed" {
		t.Fatal("日末过期排队仍承诺当日开始", view, err)
	}
}
