// 本文件验证 HRPlus 重复岗位的独立计数、消息穿插、窗口恢复以及错误快照的原子拒绝。
package httpapi

import (
	"errors"
	"testing"
)

// testPlanItemProgressContract 验证同岗位两个编排项各自计数，前项消息可以继续但后项不提前激活。
func testPlanItemProgressContract(t *testing.T, s ExecutionPlanStore) {
	t.Helper()
	email, _ := newExecutionPlanID()
	p := createArmedPlanFixture(t, s, email+"@example.com")
	c := planRunClaimFixture(t, p)
	permit, err := s.ClaimRun(t.Context(), "", p.UserEmail, c)
	if err != nil {
		t.Fatal(err)
	}
	if len(permit.Run.Items) != 2 || permit.Run.Items[0].ID == permit.Run.Items[1].ID || permit.Run.Items[0].Snapshot.PositionID != permit.Run.Items[1].Snapshot.PositionID {
		t.Fatal("重复岗位未分成独立运行")
	}
	first := planRunUpdateFixture(t, c, 2, "status", "running")
	first.Items = completePlanItemsFixture(permit.Run, 0)
	first.Items[0].State = "running"
	first.Items[0].Actions["auto_reply"] = ExecutionPlanActionProgress{State: "active", Count: 2, UnknownCount: 1}
	active, err := s.UpdateRun(t.Context(), "", p.UserEmail, first)
	if err != nil {
		t.Fatal(err)
	}
	bad := planRunUpdateFixture(t, c, 3, "status", "running")
	bad.Items = completePlanItemsFixture(active.Run, 0)
	bad.Items[0].Actions["auto_reply"] = ExecutionPlanActionProgress{State: "active", Count: 1, UnknownCount: 1}
	if _, err = s.UpdateRun(t.Context(), "", p.UserEmail, bad); !errors.Is(err, ErrExecutionPlanSequence) {
		t.Fatal("计数倒退被接受", err)
	}
	bad.Items = completePlanItemsFixture(active.Run, 0)
	bad.Items[0].Actions["auto_reply"] = ExecutionPlanActionProgress{State: "active", Count: 99, UnknownCount: 1}
	bad.Items[1].State = "running"
	bad.Items[1].Actions["greeting"] = ExecutionPlanActionProgress{State: "active", Count: 3}
	if _, err = s.UpdateRun(t.Context(), "", p.UserEmail, bad); !errors.Is(err, ErrExecutionPlanSequence) {
		t.Fatal("后项提前激活", err)
	}
	unchanged, err := s.GetRun(t.Context(), "", p.UserEmail, c.RunID)
	if err != nil || unchanged.Items[0].Actions["auto_reply"].Count != 2 || unchanged.Items[1].Actions["greeting"].Count != 0 {
		t.Fatal("错误快照部分保存", err)
	}
	moved := planRunUpdateFixture(t, c, 3, "status", "running")
	moved.CurrentItem = 1
	moved.Items = completePlanItemsFixture(active.Run, 0)
	moved.Items[0].State = "completed"
	moved.Items[1].State = "running"
	moved.Items[1].Actions["greeting"] = ExecutionPlanActionProgress{State: "active", Count: 3}
	running, err := s.UpdateRun(t.Context(), "", p.UserEmail, moved)
	if err != nil {
		t.Fatal("前项消息活跃挡住主项切换", err)
	}
	more := planRunUpdateFixture(t, c, 4, "status", "running")
	more.CurrentItem = 1
	more.Items = completePlanItemsFixture(running.Run, 0)
	more.Items[0].Actions["auto_reply"] = ExecutionPlanActionProgress{State: "active", Count: 5, UnknownCount: 1}
	updated, err := s.UpdateRun(t.Context(), "", p.UserEmail, more)
	if err != nil {
		t.Fatal("已结束主项不能继续消息", err)
	}
	// 保存后修改调用者的 map，不得污染存储。
	more.Items[0].Actions["auto_reply"] = ExecutionPlanActionProgress{State: "active", Count: 123}
	waiting := planRunUpdateFixture(t, c, 5, "release", "waiting_window")
	waiting.CurrentItem = 1
	if _, err = s.UpdateRun(t.Context(), "", p.UserEmail, waiting); err != nil {
		t.Fatal(err)
	}
	next := planRunClaimFixture(t, p)
	next.RunID = c.RunID
	resumed, err := s.ClaimRun(t.Context(), "", p.UserEmail, next)
	if err != nil || resumed.Run.Items[0].ID != permit.Run.Items[0].ID || resumed.Run.Items[0].Actions["auto_reply"].Count != 5 || resumed.Run.Items[0].Actions["auto_reply"].UnknownCount != 1 || resumed.Run.Items[1].Actions["greeting"].Count != 3 {
		t.Fatal("跨窗口计数或身份丢失", err)
	}
	confirmed := planRunUpdateFixture(t, next, 7, "status", "running")
	confirmed.CurrentItem = 1
	if _, err = s.UpdateRun(t.Context(), "", p.UserEmail, confirmed); err != nil {
		t.Fatal(err)
	}
	finish := planRunUpdateFixture(t, next, 8, "release", "completed")
	finish.CurrentItem = 2
	if _, err = s.UpdateRun(t.Context(), "", p.UserEmail, finish); !errors.Is(err, ErrExecutionPlanSequence) {
		t.Fatal("消息仍活跃就宣布完成", err)
	}
	finish.Items = completePlanItemsFixture(updated.Run, 2)
	if _, err = s.UpdateRun(t.Context(), "", p.UserEmail, finish); err != nil {
		t.Fatal(err)
	}
	tomorrow := planRunClaimFixture(t, p)
	tomorrow.ExecutionDate = "2026-10-11"
	fresh, err := s.ClaimRun(t.Context(), "", p.UserEmail, tomorrow)
	if err != nil || fresh.Run.Items[0].ID == permit.Run.Items[0].ID || fresh.Run.Items[0].Actions["auto_reply"].Count != 0 {
		t.Fatal("次日复用了旧计数或执行项身份", err)
	}
	history, err := s.GetRun(t.Context(), "", p.UserEmail, c.RunID)
	if err != nil || history.Items[0].Actions["auto_reply"].Count != 5 || history.Items[1].Actions["greeting"].Count != 3 {
		t.Fatal("新运行清掉旧统计", err)
	}
	partial := planRunUpdateFixture(t, tomorrow, 2, "status", "running")
	partial.Items = completePlanItemsFixture(fresh.Run, 0)
	partial.Items[0].State = "running"
	partial.Items[0].Actions["auto_reply"] = ExecutionPlanActionProgress{State: "active", Count: 1, UnknownCount: 1}
	if _, err = s.UpdateRun(t.Context(), "", p.UserEmail, partial); err != nil {
		t.Fatal(err)
	}
	endOfDay := planRunUpdateFixture(t, tomorrow, 3, "release", "incomplete")
	ended, err := s.UpdateRun(t.Context(), "", p.UserEmail, endOfDay)
	if err != nil || ended.Run.Items[0].State != "stopped" || ended.Run.Items[0].Actions["auto_reply"].State != "stopped" || ended.Run.Items[0].Actions["auto_reply"].Count != 1 || ended.Run.Items[0].Actions["auto_reply"].UnknownCount != 1 || ended.Run.Items[1].Actions["greeting"].State != "pending" {
		t.Fatal("日末结束清掉数量或激活了未开始动作", err)
	}
	stillEnabled, err := s.Get(t.Context(), "", p.UserEmail, p.ID)
	if err != nil || stillEnabled.State != "enabled" {
		t.Fatal("当天未完成关闭了周期计划", err)
	}
}

