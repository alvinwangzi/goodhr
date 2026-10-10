// 本文件持久保存 HRPlus 原计划安全步骤的完整状态请求，匹配回执与规范快照原子确认，未确认时不继续页面动作。
package planrunner

import (
	"context"
	"database/sql"
	"errors"
	"goodhr5/local-agent-go/internal/cloudapi"
	"goodhr5/local-agent-go/internal/localdb"
	"goodhr5/local-agent-go/internal/planmodel"
	"goodhr5/local-agent-go/internal/planoperations"
	"reflect"
)

// PersistProgress 先加密保存本次原状态，再发送；不明确响应保留原编号，调用方不能据此推进下一岗位。
// next 是步骤已发生的事实，规范开始时间与停止结果仍以云端明确回执为准。
func (c *Coordinator) PersistProgress(ctx context.Context, held *Acquired, next planmodel.Run, requestID string, a planoperations.Authority, dispatch ...[]byte) (planmodel.Permit, error) {
	if held == nil || held.Reservation == nil || !held.Reservation.Valid() || a.StillCurrent == nil || !a.StillCurrent() || a.OwnerScope != held.scope {
		return planmodel.Permit{}, ErrPlanAuthority
	}
	if err := next.Validate(); err != nil {
		return planmodel.Permit{}, err
	}
	claim, err := c.requests.OriginalClaim(ctx, held.scope, held.claimRequestID)
	if err != nil {
		return planmodel.Permit{}, err
	}
	if next.ID != claim.RunID || next.PlanID != claim.PlanID || next.OwnerID != claim.OwnerID || next.ActivationID != claim.ActivationID || next.ConfigVersion != claim.ExpectedVersion || next.ExecutionDate != claim.ExecutionDate || !reflect.DeepEqual(next.Snapshot, held.Permit.Run.Snapshot) || (next.State != "running" && next.State != "draining") {
		return planmodel.Permit{}, localdb.ErrPlanRequestConflict
	}
	input := cloudapi.PlanRunUpdateRequest{RunID: next.ID, Action: "status", RequestID: requestID, OwnerID: claim.OwnerID, MachineID: claim.MachineID, Credential: claim.Credential, Sequence: next.Sequence, State: next.State, CurrentItem: next.CurrentItem, EndReason: next.EndReason}
	for _, item := range next.Items {
		input.Items = append(input.Items, planmodel.ItemUpdate{ID: item.ID, ItemID: item.ItemID, State: item.State, Actions: item.Actions})
	}
	if err = input.Validate(); err != nil {
		return planmodel.Permit{}, err
	}
	_, existing := c.db.PlanOperation(ctx, held.scope, requestID)
	if errors.Is(existing, sql.ErrNoRows) {
		if _, err := c.db.NextPlanUpdate(ctx, held.scope); err == nil {
			return planmodel.Permit{}, errors.New("还有原状态等待核对，不能登记下一步骤")
		} else if !errors.Is(err, sql.ErrNoRows) {
			return planmodel.Permit{}, err
		}
		current, err := c.db.PlanRunSnapshot(ctx, held.scope, next.ID)
		if err != nil {
			return planmodel.Permit{}, err
		}
		if next.Sequence != current.Sequence+1 || next.CurrentItem < current.CurrentItem || len(next.Items) != len(current.Items) {
			return planmodel.Permit{}, localdb.ErrPlanSnapshotStale
		}
		for index, item := range current.Items {
			actual := next.Items[index]
			if item.ID != actual.ID || item.ItemID != actual.ItemID || item.TaskRunID != actual.TaskRunID {
				return planmodel.Permit{}, localdb.ErrPlanRequestConflict
			}
			for action, progress := range item.Actions {
				if actual.Actions[action].Count < progress.Count || actual.Actions[action].UnknownCount < progress.UnknownCount {
					return planmodel.Permit{}, localdb.ErrPlanSnapshotStale
				}
			}
		}
	} else if existing != nil {
		return planmodel.Permit{}, existing
	}
	identity, err := c.client.SessionIdentity(ctx, a.Token)
	if err != nil {
		return planmodel.Permit{}, err
	}
	if cloudapi.SessionOwnerScope(c.client.BaseURL, identity.UserEmail) != held.scope || !a.StillCurrent() || !held.Reservation.Valid() {
		return planmodel.Permit{}, ErrPlanAuthority
	}
	op, err := c.requests.StageUpdate(ctx, held.scope, next.PlanID, input, dispatch...)
	if err != nil {
		return planmodel.Permit{}, err
	}
	original, err := c.requests.OriginalUpdate(ctx, held.scope, requestID)
	if err != nil {
		return planmodel.Permit{}, err
	}
	// OriginalUpdate 已从原密文解码成独立数据，不发送调用方可变的 next。
	if !a.StillCurrent() || !held.Reservation.Valid() {
		return planmodel.Permit{}, ErrPlanAuthority
	}
	permit, err := c.client.UpdateExecutionPlanRun(held.Reservation.Context(), a.Token, original)
	if err != nil {
		return planmodel.Permit{}, err
	}
	confirm := func() error {
		if !held.Reservation.Valid() {
			return ErrPlanAuthority
		}
		originalDispatch, err := c.requests.OriginalMessageDispatch(ctx, held.scope, requestID)
		if err != nil {
			return err
		}
		return c.db.ConfirmPlanOperationSnapshot(ctx, held.scope, op.RequestID, op.BodyHash, permit.Run, originalDispatch)
	}
	if !a.StillCurrent() || !held.Reservation.Valid() {
		return planmodel.Permit{}, ErrPlanAuthority
	}
	if a.ConfirmCurrent != nil {
		err = a.ConfirmCurrent(confirm)
	} else {
		err = confirm()
	}
	return permit, err
}
