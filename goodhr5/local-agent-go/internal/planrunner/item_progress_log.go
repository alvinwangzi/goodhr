// 本文件把 HRPlus 实际 M1 安全步骤记录到原执行项，切换岗位后也不改用当前角色归属。
package planrunner

import (
	"context"
	"fmt"
)

// recordItemStepLog 核对原准备快照及检查点后写进度日志，写入失败不继续下一步骤。
func (r *M1ExecutionRuntime) recordItemStepLog(ctx context.Context, id, level, message string) error {
	item, exists := r.items[id]
	if !exists || item.Snapshot.Options.LocalRunID == "" {
		return fmt.Errorf("执行项日志缺少原准备快照")
	}
	cp, err := r.coordinator.db.LoadActionCheckpoint(ctx, item.Snapshot.Options.LocalRunID)
	if err != nil {
		return err
	}
	if cp.PlanRunID != r.held.Permit.Run.ID || cp.ItemRunID != id || cp.OwnerScope != r.held.scope || cp.CloudRunID != item.Snapshot.Options.CloudRunID {
		return ErrPlanAuthority
	}
	_, err = r.coordinator.db.AddPlanItemLog(ctx, r.held.scope, cp.RunID, level, message)
	return err
}