// TestPlanItemProgressStores 验证内存与独立 PostgreSQL 的相同编排项进度契约。
func TestPlanItemProgressStores(t *testing.T) {
	t.Run("memory", func(t *testing.T) { testPlanItemProgressContract(t, NewMemoryExecutionPlanStore()) })
	t.Run("postgres", func(t *testing.T) {
		testPlanItemProgressContract(t, NewPostgresExecutionPlanStore(planPostgresFixture(t)))
	})
}

// TestPlanItemProgressRollbackPostgres 注入第二条执行项写入失败，验证第一条、父状态和占用均回滚。
func TestPlanItemProgressRollbackPostgres(t *testing.T) {
	db := planPostgresFixture(t)
	s := NewPostgresExecutionPlanStore(db)
	email, _ := newExecutionPlanID()
	p := createArmedPlanFixture(t, s, email+"@example.com")
	c := planRunClaimFixture(t, p)
	permit, err := s.ClaimRun(t.Context(), "", p.UserEmail, c)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE OR REPLACE FUNCTION hrplus_m2_fixture_fail_item() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.id::text=TG_ARGV[0] THEN RAISE EXCEPTION 'fixture item write failure'; END IF; RETURN NEW; END $$`)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = db.Exec(`DROP TRIGGER IF EXISTS hrplus_m2_fixture_item_failure ON execution_plan_item_runs`)
		_, _ = db.Exec(`DROP FUNCTION IF EXISTS hrplus_m2_fixture_fail_item()`)
	})
	// 编号由测试自己生成，触发器只影响这一个测试行。
	_, err = db.Exec(`CREATE TRIGGER hrplus_m2_fixture_item_failure BEFORE UPDATE ON execution_plan_item_runs FOR EACH ROW EXECUTE FUNCTION hrplus_m2_fixture_fail_item('` + permit.Run.Items[1].ID + `')`)
	if err != nil {
		t.Fatal(err)
	}
	update := planRunUpdateFixture(t, c, 2, "status", "running")
	update.CurrentItem = 1
	update.Items = completePlanItemsFixture(permit.Run, 1)
	update.Items[0].Actions["auto_reply"] = ExecutionPlanActionProgress{State: "completed", Count: 9}
	update.Items[1].State = "running"
	update.Items[1].Actions["greeting"] = ExecutionPlanActionProgress{State: "active", Count: 3}
	if _, err = s.UpdateRun(t.Context(), "", p.UserEmail, update); err == nil {
		t.Fatal("第二条失败仍提交")
	}
	current, err := s.GetRun(t.Context(), "", p.UserEmail, c.RunID)
	if err != nil || current.Sequence != 1 || current.State != "starting" || current.Items[0].Actions["auto_reply"].Count != 0 {
		t.Fatal("第一条或父状态未回滚", err)
	}
	var state string
	if err = db.QueryRow(`SELECT state FROM account_execution_owners WHERE account_key=$1`, p.UserEmail).Scan(&state); err != nil || state != "starting" {
		t.Fatal("失败写入改变占用状态", err)
	}
}
