// 本文件验证 HRPlus 计划窗口释放与迟到停止确认，不以旧岗位运行状态阻塞或覆盖后来任务。
package httpapi

import (
	"errors"
	"strings"
	"testing"
)

// testPlanItemCleanup 验证窗口休息释放旧式岗位占用，原任务停止时不覆盖后来手动任务。
func testPlanItemCleanup(t *testing.T, s ExecutionPlanStore, tasks TaskRunStore, positions PositionStore, p ExecutionPlan, beforeConfirm func(ExecutionPlan, string, ExecutionPlanStopConfirmation)) {
	t.Helper()
	claim := planRunClaimFixture(t, p)
	permit, err := s.ClaimRun(t.Context(), p.TenantID, p.UserEmail, claim)
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := s.PrepareItemTask(t.Context(), p.TenantID, p.UserEmail, itemTaskRequestFixture(t, claim, permit.Run.Items[0].ID))
	if err != nil {
		t.Fatal(err)
	}
	status := planRunUpdateFixture(t, claim, prepared.Run.Sequence+1, "status", "running")
	status.Items = completePlanItemsFixture(prepared.Run, 0)
	status.Items[0].State = "running"
	for action, progress := range status.Items[0].Actions {
		progress.State = "active"
		progress.Count = 1
		status.Items[0].Actions[action] = progress
	}
	running, err := s.UpdateRun(t.Context(), p.TenantID, p.UserEmail, status)
	if err != nil {
		t.Fatal(err)
	}
	release := planRunUpdateFixture(t, claim, running.Run.Sequence+1, "release", "waiting_window")
	if _, err = s.UpdateRun(t.Context(), p.TenantID, p.UserEmail, release); err != nil {
		t.Fatal(err)
	}
	oldTaskID := prepared.Run.Items[0].TaskRunID
	old, err := tasks.TaskRunByID(p.TenantID, oldTaskID)
	if err != nil || old.Status != "waiting_window" || old.FinishedAt != nil {
		t.Fatal("休息中的原任务仍显示运行或提前结束", old.Status, err)
	}
	position, err := positions.PositionByID(p.TenantID, p.UserEmail, p.Config.Items[0].PositionID, false)
	if err != nil || position.Status != "waiting_window" {
		t.Fatal("窗口释放仍保留岗位运行标志", position.Status, err)
	}
	if _, err = tasks.ActiveTaskRunByPosition(position.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("休息期间仍提供旧任务作为活动归属", err)
	}
	resume := claim
	resume.RequestID, _ = newExecutionPlanID()
	resume.OwnerID, _ = newExecutionPlanID()
	resumed, err := s.ClaimRun(t.Context(), p.TenantID, p.UserEmail, resume)
	if err != nil || resumed.Run.Items[0].TaskRunID != oldTaskID || resumed.Run.Items[0].Actions[p.Config.Items[0].Actions[0]].Count != 1 {
		t.Fatal("下一窗口没有保留原任务和数量", err)
	}
	resumeStatus := planRunUpdateFixture(t, resume, resumed.Run.Sequence+1, "status", "running")
	resumed, err = s.UpdateRun(t.Context(), p.TenantID, p.UserEmail, resumeStatus)
	if err != nil {
		t.Fatal(err)
	}
	continued, err := tasks.TaskRunByID(p.TenantID, oldTaskID)
	if err != nil || continued.Status != "running" || !samePlanTaskStart(continued.StartedAt, old.StartedAt) {
		t.Fatal("恢复任务重置实际首次开始时间", err)
	}
	secondRelease := planRunUpdateFixture(t, resume, resumed.Run.Sequence+1, "release", "waiting_window")
	if _, err = s.UpdateRun(t.Context(), p.TenantID, p.UserEmail, secondRelease); err != nil {
		t.Fatal(err)
	}
	if err = positions.ClaimPositionStart(p.UserEmail, position.ID); err != nil {
		t.Fatal("休息中的计划挡住新手动任务", err)
	}
	manual, err := tasks.CreateTaskRun(TaskRun{TenantID: p.TenantID, UserEmail: p.UserEmail, PositionID: position.ID, PlatformID: "boss", MachineID: "B", Status: "running"})
	if err != nil {
		t.Fatal(err)
	}
	stop := planIntentFixture(t, "stop")
	stop.ActivationID = p.ActivationID
	p, err = s.Intent(t.Context(), p.TenantID, p.UserEmail, p.ID, stop)
	if err != nil {
		t.Fatal(err)
	}
	confirm := planStopConfirmationFixture(t, p)
	if beforeConfirm != nil {
		beforeConfirm(p, oldTaskID, confirm)
	}
	if _, err = s.ConfirmStopped(t.Context(), p.TenantID, p.UserEmail, p.ID, confirm); err != nil {
		t.Fatal(err)
	}
	old, err = tasks.TaskRunByID(p.TenantID, oldTaskID)
	if err != nil || old.Status != "stopped" || old.FinishedAt == nil {
		t.Fatal("停止确认没有结束关联任务", old.Status, err)
	}
	newTask, err := tasks.TaskRunByID(p.TenantID, manual.ID)
	if err != nil || newTask.Status != "running" || newTask.FinishedAt != nil {
		t.Fatal("旧确认覆盖新任务", err)
	}
	position, err = positions.PositionByID(p.TenantID, p.UserEmail, position.ID, false)
	if err != nil || position.Status != "running" || position.FinishedAt != nil {
		t.Fatal("旧确认覆盖新岗位显示", position.Status, err)
	}
}

