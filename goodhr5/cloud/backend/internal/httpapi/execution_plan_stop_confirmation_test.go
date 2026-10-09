// 本文件验证 HRPlus 停止收尾的设备身份、占用证明、窗口等待结算及幂等回执。
package httpapi

import (
	"errors"
	"testing"
)

// planStopConfirmationFixture 建立原停止批次的真实设备确认参数。
func planStopConfirmationFixture(t *testing.T, p ExecutionPlan) ExecutionPlanStopConfirmation {
	t.Helper()
	request, _ := newExecutionPlanID()
	return ExecutionPlanStopConfirmation{RequestID: request, ExpectedVersion: p.Version, ActivationID: p.ActivationID, MachineID: p.MachineID, CleanupConfirmed: true}
}

// TestPlanStopConfirmationMemory 验证跨窗口等待可以结算，未释放及迟到确认不能越过保护。
func TestPlanStopConfirmationMemory(t *testing.T) {
	s := NewMemoryExecutionPlanStore()
	p, err := s.Save(t.Context(), ExecutionPlan{UserEmail: "confirmation@example.com", MachineID: "A", Config: validPlanConfig()}, 0)
	if err != nil {
		t.Fatal(err)
	}
	p, err = s.Intent(t.Context(), "", p.UserEmail, p.ID, planIntentFixture(t, "arm"))
	if err != nil {
		t.Fatal(err)
	}
	run := ExecutionPlanRun{ID: "fixture", PlanID: p.ID, ActivationID: p.ActivationID, State: "waiting_window", OwnerID: "owner", Sequence: 1}
	s.runs[run.ID] = run
	s.owners = map[string]AccountExecutionOwner{"owner": {OwnerID: "owner", State: "releasing"}}
	stop := planIntentFixture(t, "stop")
	stop.ActivationID = p.ActivationID
	p, err = s.Intent(t.Context(), "", p.UserEmail, p.ID, stop)
	if err != nil || !p.StopRequested {
		t.Fatal(err)
	}
	confirmation := planStopConfirmationFixture(t, p)
	wrong := confirmation
	wrong.CleanupConfirmed = false
	if _, err = s.ConfirmStopped(t.Context(), "", p.UserEmail, p.ID, wrong); err == nil {
		t.Fatal("无收尾确认也完成停止")
	}
	wrong = confirmation
	wrong.MachineID = "B"
	if _, err = s.ConfirmStopped(t.Context(), "", p.UserEmail, p.ID, wrong); !errors.Is(err, ErrExecutionPlanRequest) {
		t.Fatal("其他电脑确认", err)
	}
	if _, err = s.ConfirmStopped(t.Context(), "", p.UserEmail, p.ID, confirmation); !errors.Is(err, ErrExecutionPlanBusy) {
		t.Fatal("账号未释放却确认", err)
	}
	delete(s.owners, "owner")
	// 即使运行状态提前显示终态，再点停止也不能代替设备收尾确认。
	s.runs[run.ID] = ExecutionPlanRun{ID: "other-terminal", PlanID: p.ID, ActivationID: p.ActivationID, State: "completed"}
	secondStop := planIntentFixture(t, "stop")
	secondStop.ActivationID = p.ActivationID
	protected, err := s.Intent(t.Context(), "", p.UserEmail, p.ID, secondStop)
	if err != nil || !protected.StopRequested {
		t.Fatal("再次停止绕过收尾确认", err)
	}
	s.runs[run.ID] = run
	settled, err := s.ConfirmStopped(t.Context(), "", p.UserEmail, p.ID, confirmation)
	if err != nil || settled.StopRequested || s.runs[run.ID].State != "stopped" || s.runs[run.ID].Sequence != 2 || s.runs[run.ID].FinishedAt == nil {
		t.Fatal("跨窗口等待未结算", err)
	}
	again, err := s.ConfirmStopped(t.Context(), "", p.UserEmail, p.ID, confirmation)
	if err != nil || again.StateSequence != settled.StateSequence {
		t.Fatal("确认重放不幂等", err)
	}
	fresh, err := s.Intent(t.Context(), "", p.UserEmail, p.ID, planIntentFixture(t, "arm"))
	if err != nil {
		t.Fatal("收尾后不能再启用", err)
	}
	if _, err = s.ConfirmStopped(t.Context(), "", p.UserEmail, p.ID, confirmation); err != nil {
		t.Fatal(err)
	}
	current, _ := s.Get(t.Context(), "", p.UserEmail, p.ID)
	if current.ActivationID != fresh.ActivationID || current.State != "enabled" {
		t.Fatal("旧确认覆盖新批次")
	}
	wrong = confirmation
	wrong.RequestID, _ = newExecutionPlanID()
	if _, err = s.ConfirmStopped(t.Context(), "", p.UserEmail, p.ID, wrong); !errors.Is(err, ErrExecutionPlanRequest) {
		t.Fatal("新编号的旧确认改变新批次", err)
	}
}

