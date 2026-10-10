// 本文件用真实 SQLite 故障注入验证 HRPlus 停止确认的原子性和迟到回执保护。
package localdb

import (
	"strings"
	"testing"
)

// TestStopSnapshotAtomic 验证确认标记写入失败时计划快照和原等待状态一并回滚。
func TestStopSnapshotAtomic(t *testing.T) {
	db, _ := openReplyDB(t)
	p, _ := snapshotFixture(t)
	p.State = "stopped"
	p.StopRequested = true
	p.StateSequence++
	if err := db.SaveCachedPlan(t.Context(), "A", p); err != nil {
		t.Fatal(err)
	}
	o, err := db.SavePlanStop(t.Context(), PlanStopOperation{OwnerScope: "A", RequestID: "60000000-0000-0000-0000-000000000099", PlanID: p.ID, ActivationID: p.ActivationID, Version: p.Version, MachineID: p.MachineID, BodyHash: strings.Repeat("a", 64), Cipher: []byte("fixture encrypted")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.EnqueuePlanRequest(t.Context(), PlanWaitingRequest{OwnerScope: "A", RequestID: "wait", PlanID: p.ID, ActivationID: p.ActivationID, TriggeredAt: p.CreatedAt}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.conn.Exec(`CREATE TRIGGER stop_confirm_failure BEFORE UPDATE ON plan_stop_operations BEGIN SELECT RAISE(ABORT,'fixture failure'); END`); err != nil {
		t.Fatal(err)
	}
	settled := p
	settled.StopRequested = false
	settled.StateSequence++
	if err := db.ConfirmPlanStopSnapshot(t.Context(), o, settled); err == nil {
		t.Fatal("故障未回滚确认")
	}
	cached, err := db.CachedPlans(t.Context(), "A")
	if err != nil || !cached[0].StopRequested {
		t.Fatal("确认失败却解除保护", err)
	}
	if waiting, err := db.WaitingPlanRequests(t.Context(), "A"); err != nil || len(waiting) != 1 {
		t.Fatal("确认失败却取消排队", err)
	}
	if _, err := db.conn.Exec(`DROP TRIGGER stop_confirm_failure`); err != nil {
		t.Fatal(err)
	}
	newer := p
	newer.Version++
	newer.StateSequence += 3
	newer.Config.Name = "新配置"
	newer.StopRequested = false
	if err := db.SaveCachedPlan(t.Context(), "A", newer); err != nil {
		t.Fatal(err)
	}
	if err := db.ConfirmPlanStopSnapshot(t.Context(), o, settled); err != nil {
		t.Fatal(err)
	}
	cached, err = db.CachedPlans(t.Context(), "A")
	if err != nil || cached[0].Version != newer.Version || cached[0].Config.Name != "新配置" {
		t.Fatal("旧回执覆盖新版本", err)
	}
	if record, err := db.PlanStopOperation(t.Context(), "A", o.RequestID); err != nil || record.State != "confirmed" {
		t.Fatal("合法旧回执未确认", err)
	}
}
