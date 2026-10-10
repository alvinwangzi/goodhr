// 本文件从 HRPlus 原 M1 检查点恢复已开始执行项配置，重启后不以当前岗位模板覆盖原快照。
package positionrunner

import (
	"context"
	"fmt"
	"goodhr5/local-agent-go/internal/cloudapi"
)

// RestorePlanItemSnapshot 复用配置读取但保留原岗位快照、动作、任务与数量，不启动页面或创建新任务。
func (r *Runner) RestorePlanItemSnapshot(ctx context.Context, scope, planRunID, itemID string, options StartOptions) (PositionRuntimeSnapshot, error) {
	cp, err := r.db.LoadActionCheckpoint(ctx, itemID)
	if err != nil {
		return PositionRuntimeSnapshot{}, err
	}
	if cp.OwnerScope != scope || cp.PlanRunID != planRunID || cp.ItemRunID != itemID {
		return PositionRuntimeSnapshot{}, fmt.Errorf("恢复检查点不属于原计划")
	}
	client := cloudapi.New(options.CloudAPIBase)
	identity, err := client.SessionIdentity(ctx, options.Token)
	if err != nil {
		return PositionRuntimeSnapshot{}, err
	}
	if cloudapi.SessionOwnerScope(client.BaseURL, identity.UserEmail) != scope {
		return PositionRuntimeSnapshot{}, fmt.Errorf("恢复登录不属于原所有者")
	}
	position, err := r.db.UpsertPositionSnapshot(localPositionSnapshotFromCloud(cp.PositionSnapshot))
	if err != nil {
		return PositionRuntimeSnapshot{}, err
	}
	if position.ID != cp.PositionID || position.PlatformID != cp.Platform {
		return PositionRuntimeSnapshot{}, fmt.Errorf("原岗位快照身份不匹配")
	}
	options.PlanRunID, options.ItemRunID, options.LocalRunID, options.CloudRunID = cp.PlanRunID, cp.ItemRunID, cp.RunID, cp.CloudRunID
	options.TaskType, options.PrioritizeReply = cp.TaskType, cp.PrioritizeReply
	options.EnableGreet = hasTaskType(parseTaskTypes(cp.TaskType), "greeting")
	return r.buildPositionRuntimeSnapshot(ctx, client, position, options, scanRounds(options))
}
