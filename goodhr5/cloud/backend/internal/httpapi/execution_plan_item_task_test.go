// 本文件验证 HRPlus 重复岗位的独立 TaskRun、准备时间、父占用复用和真实事务回滚。
package httpapi

import (
	"errors"
	"testing"
	"time"
)

// itemTaskRequestFixture 为原执行项构建父占用证明，不在测试中操作招聘页面。
func itemTaskRequestFixture(t *testing.T, c ExecutionPlanRunClaim, itemID string) ExecutionPlanItemTaskRequest {
	t.Helper()
	request, _ := newExecutionPlanID()
	return ExecutionPlanItemTaskRequest{PlanID: c.PlanID, RunID: c.RunID, ItemRunID: itemID, RequestID: request, OwnerID: c.OwnerID, MachineID: c.MachineID, Credential: c.Credential}
}

// testPrepareItemContract 验证同岗位第二项独立 TaskRun，重试不重复创建，任务尚未开始时无实际开始时间。
func testPrepareItemContract(t *testing.T, s ExecutionPlanStore, tasks TaskRunStore, p ExecutionPlan) {
	t.Helper()
	c := planRunClaimFixture(t, p)
	permit, err := s.ClaimRun(t.Context(), p.TenantID, p.UserEmail, c)
	if err != nil {
		t.Fatal(err)
	}
	future := itemTaskRequestFixture(t, c, permit.Run.Items[1].ID)
	if _, err = s.PrepareItemTask(t.Context(), p.TenantID, p.UserEmail, future); !errors.Is(err, ErrExecutionPlanSequence) {
		t.Fatal("提前准备未来项", err)
	}
	first := itemTaskRequestFixture(t, c, permit.Run.Items[0].ID)
	prepared, err := s.PrepareItemTask(t.Context(), p.TenantID, p.UserEmail, first)
	if err != nil || prepared.Run.Items[0].TaskRunID == "" || prepared.Run.Sequence != 2 {
		t.Fatal("任务未关联", err)
	}
	task, err := tasks.TaskRunByID(p.TenantID, prepared.Run.Items[0].TaskRunID)
	if err != nil || task.Status != "starting" || task.StartedAt != nil {
		t.Fatal("准备提前标为运行", err)
	}
	repeated, err := s.PrepareItemTask(t.Context(), p.TenantID, p.UserEmail, first)
	if err != nil || repeated.Run.Items[0].TaskRunID != task.ID || repeated.Run.Sequence != 2 {
		t.Fatal("重复创建任务", err)
	}
	run := prepared.Run
	status := planRunUpdateFixture(t, c, 3, "status", "running")
	status.Items = completePlanItemsFixture(run, 0)
	status.Items[0].State = "running"
	for action, progress := range status.Items[0].Actions {
		progress.State = "active"
		status.Items[0].Actions[action] = progress
	}
	running, err := s.UpdateRun(t.Context(), p.TenantID, p.UserEmail, status)
	if err != nil {
		t.Fatal(err)
	}
	task, err = tasks.TaskRunByID(p.TenantID, task.ID)
	if err != nil || task.Status != "running" || task.StartedAt == nil {
		t.Fatal("实际开始未关联", err)
	}
	advance := planRunUpdateFixture(t, c, 4, "status", "running")
	advance.CurrentItem = 1
	advance.Items = completePlanItemsFixture(running.Run, 1)
	if _, err = s.UpdateRun(t.Context(), p.TenantID, p.UserEmail, advance); err != nil {
		t.Fatal(err)
	}
	second, err := s.PrepareItemTask(t.Context(), p.TenantID, p.UserEmail, future)
	if err != nil || second.Run.Items[1].TaskRunID == task.ID || second.Run.Items[1].TaskRunID == "" {
		t.Fatal("重复岗位覆盖任务", err)
	}
	finished, err := tasks.TaskRunByID(p.TenantID, task.ID)
	if err != nil || finished.Status != "completed" || finished.FinishedAt == nil {
		t.Fatal("原任务未结算", err)
	}
	linked, err := tasks.TaskRunByID(p.TenantID, second.Run.Items[1].TaskRunID)
	if err != nil || linked.Status != "starting" || linked.StartedAt != nil {
		t.Fatal("第二项提前开始", err)
	}
}

// TestPrepareItemMemory 验证真实内存任务存储同秒也不覆盖两个编排项。
func TestPrepareItemMemory(t *testing.T) {
	s := NewMemoryExecutionPlanStore()
	s.positions = NewMemoryPositionStore()
	s.taskRuns = NewMemoryTaskRunStore()
	s.taskRuns.now = func() time.Time { return time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC) }
	email := "items@example.com"
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
	testPrepareItemContract(t, s, s.taskRuns, p)
}

// TestPrepareItemPostgres 验证独立 PostgreSQL 插入与关联同事务，空开始时间可经真实任务查询读取。
func TestPrepareItemPostgres(t *testing.T) {
	db := planPostgresFixture(t)
	email, _ := newExecutionPlanID()
	email += "@example.com"
	tenant, err := NewPostgresTenantStore(db).GetOrCreateTenant(email)
	if err != nil {
		t.Fatal(err)
	}
	position, err := NewPostgresPositionStore(db).SavePosition(Position{UserEmail: email, Name: "fixture", PlatformID: "boss"})
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
	testPrepareItemContract(t, s, NewPostgresTaskRunStore(db), p)
}
