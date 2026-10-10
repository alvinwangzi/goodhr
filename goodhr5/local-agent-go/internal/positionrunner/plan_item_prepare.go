// 本文件复用 HRPlus 单岗位配置准备，将独立计划执行项绑定到 M1 状态与检查点，不调用独立岗位启动许可。
package positionrunner

import (
	"context"
	"fmt"
	"goodhr5/local-agent-go/internal/cloudapi"
	"goodhr5/local-agent-go/internal/localdb"
	"goodhr5/local-agent-go/internal/planmodel"
	"goodhr5/local-agent-go/internal/platformcore"
	"goodhr5/local-agent-go/internal/platforms"
	"strings"
)

// BeginPlanItem 复用会员、岗位和配置准备，核对原独立 TaskRun 后恢复 M1 检查点并继承父占用。
// canPrepare 必须来自当前领取协调器，核对名义时段和登录代次；此方法不启动页面或新后台岗位协程。
func (r *Runner) BeginPlanItem(ctx context.Context, parent *PlanBrowserReservation, ownerScope string, permit planmodel.Permit, options StartOptions, canPrepare func() bool) (*PlanItemReservation, PositionRuntimeSnapshot, error) {
	if parent == nil || parent.runner != r || canPrepare == nil || !canPrepare() || !parent.Valid() || ownerScope == "" || options.Token == "" {
		return nil, PositionRuntimeSnapshot{}, fmt.Errorf("父计划准备许可已失效")
	}
	if err := permit.Run.Validate(); err != nil {
		return nil, PositionRuntimeSnapshot{}, err
	}
	if permit.Run.ID != parent.lease.planRunID || permit.Run.OwnerID != permit.Owner.OwnerID || permit.Owner.OwnerType != "plan" || permit.Owner.MachineID != options.MachineID || (permit.Owner.State != "starting" && permit.Owner.State != "running") || (permit.Run.State != "starting" && permit.Run.State != "running") || permit.Run.CurrentItem >= len(permit.Run.Items) {
		return nil, PositionRuntimeSnapshot{}, fmt.Errorf("父计划和执行电脑不匹配")
	}
	item := permit.Run.Items[permit.Run.CurrentItem]
	if !planmodel.ValidID(item.TaskRunID) || (item.State != "pending" && item.State != "running") {
		return nil, PositionRuntimeSnapshot{}, fmt.Errorf("当前执行项尚未取得原岗位任务记录")
	}
	client := cloudapi.New(options.CloudAPIBase)
	identity, err := client.SessionIdentity(ctx, options.Token)
	if err != nil {
		return nil, PositionRuntimeSnapshot{}, err
	}
	if cloudapi.SessionOwnerScope(client.BaseURL, identity.UserEmail) != ownerScope || !canPrepare() || !parent.Valid() {
		return nil, PositionRuntimeSnapshot{}, fmt.Errorf("当前登录不属于原计划所有者")
	}
	cloudPosition, err := client.FetchPosition(ctx, options.Token, item.Snapshot.PositionID)
	if err != nil {
		return nil, PositionRuntimeSnapshot{}, err
	}
	position, err := r.db.UpsertPositionSnapshot(localPositionSnapshotFromCloud(cloudPosition))
	if err != nil {
		return nil, PositionRuntimeSnapshot{}, err
	}
	if position.ID != item.Snapshot.PositionID {
		return nil, PositionRuntimeSnapshot{}, fmt.Errorf("岗位配置与原执行项不匹配")
	}
	options.TaskType = strings.Join(item.Snapshot.Actions, ",")
	options.PrioritizeReply = item.Snapshot.PrioritizeReply
	options.PlanRunID, options.ItemRunID, options.CloudRunID = permit.Run.ID, item.ID, item.TaskRunID
	if hasTaskType(item.Snapshot.Actions, "auto_reply") || hasTaskType(item.Snapshot.Actions, "re_greet") {
		runtime, err := platforms.RuntimeFor(position.PlatformID)
		if err != nil {
			return nil, PositionRuntimeSnapshot{}, err
		}
		messages, supported := runtime.(platformcore.AutoReplyRuntime)
		if !supported {
			return nil, PositionRuntimeSnapshot{}, fmt.Errorf("当前平台不支持所选消息动作")
		}
		if err = messages.AutoReplyAvailable(); err != nil {
			return nil, PositionRuntimeSnapshot{}, err
		}
		if hasTaskType(item.Snapshot.Actions, "re_greet") {
			if _, supported = runtime.(platformcore.ReGreetRuntime); !supported {
				return nil, PositionRuntimeSnapshot{}, fmt.Errorf("当前平台不支持复打招呼")
			}
		}
	}
	snapshot, err := r.buildPositionRuntimeSnapshot(ctx, client, position, options, scanRounds(options))
	if err != nil {
		return nil, PositionRuntimeSnapshot{}, err
	}
	if !canPrepare() || !parent.Valid() {
		return nil, PositionRuntimeSnapshot{}, fmt.Errorf("配置准备后父计划许可已失效")
	}
	checkpoint, err := r.db.EnsurePlanActionRun(ctx, localdb.ActionCheckpoint{PlanRunID: permit.Run.ID, ItemRunID: item.ID, OwnerScope: ownerScope, PositionID: position.ID, Platform: position.PlatformID, ProfileScope: platformcore.ReplyHash("profile:" + safePathName(positionProfileName(position))), CloudRunID: item.TaskRunID, PositionSnapshot: position.PositionSnapshot, TaskType: options.TaskType, PrioritizeReply: options.PrioritizeReply})
	if err != nil {
		return nil, PositionRuntimeSnapshot{}, err
	}
	snapshot.Options.LocalRunID = checkpoint.RunID
	if !canPrepare() || !parent.Valid() {
		return nil, PositionRuntimeSnapshot{}, fmt.Errorf("检查点保存后父计划许可已失效")
	}
	child, err := parent.BorrowItem(ownerScope, item.ID, snapshot.Options)
	if child != nil {
		child.ownerID = permit.Owner.OwnerID
	}
	return child, snapshot, err
}
