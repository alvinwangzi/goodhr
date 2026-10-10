// 本文件验证 HRPlus 稳定队列的状态比较更新、账号隔离和迟到结果保护，使用临时 SQLite。
package localdb

import (
	"errors"
	"testing"
	"time"
)

// TestPlanQueueTransitions 验证只由原等待状态开始，旧取消和另一账号不能覆盖已经运行的项。
func TestPlanQueueTransitions(t *testing.T) {
	db, _ := openReplyDB(t)
	for _, id := range []string{"first", "second"} {
		if _, err := db.EnqueuePlanRequest(t.Context(), PlanWaitingRequest{OwnerScope: "A", RequestID: id, PlanID: id, ActivationID: "batch", TriggeredAt: time.Date(2026, 10, 10, 1, 0, 0, 0, time.UTC)}); err != nil {
			t.Fatal(err)
		}
	}
	waiting, err := db.WaitingPlanRequests(t.Context(), "A")
	if err != nil || len(waiting) != 2 || waiting[0].RequestID != "first" {
		t.Fatal("等待项丢失顺序", err)
	}
	if err := db.TransitionPlanRequest(t.Context(), "B", "first", "waiting", "running"); !errors.Is(err, ErrPlanSnapshotStale) {
		t.Fatal("另一账号改写队列", err)
	}
	if err := db.TransitionPlanRequest(t.Context(), "A", "first", "waiting", "running"); err != nil {
		t.Fatal(err)
	}
	if err := db.TransitionPlanRequest(t.Context(), "A", "first", "waiting", "cancelled"); !errors.Is(err, ErrPlanSnapshotStale) {
		t.Fatal("迟到取消覆盖运行", err)
	}
	if err := db.TransitionPlanRequest(t.Context(), "A", "first", "running", "waiting"); err == nil {
		t.Fatal("运行回退排队改变事实")
	}
	if err := db.TransitionPlanRequest(t.Context(), "A", "first", "running", "done"); err != nil {
		t.Fatal(err)
	}
	if waiting, err := db.WaitingPlanRequests(t.Context(), "A"); err != nil || len(waiting) != 1 || waiting[0].RequestID != "second" {
		t.Fatal("已结算项仍等待", err)
	}
}
