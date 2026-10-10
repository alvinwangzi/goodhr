// 本文件验证 HRPlus 停止收尾、编辑顺序、重新启用和旧回执迟到的完整存储流程，不操作招聘页面。
package httpapi

import (
	"errors"
	"testing"
)

// TestPlanStopEditRestartStores 在内存与独立 PostgreSQL 中验证同日重新开始保持历史且从新顺序第一项执行。
func TestPlanStopEditRestartStores(t *testing.T) {
	for _, kind := range []string{"memory", "postgres"} {
		t.Run(kind, func(t *testing.T) {
			var store ExecutionPlanStore
			var positions PositionStore
			var tasks TaskRunStore
			email, _ := newExecutionPlanID()
			email += "@fixture.test"
			tenantID := ""
			if kind == "postgres" {
				db := planPostgresFixture(t)
				tenant, err := NewPostgresTenantStore(db).GetOrCreateTenant(email)
				if err != nil {
					t.Fatal(err)
				}
				tenantID = tenant.ID
				store, positions, tasks = NewPostgresExecutionPlanStore(db), NewPostgresPositionStore(db), NewPostgresTaskRunStore(db)
			} else {
				memory := NewMemoryExecutionPlanStore()
				memory.positions, memory.taskRuns = NewMemoryPositionStore(), NewMemoryTaskRunStore()
				store, positions, tasks = memory, memory.positions, memory.taskRuns
			}
			config := postgresPlanConfig()
			for index, name := range []string{"Java", "销售"} {
				position, err := positions.SavePosition(Position{UserEmail: email, Name: name, PlatformID: "boss"})
				if err != nil {
					t.Fatal(err)
				}
				config.Items[index].PositionID = position.ID
				config.Items[index].Actions = []string{"greeting"}
				config.Items[index].PrioritizeReply = false
			}
			plan, err := store.Save(t.Context(), ExecutionPlan{TenantID: tenantID, UserEmail: email, MachineID: "A", Config: config}, 0)
			if err != nil {
				t.Fatal(err)
			}
			plan, err = store.Intent(t.Context(), tenantID, email, plan.ID, planIntentFixture(t, "arm"))
			if err != nil {
				t.Fatal(err)
			}
			oldClaim := planRunClaimFixture(t, plan)
			old, err := store.ClaimRun(t.Context(), tenantID, email, oldClaim)
			if err != nil {
				t.Fatal(err)
			}
			old, err = store.PrepareItemTask(t.Context(), tenantID, email, itemTaskRequestFixture(t, oldClaim, old.Run.Items[0].ID))
			if err != nil {
				t.Fatal(err)
			}
			oldTask := old.Run.Items[0].TaskRunID
			status := planRunUpdateFixture(t, oldClaim, old.Run.Sequence+1, "status", "running")
			status.Items = completePlanItemsFixture(old.Run, 0)
			status.Items[0].State = "running"
			status.Items[0].Actions["greeting"] = ExecutionPlanActionProgress{State: "active", Count: 1}
			old, err = store.UpdateRun(t.Context(), tenantID, email, status)
			if err != nil {
				t.Fatal(err)
			}
			stop := planIntentFixture(t, "stop")
			stop.ActivationID = plan.ActivationID
			plan, err = store.Intent(t.Context(), tenantID, email, plan.ID, stop)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = store.Save(t.Context(), plan, plan.Version); !errors.Is(err, ErrExecutionPlanBusy) {
				t.Fatal("执行未收尾就能编辑", err)
			}
			confirmation := planStopConfirmationFixture(t, plan)
			if _, err = store.ConfirmStopped(t.Context(), tenantID, email, plan.ID, confirmation); !errors.Is(err, ErrExecutionPlanBusy) {
				t.Fatal("未释放就确认停止", err)
			}
			release := planRunUpdateFixture(t, oldClaim, old.Run.Sequence+1, "release", "stopped")
			old, err = store.UpdateRun(t.Context(), tenantID, email, release)
			if err != nil {
				t.Fatal(err)
			}
			plan, err = store.ConfirmStopped(t.Context(), tenantID, email, plan.ID, confirmation)
			if err != nil {
				t.Fatal(err)
			}
			previousActivation := plan.ActivationID
			plan.Config.Items[0], plan.Config.Items[1] = plan.Config.Items[1], plan.Config.Items[0]
			for index := range plan.Config.Items {
				plan.Config.Items[index].Order = index
			}
			plan, err = store.Save(t.Context(), plan, plan.Version)
			if err != nil {
				t.Fatal(err)
			}
			arm := planIntentFixture(t, "arm")
			arm.ExpectedVersion = plan.Version
			plan, err = store.Intent(t.Context(), tenantID, email, plan.ID, arm)
			if err != nil || plan.ActivationID == previousActivation {
				t.Fatal("重新启用未创建新批次", err)
			}
			claim := planRunClaimFixture(t, plan)
			next, err := store.ClaimRun(t.Context(), tenantID, email, claim)
			if err != nil {
				t.Fatal(err)
			}
			if next.Run.ID == old.Run.ID || next.Run.CurrentItem != 0 || next.Run.Items[0].Snapshot.PositionID != config.Items[1].PositionID || next.Run.Items[0].Actions["greeting"].Count != 0 || next.Run.Items[0].TaskRunID != "" {
				t.Fatal("新运行没有从编辑后的第一项开始", next.Run)
			}
			next, err = store.PrepareItemTask(t.Context(), tenantID, email, itemTaskRequestFixture(t, claim, next.Run.Items[0].ID))
			if err != nil || next.Run.Items[0].TaskRunID == oldTask {
				t.Fatal("新项覆盖旧任务", err)
			}
			if _, err = store.UpdateRun(t.Context(), tenantID, email, release); err != nil {
				t.Fatal("旧释放重放失去幂等性", err)
			}
			if _, err = store.ConfirmStopped(t.Context(), tenantID, email, plan.ID, confirmation); err != nil {
				t.Fatal(err)
			}
			late := status
			late.RequestID, _ = newExecutionPlanID()
			late.Sequence = old.Run.Sequence + 10
			if _, err = store.UpdateRun(t.Context(), tenantID, email, late); err == nil {
				t.Fatal("旧占用者新请求修改终态")
			}
			current, err := store.GetRun(t.Context(), tenantID, email, next.Run.ID)
			if err != nil || current.Sequence != next.Run.Sequence || current.OwnerID != claim.OwnerID || current.State != "starting" {
				t.Fatal("旧回执改变新运行", err, current)
			}
			history, err := store.GetRun(t.Context(), tenantID, email, old.Run.ID)
			if err != nil || history.State != "stopped" || history.Items[0].TaskRunID != oldTask || history.Items[0].Actions["greeting"].Count != 1 || history.Snapshot.Items[0].PositionID != config.Items[0].PositionID {
				t.Fatal("编辑覆盖旧顺序或历史", err, history)
			}
			task, err := tasks.TaskRunByID(tenantID, oldTask)
			if err != nil || task.Status != "stopped" || task.FinishedAt == nil {
				t.Fatal("旧任务没有停止结算", err, task)
			}
			// 核对新凭证仍能使用并安全释放，不遗留测试账号占用。
			end := planRunUpdateFixture(t, claim, next.Run.Sequence+1, "release", "stopped")
			if _, err = store.UpdateRun(t.Context(), tenantID, email, end); err != nil {
				t.Fatal("旧回执清除了新占用", err)
			}
		})
	}
}
