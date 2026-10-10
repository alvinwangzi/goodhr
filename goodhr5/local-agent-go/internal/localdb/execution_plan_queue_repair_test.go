// 本文件用真实 SQLite 重开验证 HRPlus 冷启动原队列修复，仅依据同账号同占用的已确认释放。
package localdb

import (
	"reflect"
	"strings"
	"testing"
)

// TestQueueRepairOriginalRelease 验证未确认、另一占用及另一账号不能结算旧队列，重开后原确认可修复。
func TestQueueRepairOriginalRelease(t *testing.T) {
	db, cfg := openReplyDB(t)
	p, _ := snapshotFixture(t)
	request := PlanWaitingRequest{OwnerScope: "A", RequestID: "60000000-0000-0000-0000-000000000001", PlanID: p.ID, ActivationID: p.ActivationID, TriggeredAt: p.CreatedAt}
	saved, err := db.EnqueuePlanRequest(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.TransitionPlanRequest(t.Context(), "A", request.RequestID, "waiting", "running"); err != nil {
		t.Fatal(err)
	}
	claim := PlanOperation{OwnerScope: "A", RequestID: request.RequestID, PlanID: p.ID, RunID: "20000000-0000-0000-0000-000000000001", OwnerID: "40000000-0000-0000-0000-000000000001", Kind: "claim", BodyHash: strings.Repeat("a", 64), Cipher: []byte("fixture encrypted")}
	if _, err := db.SavePlanOperation(t.Context(), claim); err != nil {
		t.Fatal(err)
	}
	release := claim
	release.Kind = "release"
	release.RunSequence = 3
	release.RequestID = "60000000-0000-0000-0000-000000000002"
	if _, err := db.SavePlanOperation(t.Context(), release); err != nil {
		t.Fatal(err)
	}
	if n, err := db.RepairReleasedPlanQueue(t.Context(), "A"); err != nil || n != 0 {
		t.Fatal("未确认释放就结算", n, err)
	}
	other := release
	other.RequestID = "60000000-0000-0000-0000-000000000003"
	other.OwnerID = "40000000-0000-0000-0000-000000000099"
	if _, err := db.SavePlanOperation(t.Context(), other); err != nil {
		t.Fatal(err)
	}
	if err := db.ConfirmPlanOperation(t.Context(), "A", other.RequestID, other.BodyHash); err != nil {
		t.Fatal(err)
	}
	if n, err := db.RepairReleasedPlanQueue(t.Context(), "A"); err != nil || n != 0 {
		t.Fatal("另一占用结算原队列", n, err)
	}
	if err := db.ConfirmPlanOperation(t.Context(), "A", release.RequestID, release.BodyHash); err != nil {
		t.Fatal(err)
	}
	if n, err := db.RepairReleasedPlanQueue(t.Context(), "B"); err != nil || n != 0 {
		t.Fatal("另一账号修改队列", n, err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = Open(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if n, err := db.RepairReleasedPlanQueue(t.Context(), "A"); err != nil || n != 1 {
		t.Fatal("重开未修复原记录", n, err)
	}
	var actual PlanWaitingRequest
	var triggered int64
	if err := db.conn.QueryRow(`SELECT sequence,owner_scope,request_id,plan_id,activation_id,triggered_ns,state FROM plan_waiting_requests WHERE owner_scope='A'`).Scan(&actual.Sequence, &actual.OwnerScope, &actual.RequestID, &actual.PlanID, &actual.ActivationID, &triggered, &actual.State); err != nil {
		t.Fatal(err)
	}
	actual.TriggeredAt = saved.TriggeredAt
	expected := saved
	expected.State = "done"
	if !reflect.DeepEqual(actual, expected) || triggered != saved.TriggeredAt.UnixNano() {
		t.Fatal("修复改写原顺序、时间或批次")
	}
	if n, err := db.RepairReleasedPlanQueue(t.Context(), "A"); err != nil || n != 0 {
		t.Fatal("重复修复改变记录", n, err)
	}
	if err := db.TransitionPlanRequest(t.Context(), "A", request.RequestID, "running", "done"); err != nil {
		t.Fatal("在线收尾与冷修复不能共存", err)
	}
}
