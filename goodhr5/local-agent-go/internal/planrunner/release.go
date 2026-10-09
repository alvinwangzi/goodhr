// 本文件先持久化 HRPlus 原计划的页面收尾事实，再释放本地执行权，云端确认交给既有可靠补传。
package planrunner

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"goodhr5/local-agent-go/internal/cloudapi"
	"goodhr5/local-agent-go/internal/localdb"
	"goodhr5/local-agent-go/internal/planmodel"
)

// ReleaseAfterCleanup 保存原占用者的不可变释放事实，授权过期也可留待同账号恢复补传。
// ctx 必须是收尾保存上下文，不能使用已取消的页面动作上下文；此方法不冒称云端已经释放。
func (c *Coordinator) ReleaseAfterCleanup(ctx context.Context, held *Acquired, run planmodel.Run, requestID, state, reason string, cleanupConfirmed bool) (localdb.PlanOperation, error) {
	if held == nil || held.scope == "" || held.claimRequestID == "" || !cleanupConfirmed {
		return localdb.PlanOperation{}, errors.New("缺少原计划占用或页面收尾确认")
	}
	if err := run.Validate(); err != nil {
		return localdb.PlanOperation{}, err
	}
	claim, err := c.requests.OriginalClaim(ctx, held.scope, held.claimRequestID)
	if err != nil {
		return localdb.PlanOperation{}, err
	}
	if run.ID != claim.RunID || run.PlanID != claim.PlanID || run.ActivationID != claim.ActivationID || run.ConfigVersion != claim.ExpectedVersion || run.ExecutionDate != claim.ExecutionDate || run.OwnerID != claim.OwnerID {
		return localdb.PlanOperation{}, localdb.ErrPlanRequestConflict
	}
	input := cloudapi.PlanRunUpdateRequest{RunID: run.ID, Action: "release", RequestID: requestID, OwnerID: claim.OwnerID, MachineID: claim.MachineID, Credential: claim.Credential, Sequence: run.Sequence + 1, State: state, CurrentItem: run.CurrentItem, EndReason: reason, CleanupConfirmed: true}
	for _, item := range run.Items {
		input.Items = append(input.Items, planmodel.ItemUpdate{ID: item.ID, ItemID: item.ItemID, State: item.State, Actions: item.Actions})
	}
	if err = input.Validate(); err != nil {
		return localdb.PlanOperation{}, err
	}
	// 同一编号只能确认相同的完整事实，不能悄悄忽略后来改变的数量。
	existing, err := c.db.PlanOperation(ctx, held.scope, requestID)
	if err == nil {
		original, e := c.requests.OriginalUpdate(ctx, held.scope, requestID)
		if e != nil {
			return localdb.PlanOperation{}, e
		}
		if original.Action != "release" || original.RunID != run.ID || original.OwnerID != claim.OwnerID || original.State != state || original.EndReason != reason {
			return localdb.PlanOperation{}, localdb.ErrPlanRequestConflict
		}
		wanted, _ := json.Marshal(input)
		actual, _ := json.Marshal(original)
		if string(wanted) != string(actual) {
			return localdb.PlanOperation{}, localdb.ErrPlanRequestConflict
		}
		if held.Reservation != nil {
			if e = held.Reservation.Release(true); e != nil {
				return localdb.PlanOperation{}, e
			}
		}
		return existing, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return localdb.PlanOperation{}, err
	}
	// StageUpdate 的唯一请求和内容校验决定能否保存；读不到原记录时不重新生成编号。
	operation, err := c.requests.StageUpdate(ctx, held.scope, run.PlanID, input)
	if err != nil {
		return localdb.PlanOperation{}, err
	}
	if held.Reservation != nil {
		if err = held.Reservation.Release(true); err != nil {
			return operation, err
		}
	}
	return operation, nil
}