// TestPlanStopConfirmationPostgres 使用真实数据库验证释放前拒绝、释放后同事务结算和解锁。
func TestPlanStopConfirmationPostgres(t *testing.T) {
	db := planPostgresFixture(t)
	s := NewPostgresExecutionPlanStore(db)
	email, _ := newExecutionPlanID()
	p, err := s.Save(t.Context(), ExecutionPlan{UserEmail: email + "@example.com", MachineID: "A", Config: postgresPlanConfig()}, 0)
	if err != nil {
		t.Fatal(err)
	}
	p, err = s.Intent(t.Context(), "", p.UserEmail, p.ID, planIntentFixture(t, "arm"))
	if err != nil {
		t.Fatal(err)
	}
	claim := accountClaimFixture(t)
	claim.UserEmail = p.UserEmail
	ownership := NewPostgresAccountExecutionStore(db)
	if _, err = ownership.Claim(t.Context(), claim); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = db.Exec(`DELETE FROM account_execution_owners WHERE account_key=$1`, p.UserEmail)
		_, _ = db.Exec(`DELETE FROM account_execution_requests WHERE account_key=$1`, p.UserEmail)
	})
	run, _ := newExecutionPlanID()
	item, _ := newExecutionPlanID()
	_, err = db.Exec(`INSERT INTO execution_plan_runs(id,plan_id,activation_id,execution_date,config_version,snapshot,state,owner_id) VALUES($1,$2,$3,'2026-10-10',1,'{}','waiting_window',$4)`, run, p.ID, p.ActivationID, claim.OwnerID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`INSERT INTO execution_plan_item_runs(id,run_id,item_id,ordinal,snapshot,state,counts) VALUES($1,$2,'first',0,'{}','pending','{"greeting":3}')`, item, run)
	if err != nil {
		t.Fatal(err)
	}
	stop := planIntentFixture(t, "stop")
	stop.ActivationID = p.ActivationID
	p, err = s.Intent(t.Context(), "", p.UserEmail, p.ID, stop)
	if err != nil {
		t.Fatal(err)
	}
	confirm := planStopConfirmationFixture(t, p)
	if _, err = s.ConfirmStopped(t.Context(), "", p.UserEmail, p.ID, confirm); !errors.Is(err, ErrExecutionPlanBusy) {
		t.Fatal("未实际释放即解锁", err)
	}
	release := claim
	release.RequestID, _ = newExecutionPlanID()
	if err = ownership.Release(t.Context(), release, true); err != nil {
		t.Fatal(err)
	}
	settled, err := s.ConfirmStopped(t.Context(), "", p.UserEmail, p.ID, confirm)
	if err != nil || settled.StopRequested {
		t.Fatal("释放后仍不能停止", err)
	}
	var state, reason string
	var seq int64
	var finished bool
	if err = db.QueryRow(`SELECT state,end_reason,sequence,finished_at IS NOT NULL FROM execution_plan_runs WHERE id=$1`, run).Scan(&state, &reason, &seq, &finished); err != nil || state != "stopped" || reason != "user_stopped" || seq != 2 || !finished {
		t.Fatal("父运行未结算", state, reason, seq, err)
	}
	var count int
	if err = db.QueryRow(`SELECT state,(counts->>'greeting')::int FROM execution_plan_item_runs WHERE id=$1`, item).Scan(&state, &count); err != nil || state != "stopped" || count != 3 {
		t.Fatal("子项未结算或原计数丢失", err)
	}
	replay, err := s.ConfirmStopped(t.Context(), "", p.UserEmail, p.ID, confirm)
	if err != nil || replay.StateSequence != settled.StateSequence {
		t.Fatal("PG 确认重放不幂等", err)
	}
	if _, err = s.Save(t.Context(), p, 1); err != nil {
		t.Fatal("确认后仍不能编辑", err)
	}
}
