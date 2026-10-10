// 本文件协调 HRPlus 定时与立即开始的持久队列，复用原领取流程，不直接执行招聘页面动作。
package planrunner

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"goodhr5/local-agent-go/internal/cloudapi"
	"goodhr5/local-agent-go/internal/localdb"
	"goodhr5/local-agent-go/internal/planmodel"
	"goodhr5/local-agent-go/internal/planoperations"
	"sync"
)

// Scheduler 串行合并本进程触发；真正互斥继续由 Runner 本地预留及云端账号占用保证。
type Scheduler struct {
	mu          sync.Mutex
	coordinator *Coordinator
}

// NewScheduler 使用已有协调器与数据库，避免创建第二套启动或占用接口。
func NewScheduler(c *Coordinator) *Scheduler { return &Scheduler{coordinator: c} }

// scheduledRunID 固定同一计划启用批次与日期的运行身份，下一执行日产生新身份及初始数量。
func scheduledRunID(plan planmodel.Plan, date string) string {
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte("hrplus/run/"+plan.ID+"/"+plan.ActivationID+"/"+date)).String()
}

// EnqueueDue 在名义时段内登记一次原窗口触发，定时与立即开始共享编号和原定时间。
func (s *Scheduler) EnqueueDue(ctx context.Context, plans []planmodel.Plan, machine string, a planoperations.Authority) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if a.StillCurrent == nil || !a.StillCurrent() {
		return ErrPlanAuthority
	}
	for _, plan := range plans {
		if err := plan.Validate(); err != nil {
			return err
		}
		if cloudapi.SessionOwnerScope(s.coordinator.client.BaseURL, plan.UserEmail) != a.OwnerScope {
			return ErrPlanAuthority
		}
		if plan.MachineID != machine || plan.State != "enabled" || plan.StopRequested {
			continue
		}
		window, err := plan.Config.Schedule.Current(s.coordinator.now())
		if err != nil {
			return err
		}
		if window == nil {
			continue
		}
		run, err := s.coordinator.db.PlanRunSnapshot(ctx, a.OwnerScope, scheduledRunID(plan, window.Date))
		if err == nil && run.State != "waiting_window" && run.State != "pending" && run.State != "waiting_resource" {
			continue
		}
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		key := fmt.Sprintf("hrplus/trigger/%s/%s/%s/%d/%d", plan.ID, plan.ActivationID, window.Date, plan.Config.Schedule.Windows[window.Order].StartMinute, plan.Config.Schedule.Windows[window.Order].EndMinute)
		if !a.StillCurrent() {
			return ErrPlanAuthority
		}
		_, err = s.coordinator.db.EnqueuePlanRequest(ctx, localdb.PlanWaitingRequest{OwnerScope: a.OwnerScope, RequestID: uuid.NewSHA1(uuid.NameSpaceOID, []byte(key)).String(), PlanID: plan.ID, ActivationID: plan.ActivationID, TriggeredAt: window.Start})
		if err != nil {
			return err
		}
	}
	return nil
}

// ScheduledExecution 保留原等待记录及已领取父占用；调用方必须实际收尾，不能因队列写入失败丢弃占用。
type ScheduledExecution struct {
	Request localdb.PlanWaitingRequest
	Plan    planmodel.Plan
	Held    *Acquired
}

// AcquireNext 按稳定队列挑选当前可执行项，账号忙或不明确响应保留原请求及时间，不重新排队。
func (s *Scheduler) AcquireNext(ctx context.Context, plans []planmodel.Plan, machine string, a planoperations.Authority) (*ScheduledExecution, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if a.StillCurrent == nil || !a.StillCurrent() {
		return nil, ErrPlanAuthority
	}
	byID := map[string]planmodel.Plan{}
	for _, plan := range plans {
		if err := plan.Validate(); err != nil {
			return nil, err
		}
		if cloudapi.SessionOwnerScope(s.coordinator.client.BaseURL, plan.UserEmail) != a.OwnerScope {
			return nil, ErrPlanAuthority
		}
		if _, exists := byID[plan.ID]; exists {
			return nil, fmt.Errorf("计划列表包含重复编号")
		}
		byID[plan.ID] = plan
	}
	waiting, err := s.coordinator.db.WaitingPlanRequests(ctx, a.OwnerScope)
	if err != nil {
		return nil, err
	}
	for _, request := range waiting {
		if !a.StillCurrent() {
			return nil, ErrPlanAuthority
		}
		plan, exists := byID[request.PlanID]
		if !exists || plan.MachineID != machine || plan.State != "enabled" || plan.StopRequested || plan.ActivationID != request.ActivationID {
			if err := s.coordinator.db.TransitionPlanRequest(ctx, a.OwnerScope, request.RequestID, "waiting", "cancelled"); err != nil {
				return nil, err
			}
			continue
		}
		window, err := plan.Config.Schedule.Current(s.coordinator.now())
		if err != nil {
			return nil, err
		}
		if window == nil || request.TriggeredAt.In(window.Start.Location()).Format("2006-01-02") != window.Date {
			continue
		}
		runID := scheduledRunID(plan, window.Date)
		run, err := s.coordinator.db.PlanRunSnapshot(ctx, a.OwnerScope, runID)
		if err == nil && run.State != "waiting_window" && run.State != "pending" && run.State != "waiting_resource" {
			continue
		}
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
		claim, err := s.coordinator.requests.OriginalClaim(ctx, a.OwnerScope, request.RequestID)
		if errors.Is(err, sql.ErrNoRows) {
			var secret [32]byte
			if _, err = rand.Read(secret[:]); err != nil {
				return nil, err
			}
			claim = cloudapi.PlanClaimRequest{PlanID: plan.ID, ActivationID: plan.ActivationID, ExpectedVersion: plan.Version, ExecutionDate: window.Date, RunID: runID, RequestID: request.RequestID, MachineID: machine, OwnerID: uuid.NewString(), Credential: hex.EncodeToString(secret[:]), LocalReserved: true}
			clear(secret[:])
		} else if err != nil {
			return nil, err
		}
		held, err := s.coordinator.Acquire(ctx, plan, claim, a)
		if held == nil {
			return nil, err
		}
		result := &ScheduledExecution{Request: request, Plan: plan, Held: held}
		if err != nil {
			return result, err
		}
		if err := s.coordinator.db.TransitionPlanRequest(ctx, a.OwnerScope, request.RequestID, "waiting", "running"); err != nil {
			return result, err
		}
		return result, nil
	}
	return nil, nil
}

// ConfirmFinished 仅在原云端规范快照已确认收尾且本地引用已释放时完成队列记录。
func (s *Scheduler) ConfirmFinished(ctx context.Context, execution *ScheduledExecution) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if execution == nil || execution.Held == nil || execution.Held.Reservation != nil && execution.Held.Reservation.Valid() {
		return fmt.Errorf("原计划尚未释放本地执行权")
	}
	run, err := s.coordinator.db.PlanRunSnapshot(ctx, execution.Request.OwnerScope, execution.Held.Permit.Run.ID)
	if err != nil {
		return err
	}
	if run.OwnerID != execution.Held.Permit.Run.OwnerID || (run.State != "waiting_window" && run.State != "completed" && run.State != "incomplete" && run.State != "stopped" && run.State != "blocked") {
		return ErrPlanNeedsSettlement
	}
	return s.coordinator.db.TransitionPlanRequest(ctx, execution.Request.OwnerScope, execution.Request.RequestID, "running", "done")
}
