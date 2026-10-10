// 本文件在 HRPlus 计划失败或停止后保存实际检查点与原释放请求；清理未确认时保留父占用。
package planrunner

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"goodhr5/local-agent-go/internal/localdb"
	"time"
)

// StageFailureRelease 在已停止领取新动作后等待子项实际退出，先持久保存释放再交还本地父占用。
// 状态上报不明确时沿用原上报序号；准备回执不明确时必须先只读核对云端关联，不能猜测任务或序号。
func (r *M1ExecutionRuntime) StageFailureRelease(ctx context.Context, state, reason string) (localdb.PlanOperation, error) {
	if state != "blocked" && state != "stopped" && state != "incomplete" {
		return localdb.PlanOperation{}, fmt.Errorf("异常收尾状态不正确")
	}
	if r.held == nil || r.held.scope == "" || r.held.Reservation == nil {
		return localdb.PlanOperation{}, ErrPlanAuthority
	}
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	for id, item := range r.items {
		if item.Reservation != nil {
			if err := r.CloseItem(cleanup, id); err != nil {
				return localdb.PlanOperation{}, err
			}
		}
	}
	run, err := r.coordinator.db.PlanRunSnapshot(cleanup, r.held.scope, r.held.Permit.Run.ID)
	if err != nil {
		return localdb.PlanOperation{}, err
	}
	operations, err := r.coordinator.db.PlanRunOperations(cleanup, r.held.scope, run.ID)
	if err != nil {
		return localdb.PlanOperation{}, err
	}
	unknownPrepare := false
	for _, operation := range operations {
		if operation.OwnerID != r.held.Permit.Run.OwnerID {
			continue
		}
		if operation.Kind == "release" {
			original, err := r.coordinator.requests.OriginalUpdate(cleanup, r.held.scope, operation.RequestID)
			if err != nil || original.RunID != run.ID || original.OwnerID != run.OwnerID {
				return localdb.PlanOperation{}, localdb.ErrPlanRequestConflict
			}
			// 原释放已经冻结，不因第二次收尾改写数量、状态或请求编号。
			if err := r.held.Reservation.Release(true); err != nil {
				return localdb.PlanOperation{}, err
			}
			return operation, nil
		}
		if operation.Kind == "prepare_item" && operation.State == "pending" {
			unknownPrepare = true
		}
	}
	if unknownPrepare {
		if r.authority.StillCurrent == nil || !r.authority.StillCurrent() {
			return localdb.PlanOperation{}, ErrPlanAuthority
		}
		actual, err := r.coordinator.client.ExecutionPlanRun(cleanup, r.authority.Token, run.ID)
		if err != nil {
			return localdb.PlanOperation{}, err
		}
		if actual.ID != run.ID || actual.PlanID != run.PlanID || actual.OwnerID != run.OwnerID || actual.ActivationID != run.ActivationID || actual.ConfigVersion != run.ConfigVersion || actual.ExecutionDate != run.ExecutionDate || !r.authority.StillCurrent() {
			return localdb.PlanOperation{}, ErrPlanAuthority
		}
		// 保存方法校验原编排、任务与数量，不能用只读核对改绑另一运行。
		if err := r.coordinator.db.SavePlanRunSnapshot(cleanup, r.held.scope, actual); err != nil {
			return localdb.PlanOperation{}, err
		}
		run = actual
	}
	for _, operation := range operations {
		if operation.OwnerID != run.OwnerID || operation.Kind != "status" || operation.State != "pending" {
			continue
		}
		original, err := r.coordinator.requests.OriginalUpdate(cleanup, r.held.scope, operation.RequestID)
		if err != nil {
			return localdb.PlanOperation{}, err
		}
		if original.Sequence <= run.Sequence {
			continue
		}
		if original.RunID != run.ID || original.Sequence != run.Sequence+1 || len(original.Items) != len(run.Items) {
			return localdb.PlanOperation{}, localdb.ErrPlanRequestConflict
		}
		for index, item := range original.Items {
			if item.ID != run.Items[index].ID || item.ItemID != run.Items[index].ItemID {
				return localdb.PlanOperation{}, localdb.ErrPlanRequestConflict
			}
			run.Items[index].State, run.Items[index].Actions = item.State, item.Actions
		}
		run.Sequence, run.CurrentItem = original.Sequence, original.CurrentItem
	}
	for index, item := range run.Items {
		actual, err := r.Progress(cleanup, item.ID)
		if errors.Is(err, sql.ErrNoRows) {
			if _, exists := r.items[item.ID]; exists {
				return localdb.PlanOperation{}, err
			}
			continue // 尚未准备的项没有页面动作，保留原零数量。
		}
		if err != nil {
			return localdb.PlanOperation{}, err
		}
		for action, progress := range item.Actions {
			fact, exists := actual[action]
			if !exists {
				return localdb.PlanOperation{}, localdb.ErrPlanRequestConflict
			}
			if fact.Count > progress.Count {
				progress.Count = fact.Count
			}
			if fact.UnknownCount > progress.UnknownCount {
				progress.UnknownCount = fact.UnknownCount
			}
			run.Items[index].Actions[action] = progress
		}
	}
	return r.coordinator.ReleaseAfterCleanup(cleanup, r.held, run, uuid.NewString(), state, reason, true)
}
