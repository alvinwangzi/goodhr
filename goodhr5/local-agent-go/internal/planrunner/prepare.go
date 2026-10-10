// 本文件连接 HRPlus 父计划预留与独立岗位任务准备，先保存加密原请求再访问云端，不操作招聘页面。
package planrunner

import (
	"context"
	"errors"
	"goodhr5/local-agent-go/internal/cloudapi"
	"goodhr5/local-agent-go/internal/localdb"
	"goodhr5/local-agent-go/internal/planmodel"
	"goodhr5/local-agent-go/internal/planoperations"
	"reflect"
)

// PrepareItem 在现有父预留中准备当前项；模糊结果保留原请求和占用，调用方不能开始岗位页面操作。
func (c *Coordinator) PrepareItem(ctx context.Context, held *Acquired, run planmodel.Run, requestID string, a planoperations.Authority) (planmodel.Permit, error) {
	if held == nil || !held.CanPrepare() || a.StillCurrent == nil || !a.StillCurrent() || a.OwnerScope != held.scope {
		return planmodel.Permit{}, ErrPlanAuthority
	}
	if err := run.Validate(); err != nil {
		return planmodel.Permit{}, err
	}
	if run.CurrentItem >= len(run.Items) || (run.State != "starting" && run.State != "running") {
		return planmodel.Permit{}, ErrPlanNeedsSettlement
	}
	claim, err := c.requests.OriginalClaim(ctx, held.scope, held.claimRequestID)
	if err != nil {
		return planmodel.Permit{}, err
	}
	if run.ID != claim.RunID || run.PlanID != claim.PlanID || run.ActivationID != claim.ActivationID || run.ExecutionDate != claim.ExecutionDate || run.ConfigVersion != claim.ExpectedVersion || run.OwnerID != claim.OwnerID || !reflect.DeepEqual(run.Snapshot, held.Permit.Run.Snapshot) {
		return planmodel.Permit{}, localdb.ErrPlanRequestConflict
	}
	identity, err := c.client.SessionIdentity(ctx, a.Token)
	if err != nil {
		return planmodel.Permit{}, err
	}
	if cloudapi.SessionOwnerScope(c.client.BaseURL, identity.UserEmail) != held.scope || !held.CanPrepare() || !a.StillCurrent() {
		return planmodel.Permit{}, ErrPlanAuthority
	}
	item := run.Items[run.CurrentItem]
	// 冷恢复当前项的准备回执不明确时，重用原请求，不能为同一次准备换新编号。
	operations, err := c.db.PlanRunOperations(ctx, held.scope, run.ID)
	if err != nil {
		return planmodel.Permit{}, err
	}
	for _, record := range operations {
		if record.Kind != "prepare_item" || record.State != "pending" || record.OwnerID != claim.OwnerID {
			continue
		}
		original, err := c.requests.OriginalPrepare(ctx, held.scope, record.RequestID)
		if err != nil {
			return planmodel.Permit{}, err
		}
		if original.ItemRunID != item.ID {
			continue
		}
		if original.RunID != run.ID || original.OwnerID != claim.OwnerID || original.MachineID != claim.MachineID || original.Credential != claim.Credential {
			return planmodel.Permit{}, localdb.ErrPlanRequestConflict
		}
		requestID = original.RequestID
		break
	}
	input := cloudapi.PlanItemTaskRequest{RunID: run.ID, ItemRunID: item.ID, RequestID: requestID, OwnerID: claim.OwnerID, MachineID: claim.MachineID, Credential: claim.Credential}
	operation, err := c.requests.StagePrepare(ctx, held.scope, run.PlanID, input)
	if err != nil {
		return planmodel.Permit{}, err
	}
	original, err := c.requests.OriginalPrepare(ctx, held.scope, requestID)
	if err != nil {
		return planmodel.Permit{}, err
	}
	if !held.CanPrepare() || !a.StillCurrent() {
		return planmodel.Permit{}, ErrPlanAuthority
	}
	result, err := c.client.PrepareExecutionPlanItemTask(held.Reservation.Context(), a.Token, original)
	if err != nil {
		return planmodel.Permit{}, err
	}
	if result.Run.PlanID != run.PlanID || result.Run.ActivationID != run.ActivationID || result.Run.ExecutionDate != run.ExecutionDate || result.Run.ConfigVersion != run.ConfigVersion || !reflect.DeepEqual(result.Run.Snapshot, run.Snapshot) || result.Run.CurrentItem != run.CurrentItem || result.Run.Sequence < run.Sequence || result.Run.Sequence > run.Sequence+1 || (item.TaskRunID != "" && result.Run.Items[run.CurrentItem].TaskRunID != item.TaskRunID) {
		return planmodel.Permit{}, localdb.ErrPlanRequestConflict
	}
	confirm := func() error {
		if !held.Reservation.Valid() {
			return ErrPlanAuthority
		}
		return c.db.ConfirmPlanOperationSnapshot(ctx, held.scope, operation.RequestID, operation.BodyHash, result.Run)
	}
	if !a.StillCurrent() || !held.Reservation.Valid() {
		return planmodel.Permit{}, ErrPlanAuthority
	}
	if a.ConfirmCurrent != nil {
		err = a.ConfirmCurrent(confirm)
	} else {
		err = confirm()
	}
	if err != nil {
		return planmodel.Permit{}, err
	}
	if !held.CanPrepare() || !a.StillCurrent() {
		// 迟到任务关联也是已发生事实，保存后交给收尾，不能因此打开页面。
		return result, errors.Join(ErrPlanNeedsSettlement, ErrPlanAuthority)
	}
	return result, nil
}
