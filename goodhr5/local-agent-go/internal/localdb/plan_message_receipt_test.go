// 本文件验证 HRPlus 原回执、规范进度与父轮换在真实 SQLite 故障事务中一起回滚和恢复。
package localdb

import (
	"encoding/json"
	"goodhr5/local-agent-go/internal/planmodel"
	"strings"
	"testing"
)

// TestMessageReceiptAtomicRollback 在轮换写入失败处模拟退出，原回执重开后可用原请求一并确认。
func TestMessageReceiptAtomicRollback(t *testing.T) {
	db, cfg := openReplyDB(t)
	_, run := snapshotFixture(t)
	if err := db.SavePlanRunSnapshot(t.Context(), "A", run); err != nil {
		t.Fatal(err)
	}
	next := run
	next.Sequence++
	next.State = "running"
	next.Items[0].State = "running"
	next.Items[0].Actions["auto_reply"] = planmodel.ActionProgress{State: "active", Count: 3}
	raw, _ := json.Marshal(map[string]any{"Schema": 1, "OwnerScope": "A", "Run": next, "PagesSinceScan": 2})
	op, err := db.SavePlanOperation(t.Context(), PlanOperation{OwnerScope: "A", RequestID: "60000000-0000-0000-0000-000000000002", PlanID: next.PlanID, RunID: next.ID, OwnerID: next.OwnerID, Kind: "status", RunSequence: next.Sequence, BodyHash: strings.Repeat("a", 64), Cipher: []byte("fixture-encrypted-request")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.conn.Exec(`CREATE TRIGGER fixture_dispatch_failure BEFORE INSERT ON plan_message_dispatch BEGIN SELECT RAISE(ABORT,'fixture dispatch failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err = db.ConfirmPlanOperationSnapshot(t.Context(), "A", op.RequestID, op.BodyHash, next, raw); err == nil {
		t.Fatal("轮换写入失败仍确认")
	}
	current, err := db.PlanRunSnapshot(t.Context(), "A", run.ID)
	if err != nil || current.Sequence != run.Sequence {
		t.Fatal("轮换失败却保存进度", current, err)
	}
	record, err := db.PlanOperation(t.Context(), "A", op.RequestID)
	if err != nil || record.State != "pending" {
		t.Fatal("失败回执未回滚", record, err)
	}
	if _, err = db.PlanMessageDispatch(t.Context(), "A", run.ID); err == nil {
		t.Fatal("失败事务保留轮换")
	}
	if _, err = db.conn.Exec(`DROP TRIGGER fixture_dispatch_failure`); err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if err = reopened.ConfirmPlanOperationSnapshot(t.Context(), "A", op.RequestID, op.BodyHash, next, raw); err != nil {
		t.Fatal(err)
	}
	confirmed, err := reopened.PlanMessageDispatch(t.Context(), "A", run.ID)
	if err != nil {
		t.Fatal(err)
	}
	var state map[string]any
	if json.Unmarshal(confirmed, &state) != nil || state["PagesSinceScan"] != float64(2) {
		t.Fatal("原轮换未恢复")
	}
	record, err = reopened.PlanOperation(t.Context(), "A", op.RequestID)
	if err != nil || record.State != "confirmed" {
		t.Fatal("原回执未一起确认")
	}
}

// TestMessageReceiptLateConfirmation 验证迟到的原回执只确认请求，不覆盖较新的进度及轮换。
func TestMessageReceiptLateConfirmation(t *testing.T) {
	db, _ := openReplyDB(t)
	_, old := snapshotFixture(t)
	old.State = "running"
	newer := old
	newer.Sequence++
	if err := db.SavePlanRunSnapshot(t.Context(), "A", old); err != nil {
		t.Fatal(err)
	}
	if err := db.SavePlanRunSnapshot(t.Context(), "A", newer); err != nil {
		t.Fatal(err)
	}
	rawNew, _ := json.Marshal(map[string]any{"Schema": 1, "OwnerScope": "A", "Run": newer, "PagesSinceScan": 5})
	if err := db.SavePlanMessageDispatch(t.Context(), "A", newer.ID, newer.Sequence, rawNew); err != nil {
		t.Fatal(err)
	}
	op, err := db.SavePlanOperation(t.Context(), PlanOperation{OwnerScope: "A", RequestID: "60000000-0000-0000-0000-000000000009", PlanID: old.PlanID, RunID: old.ID, OwnerID: old.OwnerID, Kind: "status", RunSequence: old.Sequence, BodyHash: strings.Repeat("b", 64), Cipher: []byte("fixture-encrypted-old-request")})
	if err != nil {
		t.Fatal(err)
	}
	rawOld, _ := json.Marshal(map[string]any{"Schema": 1, "OwnerScope": "A", "Run": old, "PagesSinceScan": 1})
	if err := db.ConfirmPlanOperationSnapshot(t.Context(), "A", op.RequestID, op.BodyHash, old, rawOld); err != nil {
		t.Fatal(err)
	}
	actual, err := db.PlanMessageDispatch(t.Context(), "A", old.ID)
	if err != nil || string(actual) != string(rawNew) {
		t.Fatal("迟到回执覆盖新轮换", err)
	}
	current, err := db.PlanRunSnapshot(t.Context(), "A", old.ID)
	if err != nil || current.Sequence != newer.Sequence {
		t.Fatal("迟到回执覆盖新进度", err)
	}
	confirmed, err := db.PlanOperation(t.Context(), "A", op.RequestID)
	if err != nil || confirmed.State != "confirmed" {
		t.Fatal("旧回执不能确认", err)
	}
}