// TestPlanItemCleanupMemory 验证共享内存岗位、任务和计划存储的释放与迟到确认。
func TestPlanItemCleanupMemory(t *testing.T) {
	s := NewMemoryExecutionPlanStore()
	s.positions, s.taskRuns = NewMemoryPositionStore(), NewMemoryTaskRunStore()
	email := "cleanup-items@example.com"
	position, err := s.positions.SavePosition(Position{UserEmail: email, Name: "fixture", PlatformID: "boss"})
	if err != nil {
		t.Fatal(err)
	}
	config := validPlanConfig()
	for i := range config.Items {
		config.Items[i].PositionID = position.ID
	}
	p, err := s.Save(t.Context(), ExecutionPlan{UserEmail: email, MachineID: "A", Config: config}, 0)
	if err != nil {
		t.Fatal(err)
	}
	p, err = s.Intent(t.Context(), "", email, p.ID, planIntentFixture(t, "arm"))
	if err != nil {
		t.Fatal(err)
	}
	testPlanItemCleanup(t, s, s.taskRuns, s.positions, p, nil)
}

// TestPlanItemCleanupPostgres 使用真实隔离数据库验证窗口释放和停止确认的任务、岗位事务同步。
func TestPlanItemCleanupPostgres(t *testing.T) {
	db := planPostgresFixture(t)
	email, _ := newExecutionPlanID()
	email += "@example.com"
	tenant, err := NewPostgresTenantStore(db).GetOrCreateTenant(email)
	if err != nil {
		t.Fatal(err)
	}
	positions := NewPostgresPositionStore(db)
	position, err := positions.SavePosition(Position{UserEmail: email, Name: "fixture", PlatformID: "boss"})
	if err != nil {
		t.Fatal(err)
	}
	config := postgresPlanConfig()
	for i := range config.Items {
		config.Items[i].PositionID = position.ID
	}
	s := NewPostgresExecutionPlanStore(db)
	p, err := s.Save(t.Context(), ExecutionPlan{TenantID: tenant.ID, UserEmail: email, MachineID: "A", Config: config}, 0)
	if err != nil {
		t.Fatal(err)
	}
	p, err = s.Intent(t.Context(), tenant.ID, email, p.ID, planIntentFixture(t, "arm"))
	if err != nil {
		t.Fatal(err)
	}
	testPlanItemCleanup(t, s, NewPostgresTaskRunStore(db), positions, p, func(stopped ExecutionPlan, taskID string, confirm ExecutionPlanStopConfirmation) {
		name := "fixture_stop_" + strings.ReplaceAll(taskID, "-", "")
		_, err := db.Exec(`CREATE FUNCTION ` + name + `() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.id::text='` + taskID + `' THEN RAISE EXCEPTION 'fixture task update failure'; END IF; RETURN NEW; END $$; CREATE TRIGGER ` + name + ` BEFORE UPDATE ON task_runs FOR EACH ROW EXECUTE FUNCTION ` + name + `()`)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			_, _ = db.Exec(`DROP TRIGGER IF EXISTS ` + name + ` ON task_runs; DROP FUNCTION IF EXISTS ` + name + `()`)
		})
		if _, err = s.ConfirmStopped(t.Context(), p.TenantID, p.UserEmail, p.ID, confirm); err == nil {
			t.Fatal("关联任务写入失败仍确认停止")
		}
		current, err := s.Get(t.Context(), p.TenantID, p.UserEmail, p.ID)
		if err != nil || !current.StopRequested || current.StateSequence != stopped.StateSequence {
			t.Fatal("任务写入失败开放了编辑保护", err)
		}
		var state string
		if err = db.QueryRow(`SELECT r.state FROM execution_plan_runs r JOIN execution_plan_item_runs i ON i.run_id=r.id WHERE i.task_run_id=$1`, taskID).Scan(&state); err != nil || state != "waiting_window" {
			t.Fatal("停止事务没有回滚父状态", state, err)
		}
		if _, err = db.Exec(`DROP TRIGGER ` + name + ` ON task_runs; DROP FUNCTION ` + name + `()`); err != nil {
			t.Fatal(err)
		}
	})
}
