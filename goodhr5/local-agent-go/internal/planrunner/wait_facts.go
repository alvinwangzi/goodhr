// 本文件从 HRPlus 原持久队列上传只读排队事实，确认后不重复发送，不改变实际排队顺序。
package planrunner

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"goodhr5/local-agent-go/internal/cloudapi"
	"goodhr5/local-agent-go/internal/localdb"
	"goodhr5/local-agent-go/internal/planmodel"
	"goodhr5/local-agent-go/internal/planoperations"
)

// SyncWaitingFacts 只使用原队列和当前核对配置，登录代次变化后不能确认到另一账号。
func (s *Scheduler) SyncWaitingFacts(ctx context.Context, plans []planmodel.Plan, machine string, a planoperations.Authority) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if a.StillCurrent == nil || !a.StillCurrent() {
		return ErrPlanAuthority
	}
	byID := map[string]planmodel.Plan{}
	for _, plan := range plans {
		if err := plan.Validate(); err != nil {
			return err
		}
		if cloudapi.SessionOwnerScope(s.coordinator.client.BaseURL, plan.UserEmail) != a.OwnerScope {
			return ErrPlanAuthority
		}
		if plan.State == "enabled" && !plan.StopRequested && plan.MachineID == machine {
			byID[plan.ID] = plan
		}
	}
	waiting, err := s.coordinator.db.WaitingPlanRequests(ctx, a.OwnerScope)
	if err != nil {
		return err
	}
	for _, request := range waiting {
		plan, ok := byID[request.PlanID]
		if !ok || plan.ActivationID != request.ActivationID {
			continue
		}
		queued, confirmed, err := s.coordinator.db.PlanWaitReceipt(ctx, a.OwnerScope, request.RequestID)
		if err != nil {
			return err
		}
		input := cloudapi.PlanWaitFact{PlanID: plan.ID, RequestID: request.RequestID, ActivationID: request.ActivationID, ConfigVersion: plan.Version, MachineID: machine, TriggeredAt: request.TriggeredAt.UTC(), QueuedAt: queued}
		raw, err := json.Marshal(input)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(raw)
		hash := hex.EncodeToString(sum[:])
		if confirmed != "" {
			if confirmed != hash {
				return localdb.ErrPlanRequestConflict
			}
			continue
		}
		if !a.StillCurrent() {
			return ErrPlanAuthority
		}
		if err = s.coordinator.client.RecordExecutionPlanWait(ctx, a.Token, input); err != nil {
			return err
		}
		confirm := func() error {
			if !a.StillCurrent() {
				return ErrPlanAuthority
			}
			return s.coordinator.db.ConfirmPlanWaitReceipt(ctx, request, hash)
		}
		// ConfirmCurrent 已持有会话锁，回调内不能再次调用 StillCurrent。
		if a.ConfirmCurrent != nil {
			err = a.ConfirmCurrent(func() error { return s.coordinator.db.ConfirmPlanWaitReceipt(ctx, request, hash) })
		} else {
			err = confirm()
		}
		if err != nil {
			return err
		}
	}
	return nil
}
