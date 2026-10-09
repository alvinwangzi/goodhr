// 本文件验证 HRPlus 启用停止的幂等批次、迟到请求隔离及编辑互斥。
package httpapi

import (
	"errors"
	"testing"
)

// planIntentFixture 生成一个不包含登录凭证的计划意图请求。
func planIntentFixture(t *testing.T, action string) ExecutionPlanIntent {
	t.Helper()
	id, err := newExecutionPlanID()
	if err != nil {
		t.Fatal(err)
	}
	i := ExecutionPlanIntent{RequestID: id, ExpectedVersion: 1, Action: action}
	if action == "arm" {
		i.StartMode = "scheduled"
	}
	return i
}

// testPlanIntentStore 验证内存与真实 PostgreSQL 的相同启用停止契约。
func testPlanIntentStore(t *testing.T, s ExecutionPlanStore) {
	t.Helper()
	email, _ := newExecutionPlanID()
	email += "@example.com"
	p, err := s.Save(t.Context(), ExecutionPlan{UserEmail: email, MachineID: "A", Config: postgresPlanConfig()}, 0)
	if err != nil {
		t.Fatal(err)
	}
	arm := planIntentFixture(t, "arm")
	enabled, err := s.Intent(t.Context(), "", email, p.ID, arm)
	if err != nil || enabled.State != "enabled" || enabled.ActivationID == "" || enabled.StateSequence != p.StateSequence+1 {
		t.Fatal("启用失败", enabled, err)
	}
	replay, err := s.Intent(t.Context(), "", email, p.ID, arm)
	if err != nil || replay.ActivationID != enabled.ActivationID || replay.StateSequence != enabled.StateSequence {
		t.Fatal("重试创建第二批次", err)
	}
	altered := arm
	altered.StartMode = "immediate"
	if _, err = s.Intent(t.Context(), "", email, p.ID, altered); !errors.Is(err, ErrExecutionPlanRequest) {
		t.Fatal("换内容重放被接受", err)
	}
	if _, err = s.Save(t.Context(), p, 1); !errors.Is(err, ErrExecutionPlanBusy) {
		t.Fatal("未停止仍编辑已启用计划", err)
	}
	stop := planIntentFixture(t, "stop")
	stop.ActivationID = enabled.ActivationID
	stopped, err := s.Intent(t.Context(), "", email, p.ID, stop)
	if err != nil || stopped.State != "stopped" || stopped.StopRequested {
		t.Fatal("空闲计划未停止", err)
	}
	nextArm := planIntentFixture(t, "arm")
	next, err := s.Intent(t.Context(), "", email, p.ID, nextArm)
	if err != nil || next.ActivationID == enabled.ActivationID {
		t.Fatal("重新启用未换批次", err)
	}
	if _, err = s.Intent(t.Context(), "", email, p.ID, stop); err != nil {
		t.Fatal("旧停止重放不幂等", err)
	}
	current, err := s.Get(t.Context(), "", email, p.ID)
	if err != nil || current.State != "enabled" || current.ActivationID != next.ActivationID {
		t.Fatal("旧停止覆盖新批次", err)
	}
	staleStop := planIntentFixture(t, "stop")
	staleStop.ActivationID = enabled.ActivationID
	if _, err = s.Intent(t.Context(), "", email, p.ID, staleStop); !errors.Is(err, ErrExecutionPlanRequest) {
		t.Fatal("旧批次停止新计划", err)
	}
	if _, err = s.Intent(t.Context(), "", "foreign@example.com", p.ID, nextArm); !errors.Is(err, ErrNotFound) {
		t.Fatal("越权启用", err)
	}
}

// TestPlanIntentStores 分别运行内存契约和明确启用的真实数据库契约。
func TestPlanIntentStores(t *testing.T) {
	t.Run("memory", func(t *testing.T) { testPlanIntentStore(t, NewMemoryExecutionPlanStore()) })
	t.Run("postgres", func(t *testing.T) { testPlanIntentStore(t, NewPostgresExecutionPlanStore(planPostgresFixture(t))) })
}

