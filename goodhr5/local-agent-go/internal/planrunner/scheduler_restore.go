// 本文件在 HRPlus 冷启动时核对原运行并复用原领取请求，本地重新预留后才允许恢复页面。
package planrunner

import (
	"context"
	"goodhr5/local-agent-go/internal/localdb"
	"goodhr5/local-agent-go/internal/planmodel"
	"goodhr5/local-agent-go/internal/planoperations"
)

// restoreRunning 在当前名义时段和原日期内恢复原已领取项，未确认收尾或新批次不能借旧执行权启动。
func (s *Scheduler) restoreRunning(ctx context.Context, plans map[string]planmodel.Plan, machine string, a planoperations.Authority) (*ScheduledExecution, error) {
	requests, err := s.coordinator.db.RunningPlanRequests(ctx, a.OwnerScope)
	if err != nil {
		return nil, err
	}
	for _, request := range requests {
		if !a.StillCurrent() {
			return nil, ErrPlanAuthority
		}
		plan, exists := plans[request.PlanID]
		if !exists || plan.State != "enabled" || plan.StopRequested || plan.MachineID != machine || plan.ActivationID != request.ActivationID {
			continue
		}
		window, err := plan.Config.Schedule.Current(s.coordinator.now())
		if err != nil {
			return nil, err
		}
		if window == nil {
			continue
		}
		claim, err := s.coordinator.requests.OriginalClaim(ctx, a.OwnerScope, request.RequestID)
		if err != nil {
			return nil, err
		}
		if claim.ExecutionDate != window.Date || claim.PlanID != plan.ID || claim.ActivationID != plan.ActivationID || claim.ExpectedVersion != plan.Version || claim.MachineID != machine {
			continue
		}
		run, err := s.coordinator.db.PlanRunSnapshot(ctx, a.OwnerScope, claim.RunID)
		if err != nil {
			return nil, err
		}
		if run.State != "starting" && run.State != "running" {
			continue
		}
		if run.OwnerID != claim.OwnerID || run.PlanID != claim.PlanID || run.ActivationID != claim.ActivationID || run.ExecutionDate != claim.ExecutionDate || run.ConfigVersion != claim.ExpectedVersion {
			return nil, localdb.ErrPlanRequestConflict
		}
		pending, err := s.coordinator.db.PlanRecoveryPending(ctx, a.OwnerScope, run.ID)
		if err != nil {
			return nil, err
		}
		if pending {
			continue
		}
		held, err := s.coordinator.Acquire(ctx, plan, claim, a)
		if held == nil {
			return nil, err
		}
		return &ScheduledExecution{Request: request, Plan: plan, Held: held}, err
	}
	return nil, nil
}
