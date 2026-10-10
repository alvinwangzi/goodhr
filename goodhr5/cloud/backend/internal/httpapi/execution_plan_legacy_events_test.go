// 本文件验证 HRPlus 旧手动运行引起计划占用变化和作用域通知，不把普通读取或统计心跳当成开始。
package httpapi

import (
	"github.com/lib/pq"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"
)

// TestLegacyManualPlanNotification 使用真实旧岗位状态存储和通知中间件，只有身份变化通知原账号。
func TestLegacyManualPlanNotification(t *testing.T) {
	server := mustNewServer(t)
	routes := server.Routes()
	email := "legacy-plan-event@example.com"
	token := loginForTest(t, routes, email)
	tenant, err := server.auth.tenantStore.GetOrCreateTenant(email)
	if err != nil {
		t.Fatal(err)
	}
	plans := server.executionPlans.store.(*MemoryExecutionPlanStore)
	position, err := plans.positions.SavePosition(Position{UserEmail: email, Name: "旧手动岗位", PlatformID: "boss"})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := plans.Save(t.Context(), ExecutionPlan{TenantID: tenant.ID, UserEmail: email, MachineID: "A", Config: postgresPlanConfig()}, 0)
	if err != nil {
		t.Fatal(err)
	}
	changes, close, ok := server.executionPlans.events.subscribe(planEventScope(tenant.ID, email))
	if !ok {
		t.Fatal("无法订阅")
	}
	defer close()
	other, closeOther, _ := server.executionPlans.events.subscribe(planEventScope(tenant.ID, "other@example.com"))
	defer closeOther()
	call := func(action string) {
		handler := server.executionPlans.notifyLegacyPositionMutation(func(w http.ResponseWriter, r *http.Request) {
			if action == "start" {
				if err := plans.positions.ClaimPositionStart(email, position.ID); err != nil {
					t.Error(err)
					w.WriteHeader(409)
					return
				}
				_, err := plans.taskRuns.CreateTaskRun(TaskRun{TenantID: tenant.ID, UserEmail: email, PositionID: position.ID, MachineID: "B", TaskType: "greeting"})
				if err != nil {
					t.Error(err)
				}
			}
			if action == "finish" {
				if err := plans.positions.UpdatePositionStatus(position.ID, "completed"); err != nil {
					t.Error(err)
				}
				active, err := plans.taskRuns.ActiveTaskRunByPosition(position.ID)
				if err == nil {
					_ = plans.taskRuns.FinishTaskRun(active.ID, "completed", "", 0, 0)
				}
			}
			w.WriteHeader(200)
		})
		req := httptest.NewRequest(http.MethodPost, "/api/positions/"+position.ID+"/status", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		response := httptest.NewRecorder()
		handler(response, req)
		if response.Code != 200 {
			t.Fatal("原状态修改失败", response.Code)
		}
	}
	call("start")
	select {
	case <-changes:
	default:
		t.Fatal("手动开始没有通知计划页")
	}
	snapshot, err := plans.RuntimeSnapshot(t.Context(), tenant.ID, email, plan.ID)
	if err != nil || !snapshot.LegacyBusy || snapshot.AccountOwner == nil || snapshot.AccountOwner.MachineID != "B" || snapshot.AccountOwner.OwnerType != "manual" {
		t.Fatal("旧手动占用未显示原电脑", snapshot, err)
	}
	call("heartbeat")
	select {
	case <-changes:
		t.Fatal("同状态心跳反复通知")
	default:
	}
	call("finish")
	select {
	case <-changes:
	default:
		t.Fatal("手动完成未通知释放")
	}
	snapshot, err = plans.RuntimeSnapshot(t.Context(), tenant.ID, email, plan.ID)
	if err != nil || snapshot.LegacyBusy || snapshot.AccountOwner != nil {
		t.Fatal("旧手动结束仍显示占用", snapshot, err)
	}
	select {
	case <-other:
		t.Fatal("旧手动通知跨账号")
	default:
	}
}

// TestLegacyPostgresPlanNotification 验证数据库旧岗位开始/结束提交发事件，同状态心跳不发事件。
func TestLegacyPostgresPlanNotification(t *testing.T) {
	db := planPostgresFixture(t)
	plans := NewPostgresExecutionPlanStore(db)
	id, _ := newExecutionPlanID()
	plan := createArmedPlanFixture(t, plans, id+"@example.com")
	positions := NewPostgresPositionStore(db)
	position, err := positions.SavePosition(Position{UserEmail: plan.UserEmail, Name: "旧岗位事件夹具", PlatformID: "boss"})
	if err != nil {
		t.Fatal(err)
	}
	listener := pq.NewListener(os.Getenv("GOODHR_EXECUTION_PLAN_TEST_PG_DSN"), time.Second, time.Second, nil)
	defer listener.Close()
	if err := listener.Listen("hrplus_plan_changes"); err != nil {
		t.Fatal(err)
	}
	wait := func(expected bool) {
		duration := 200 * time.Millisecond
		if expected {
			duration = 3 * time.Second
		}
		timer := time.NewTimer(duration)
		defer timer.Stop()
		for {
			select {
			case event := <-listener.Notify:
				if event != nil && event.Extra == planEventScope("", plan.UserEmail) {
					if !expected {
						t.Fatal("旧手动同状态仍发送通知")
					}
					return
				}
			case <-timer.C:
				if expected {
					t.Fatal("旧手动提交未通知")
				}
				return
			}
		}
	}
	if err := positions.ClaimPositionStart(plan.UserEmail, position.ID); err != nil {
		t.Fatal(err)
	}
	wait(true)
	snapshot, err := plans.RuntimeSnapshot(t.Context(), "", plan.UserEmail, plan.ID)
	if err != nil || !snapshot.LegacyBusy {
		t.Fatal("真实旧手动占用被漏掉", err)
	}
	if err := positions.UpdatePositionStatus(position.ID, "running"); err != nil {
		t.Fatal(err)
	}
	wait(false)
	if err := positions.UpdatePositionStatus(position.ID, "completed"); err != nil {
		t.Fatal(err)
	}
	wait(true)
	snapshot, err = plans.RuntimeSnapshot(t.Context(), "", plan.UserEmail, plan.ID)
	if err != nil || snapshot.LegacyBusy {
		t.Fatal("真实旧手动结束仍占用", err)
	}
}
