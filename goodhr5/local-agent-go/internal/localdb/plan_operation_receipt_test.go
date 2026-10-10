// 本文件用真实 SQLite 故障事务和重开验证 HRPlus 原回执与进度一起确认，旧回执不能覆盖新计数。
package localdb

import (
	"goodhr5/local-agent-go/internal/planmodel"
	"strings"
	"testing"
)

// TestPlanReceiptSnapshotAtomic 验证第二次写入失败回滚进度，原回执重试及迟到回执保留更高序号。
func TestPlanReceiptSnapshotAtomic(t *testing.T) {
	db, cfg := openReplyDB(t)
	_, run := snapshotFixture(t)
	if err := db.SavePlanRunSnapshot(t.Context(), "A", run); err != nil {
		t.Fatal(err)
	}
	run.Sequence = 2
	run.State = "running"
	run.Items[0].State = "running"
	run.Items[0].Actions["auto_reply"] = planmodel.ActionProgress{State: "active", Count: 3}
	op, err := db.SavePlanOperation(t.Context(), PlanOperation{OwnerScope: "A", RequestID: "60000000-0000-0000-0000-000000000001", PlanID: run.PlanID, RunID: run.ID, OwnerID: run.OwnerID, Kind: "status", RunSequence: 2, BodyHash: strings.Repeat("a", 64), Cipher: []byte("fixture-encrypted-bytes")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.conn.Exec(`CREATE TRIGGER fixture_receipt_failure BEFORE UPDATE ON plan_operations BEGIN SELECT RAISE(ABORT,'fixture receipt failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err = db.ConfirmPlanOperationSnapshot(t.Context(), "A", op.RequestID, op.BodyHash, run); err == nil {
		t.Fatal("回执写入失败仍确认")
	}
	old, err := db.PlanRunSnapshot(t.Context(), "A", run.ID)
	if err != nil || old.Sequence != 1 {
		t.Fatal("回执失败却保存了新进度", err)
	}
	record, err := db.PlanOperation(t.Context(), "A", op.RequestID)
	if err != nil || record.State != "pending" {
		t.Fatal("失败后丢失原请求", err)
	}
	if _, err = db.conn.Exec(`DROP TRIGGER fixture_receipt_failure`); err != nil {
		t.Fatal(err)
	}
	if err = db.ConfirmPlanOperationSnapshot(t.Context(), "A", op.RequestID, op.BodyHash, run); err != nil {
		t.Fatal(err)
	}
	newer := run
	newer.Sequence = 3
	newer.Items = append([]planmodel.ItemRun(nil), run.Items...)
	newer.Items[0].Actions = map[string]planmodel.ActionProgress{}
	for action, progress := range run.Items[0].Actions {
		newer.Items[0].Actions[action] = progress
	}
	newer.Items[0].Actions["auto_reply"] = planmodel.ActionProgress{State: "active", Count: 4}
	if err = db.SavePlanRunSnapshot(t.Context(), "A", newer); err != nil {
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
	if err = reopened.ConfirmPlanOperationSnapshot(t.Context(), "A", op.RequestID, op.BodyHash, run); err != nil {
		t.Fatal("重开后无法确认旧回执", err)
	}
	kept, err := reopened.PlanRunSnapshot(t.Context(), "A", run.ID)
	if err != nil || kept.Sequence != 3 || kept.Items[0].Actions["auto_reply"].Count != 4 {
		t.Fatal("迟到回执覆盖新进度", err)
	}
	if err = reopened.ConfirmPlanOperationSnapshot(t.Context(), "A", op.RequestID, strings.Repeat("b", 64), run); err == nil {
		t.Fatal("不同原内容摘要也确认")
	}
}
