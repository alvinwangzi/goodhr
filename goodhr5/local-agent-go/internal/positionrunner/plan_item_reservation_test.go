// 本文件验证 HRPlus 单岗位状态继承父计划占用，子项切换不释放父锁且旧句柄不能清除新项。
package positionrunner

import (
	"context"
	"errors"
	"goodhr5/local-agent-go/internal/localdb"
	"testing"
)

// TestPlanItemInheritance 验证原检查点归属、父引用收尾和同岗位再次附加的迟到释放隔离。
func TestPlanItemInheritance(t *testing.T) {
	db := openRunnerTestDB(t)
	r := newTestRunner(t, db, &fakeWorker{})
	planID := "20000000-0000-0000-0000-000000000001"
	checkpoint := localdb.ActionCheckpoint{PlanRunID: planID, ItemRunID: "50000000-0000-0000-0000-000000000001", CloudRunID: "70000000-0000-0000-0000-000000000001", OwnerScope: "fixture-owner", PositionID: "same-position", Platform: "boss", ProfileScope: "fixture-profile", TaskType: "greeting", Greeted: 1}
	if _, err := db.EnsurePlanActionRun(t.Context(), checkpoint); err != nil {
		t.Fatal(err)
	}
	parent, err := r.ReservePlanBrowser(t.Context(), planID)
	if err != nil {
		t.Fatal(err)
	}
	options := StartOptions{CloudRunID: checkpoint.CloudRunID, TaskType: "greeting"}
	if _, err = parent.BorrowItem("other-owner", checkpoint.ItemRunID, options); err == nil {
		t.Fatal("其他所有者借用原执行项")
	}
	item, err := parent.BorrowItem(checkpoint.OwnerScope, checkpoint.ItemRunID, options)
	if err != nil || !item.Valid() || item.state.options.LocalRunID != checkpoint.ItemRunID || item.state.options.PlanRunID != planID || item.state.options.CloudRunID != checkpoint.CloudRunID || item.state.runGreeted != 1 {
		t.Fatal("没有继承原项与任务计数", err)
	}
	if err = parent.Release(true); !errors.Is(err, ErrPlanBrowserCleanup) {
		t.Fatal("父占用越过子项收尾", err)
	}
	if err = item.ReleaseAfterCleanup(false); !errors.Is(err, ErrPlanBrowserCleanup) {
		t.Fatal("无子项收尾确认仍释放", err)
	}
	item.state.pendingDetailClose = func(context.Context) error { return errors.New("fixture pending") }
	if err = item.ReleaseAfterCleanup(true); !errors.Is(err, ErrPlanBrowserCleanup) {
		t.Fatal("详情未关仍交还引用", err)
	}
	item.state.pendingDetailClose = nil
	if err = item.ReleaseAfterCleanup(true); err != nil || !parent.Valid() {
		t.Fatal("子项退出释放了父占用", err)
	}
	checkpoint.ItemRunID = "50000000-0000-0000-0000-000000000002"
	checkpoint.CloudRunID = "70000000-0000-0000-0000-000000000002"
	checkpoint.Greeted = 0
	if _, err = db.EnsurePlanActionRun(t.Context(), checkpoint); err != nil {
		t.Fatal(err)
	}
	options.CloudRunID = checkpoint.CloudRunID
	next, err := parent.BorrowItem(checkpoint.OwnerScope, checkpoint.ItemRunID, options)
	if err != nil || next.state.runGreeted != 0 {
		t.Fatal("同岗位下一项无法独立附加", err)
	}
	if err = item.ReleaseAfterCleanup(true); err != nil || !next.Valid() {
		t.Fatal("旧子项释放清掉新项", err)
	}
	if r.StopAll("fixture stop") != 2 || next.Context().Err() == nil || next.Valid() {
		t.Fatal("停止没有取消父子流程")
	}
	if err = next.ReleaseAfterCleanup(true); err != nil {
		t.Fatal(err)
	}
	if err = parent.Release(true); err != nil {
		t.Fatal(err)
	}
	if !r.setRunning("manual", func() {}, StartOptions{}) {
		t.Fatal("完整收尾后仍占用页面")
	}
	r.clear("manual")
}
