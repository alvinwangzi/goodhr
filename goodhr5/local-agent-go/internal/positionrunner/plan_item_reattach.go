// 本文件恢复 HRPlus 已准备执行项的页面角色，沿用原 TaskRun 与未领取队列，不另起岗位或账号占用。
package positionrunner

import (
	"fmt"
	"goodhr5/local-agent-go/internal/planmodel"
)

// ReattachPlanItem 在同一父许可和原快照下重新附加角色，可处理已经完成主项的已激活消息。
func (r *Runner) ReattachPlanItem(parent *PlanBrowserReservation, scope string, permit planmodel.Permit, itemID string, snapshot PositionRuntimeSnapshot, previous *PlanItemReservation) (*PlanItemReservation, error) {
	if parent == nil || parent.runner != r || !parent.Valid() || permit.Run.ID != parent.lease.planRunID || permit.Run.OwnerID != permit.Owner.OwnerID || permit.Owner.OwnerType != "plan" || permit.Owner.State != "running" {
		return nil, fmt.Errorf("父计划重新附加许可不匹配")
	}
	if err := permit.Run.Validate(); err != nil {
		return nil, err
	}
	found := false
	for _, item := range permit.Run.Items {
		if item.ID == itemID {
			found = item.Order <= permit.Run.CurrentItem && item.TaskRunID == snapshot.Options.CloudRunID && item.Snapshot.PositionID == snapshot.Position.ID && (item.State == "running" || item.State == "completed")
		}
	}
	if !found {
		return nil, fmt.Errorf("原执行项尚未开始或任务关联不匹配")
	}
	options := snapshot.Options
	options.MachineID = permit.Owner.MachineID
	child, err := parent.BorrowItem(scope, itemID, options)
	if err != nil {
		return nil, err
	}
	child.ownerID = permit.Owner.OwnerID
	if previous != nil && previous.state.options.PlanRunID == permit.Run.ID && previous.state.options.ItemRunID == itemID && previous.state.options.CloudRunID == options.CloudRunID {
		child.messages = previous.messages
		child.messageStatsRestored = previous.messageStatsRestored
	}
	return child, nil
}
