// 本文件验证 HRPlus 实际准备适配器记录原执行项进度，不把错误任务或缺少快照的项作为日志来源。
package planrunner

import (
	"goodhr5/local-agent-go/internal/planmodel"
	"goodhr5/local-agent-go/internal/positionrunner"
	"sync/atomic"
	"testing"
)

// TestM1ItemProgressLogOrigin 使用真实准备、检查点和 SQLite 检查日志归属，不调用招聘页面。
func TestM1ItemProgressLogOrigin(t *testing.T) {
	c, plan, claim, authority, _, _ := acquireFixture(t, &atomic.Int32{}, func(p *planmodel.Permit) {
		p.Run.Snapshot.Items[0].Actions = []string{"greeting"}
		p.Run.Snapshot.Items[0].PrioritizeReply = false
		p.Run.Items[0].Snapshot = p.Run.Snapshot.Items[0]
		p.Run.Items[0].Actions = map[string]planmodel.ActionProgress{"greeting": {State: "pending"}}
	})
	held, err := c.Acquire(t.Context(), plan, claim, authority)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Reservation.Release(true)
	prepared, err := c.BeginItem(t.Context(), held, held.Permit.Run, "60000000-0000-0000-0000-000000000008", authority, positionrunner.StartOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer prepared.Reservation.ReleaseAfterCleanup(true)
	id := prepared.Permit.Run.Items[0].ID
	runtime := NewM1ExecutionRuntime(c, held, authority, positionrunner.StartOptions{})
	runtime.items[id] = prepared
	if err = runtime.recordItemStepLog(t.Context(), id, "info", "实际步骤已返回"); err != nil {
		t.Fatal(err)
	}
	wrong := prepared
	wrong.Snapshot.Options.CloudRunID = "70000000-0000-0000-0000-000000000009"
	runtime.items[id] = wrong
	if err = runtime.recordItemStepLog(t.Context(), id, "info", "错误任务不能记录"); err == nil {
		t.Fatal("错误任务成为日志来源")
	}
	if err = runtime.recordItemStepLog(t.Context(), "missing", "info", "不存在的项"); err == nil {
		t.Fatal("没有准备快照仍能记录")
	}
	items, err := c.db.ListPlanItemLogs(t.Context(), held.scope, held.Permit.Run.ID, id, 0, 100)
	if err != nil || len(items) != 1 || items[0].TaskRunID != prepared.Permit.Run.Items[0].TaskRunID {
		t.Fatal("进度日志不属于原任务", items, err)
	}
}
