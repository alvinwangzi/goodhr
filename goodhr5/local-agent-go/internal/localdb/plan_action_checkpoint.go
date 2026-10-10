// 本文件把 HRPlus 独立计划执行项绑定到已有 M1 检查点，恢复保留原编号、数量与候选人事实。
package localdb

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"goodhr5/local-agent-go/internal/planmodel"
	"reflect"
)

// EnsurePlanActionRun 使用原执行项运行编号创建或恢复检查点，同岗位其他项仍有独立计数。
func (db *DB) EnsurePlanActionRun(ctx context.Context, checkpoint ActionCheckpoint) (ActionCheckpoint, error) {
	if !planmodel.ValidID(checkpoint.PlanRunID) || !planmodel.ValidID(checkpoint.ItemRunID) || !planmodel.ValidID(checkpoint.CloudRunID) || checkpoint.OwnerScope == "" || checkpoint.PositionID == "" || checkpoint.Platform == "" || checkpoint.ProfileScope == "" || checkpoint.TaskType == "" {
		return ActionCheckpoint{}, ErrPlanRequestConflict
	}
	checkpoint.RunID = checkpoint.ItemRunID
	// 与从 SQLite JSON 读取的快照保持相同数字类型，比较完整原配置而不是只比较岗位名。
	raw, err := json.Marshal(checkpoint)
	if err != nil {
		return ActionCheckpoint{}, err
	}
	if err = json.Unmarshal(raw, &checkpoint); err != nil {
		return ActionCheckpoint{}, err
	}
	tx, err := db.conn.BeginTx(ctx, nil)
	if err != nil {
		return ActionCheckpoint{}, err
	}
	defer tx.Rollback()
	var stored string
	err = tx.QueryRowContext(ctx, `SELECT checkpoint FROM action_runs WHERE run_id=?`, checkpoint.RunID).Scan(&stored)
	if err == nil {
		var original ActionCheckpoint
		if err = json.Unmarshal([]byte(stored), &original); err != nil {
			return ActionCheckpoint{}, err
		}
		if original.RunID != checkpoint.RunID || original.PlanRunID != checkpoint.PlanRunID || original.ItemRunID != checkpoint.ItemRunID || original.OwnerScope != checkpoint.OwnerScope || original.PositionID != checkpoint.PositionID || original.Platform != checkpoint.Platform || original.CloudRunID != checkpoint.CloudRunID || original.TaskType != checkpoint.TaskType || original.PrioritizeReply != checkpoint.PrioritizeReply || (!original.AccountBound && original.ProfileScope != checkpoint.ProfileScope) || !reflect.DeepEqual(original.PositionSnapshot, checkpoint.PositionSnapshot) {
			return ActionCheckpoint{}, ErrPlanRequestConflict
		}
		return original, tx.Commit()
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return ActionCheckpoint{}, err
	}
	if err = insertActionRun(ctx, tx, checkpoint); err != nil {
		return ActionCheckpoint{}, err
	}
	return checkpoint, tx.Commit()
}