// TestPlanStopDuringRun 验证停止关闭后续触发，但不在设备收尾前开放编辑。
func TestPlanStopDuringRun(t *testing.T) {
	s := NewMemoryExecutionPlanStore()
	p, err := s.Save(t.Context(), ExecutionPlan{UserEmail: "active@example.com", MachineID: "A", Config: validPlanConfig()}, 0)
	if err != nil {
		t.Fatal(err)
	}
	enabled, err := s.Intent(t.Context(), "", p.UserEmail, p.ID, planIntentFixture(t, "arm"))
	if err != nil {
		t.Fatal(err)
	}
	s.runs["run"] = ExecutionPlanRun{PlanID: p.ID, State: "waiting_window"}
	stop := planIntentFixture(t, "stop")
	stop.ActivationID = enabled.ActivationID
	stopped, err := s.Intent(t.Context(), "", p.UserEmail, p.ID, stop)
	if err != nil || stopped.State != "stopped" || !stopped.StopRequested {
		t.Fatal("未保留收尾意图", err)
	}
	if _, err = s.Save(t.Context(), p, 1); !errors.Is(err, ErrExecutionPlanBusy) {
		t.Fatal("收尾前编辑", err)
	}
	if _, err = s.Intent(t.Context(), "", p.UserEmail, p.ID, planIntentFixture(t, "arm")); !errors.Is(err, ErrExecutionPlanBusy) {
		t.Fatal("收尾前启动新批次", err)
	}
}

// TestPlanArmEditCompetitionPostgres 验证相同计划行锁下，编辑与启用只有一个成功。
func TestPlanArmEditCompetitionPostgres(t *testing.T) {
	s := NewPostgresExecutionPlanStore(planPostgresFixture(t))
	email, _ := newExecutionPlanID()
	p, err := s.Save(t.Context(), ExecutionPlan{UserEmail: email + "@example.com", MachineID: "A", Config: postgresPlanConfig()}, 0)
	if err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	results := make(chan error, 2)
	arm := planIntentFixture(t, "arm")
	go func() { <-start; _, e := s.Intent(t.Context(), "", p.UserEmail, p.ID, arm); results <- e }()
	go func() { <-start; _, e := s.Save(t.Context(), p, 1); results <- e }()
	close(start)
	success := 0
	for n := 0; n < 2; n++ {
		e := <-results
		if e == nil {
			success++
		} else if !errors.Is(e, ErrExecutionPlanBusy) && !errors.Is(e, ErrExecutionPlanVersion) {
			t.Fatal(e)
		}
	}
	if success != 1 {
		t.Fatalf("编辑与启用同时成功: %d", success)
	}
}

// TestPlanStopReleasingPostgres 验证运行已显示终态但账号仍在收尾时，停止仍保留编辑保护。
func TestPlanStopReleasingPostgres(t *testing.T) {
	db := planPostgresFixture(t)
	s := NewPostgresExecutionPlanStore(db)
	email, _ := newExecutionPlanID()
	p, err := s.Save(t.Context(), ExecutionPlan{UserEmail: email + "@example.com", MachineID: "A", Config: postgresPlanConfig()}, 0)
	if err != nil {
		t.Fatal(err)
	}
	enabled, err := s.Intent(t.Context(), "", p.UserEmail, p.ID, planIntentFixture(t, "arm"))
	if err != nil {
		t.Fatal(err)
	}
	run, _ := newExecutionPlanID()
	owner, _ := newExecutionPlanID()
	_, err = db.Exec(`INSERT INTO execution_plan_runs(id,plan_id,activation_id,execution_date,config_version,snapshot,state,owner_id) VALUES($1,$2,$3,'2026-10-10',1,'{}','completed',$4)`, run, p.ID, enabled.ActivationID, owner)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`INSERT INTO account_execution_owners(account_key,user_email,machine_id,owner_type,owner_id,credential_hash,state) VALUES($1,$1,'A','plan',$2,'fixture','releasing')`, p.UserEmail, owner)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = db.Exec(`DELETE FROM account_execution_owners WHERE account_key=$1`, p.UserEmail) })
	stop := planIntentFixture(t, "stop")
	stop.ActivationID = enabled.ActivationID
	stopped, err := s.Intent(t.Context(), "", p.UserEmail, p.ID, stop)
	if err != nil || !stopped.StopRequested || stopped.State != "stopped" {
		t.Fatal("显示终态被当成已收尾", err)
	}
	if _, err = s.Save(t.Context(), p, 1); !errors.Is(err, ErrExecutionPlanBusy) {
		t.Fatal("收尾中的计划可编辑", err)
	}
	var remaining int
	if err = db.QueryRow(`SELECT count(*) FROM account_execution_owners WHERE account_key=$1`, p.UserEmail).Scan(&remaining); err != nil || remaining != 1 {
		t.Fatal("用户停止意图强行释放账号", remaining, err)
	}
}
