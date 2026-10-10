// 本文件将 HRPlus 执行计划接入本地后台生命周期，使用已核对登录、原排队与 M1 运行，不依赖网页常开。
package app

import (
	"context"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"goodhr5/local-agent-go/internal/cloudapi"
	"goodhr5/local-agent-go/internal/planoperations"
	"goodhr5/local-agent-go/internal/planrunner"
	"goodhr5/local-agent-go/internal/protectedsession"
	"log"
	"time"
)

// planExecution 保留真实父占用与旧登录归属，退出后必须等待执行协程再处理原收尾。
type planExecution struct {
	execution   *planrunner.ScheduledExecution
	coordinator *planrunner.Coordinator
	scheduler   *planrunner.Scheduler
	runtime     *planrunner.M1ExecutionRuntime
	identity    *protectedsession.Session
	version     uint64
	done        chan struct{}
	cancel      context.CancelFunc
	err         error
	staged      bool
}

// signalPlanExecutions 合并后台唤醒请求，登录成功、运行结束后不等待下一次定时检查。
func (s *Server) signalPlanExecutions() {
	select {
	case s.planExecutionWake <- struct{}{}:
	default:
	}
}

// planAuthority 绑定已提交登录代次，迟到回执不能确认到新登录账户。
func (s *Server) planAuthority(identity *protectedsession.Session, version uint64) planoperations.Authority {
	validLocked := func() bool {
		return s.sessionCurrent == identity && !s.sessionBlocked && s.sessionVersion == version && s.sessionCommittedVersion == version
	}
	a := planoperations.Authority{Token: identity.Token, OwnerScope: cloudapi.SessionOwnerScope(identity.CloudBase, identity.UserEmail)}
	a.StillCurrent = func() bool { s.sessionMu.Lock(); defer s.sessionMu.Unlock(); return validLocked() }
	a.ConfirmCurrent = func(confirm func() error) error {
		s.sessionMu.Lock()
		defer s.sessionMu.Unlock()
		if !validLocked() {
			return planrunner.ErrPlanAuthority
		}
		return confirm()
	}
	return a
}

// currentPlanSession 只读取已提交且未阻断的登录证明，不使用未验证的网页请求或本地缓存。
func (s *Server) currentPlanSession() (*protectedsession.Session, uint64) {
	s.sessionMu.Lock()
	defer s.sessionMu.Unlock()
	if s.sessionCurrent == nil || s.sessionBlocked || s.sessionVersion != s.sessionCommittedVersion {
		return nil, 0
	}
	return s.sessionCurrent, s.sessionVersion
}

// runPlanExecutions 每五秒检查计划，网页关闭仍运行；结束只等待原步骤退出，不能强制释放不明确页面。
func (s *Server) runPlanExecutions(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	defer s.shutdownPlanExecution(ctx)
	previous := ""
	for {
		if ctx.Err() != nil {
			return
		}
		err := s.processPlanExecutions(ctx)
		if err != nil && !errors.Is(err, context.Canceled) {
			if err.Error() != previous {
				log.Printf("[执行计划] 等待核对：%s", err.Error())
				previous = err.Error()
			}
		} else {
			previous = ""
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-s.planExecutionWake:
		}
	}
}

// processPlanExecutions 串行协调触发和收尾，实际长任务单独运行，当前任务期间仍能检查远程停止。
func (s *Server) processPlanExecutions(parent context.Context) error {
	s.planExecutionMu.Lock()
	defer s.planExecutionMu.Unlock()
	identity, version := s.currentPlanSession()
	active := s.activePlanExecution
	if active != nil {
		if identity != active.identity || version != active.version {
			active.cancel()
		}
		select {
		case <-active.done:
			return s.settlePlanExecution(parent, active, identity, version)
		default:
			if identity == nil || identity != active.identity || version != active.version {
				return nil
			}
		}
	}
	if identity == nil {
		return nil
	}
	a := s.planAuthority(identity, version)
	ctx, cancel := context.WithTimeout(parent, 15*time.Second)
	defer cancel()
	client := cloudapi.New(identity.CloudBase)
	plans, err := client.ListExecutionPlans(ctx, identity.Token)
	if err != nil {
		var expired cloudapi.AuthExpiredError
		if errors.As(err, &expired) {
			if active != nil {
				active.cancel()
			}
			s.invalidateExpiredPlanSession(version, identity)
		}
		return err
	}
	if !a.StillCurrent() {
		return planrunner.ErrPlanAuthority
	}
	if active != nil {
		allowed := false
		for _, plan := range plans {
			if plan.ID == active.execution.Plan.ID && plan.ActivationID == active.execution.Plan.ActivationID && plan.Version == active.execution.Plan.Version && plan.State == "enabled" && !plan.StopRequested && plan.MachineID == identity.MachineID {
				allowed = true
			}
		}
		if !allowed {
			active.cancel()
		}
		// 长任务期间仍登记其他计划的原定触发，不等当前任务结束才发现错过的窗口。
		return active.scheduler.EnqueueDue(ctx, plans, identity.MachineID, a)
	}
	coordinator := planrunner.New(s.db, s.runner, client, s.planNow)
	scheduler := planrunner.NewScheduler(coordinator)
	if err := scheduler.EnqueueDue(ctx, plans, identity.MachineID, a); err != nil {
		return err
	}
	// 长任务上下文属于本地服务，不继承网页请求或这次十五秒读取期限。
	execCtx, stop := context.WithCancel(parent)
	execution, err := scheduler.AcquireNext(execCtx, plans, identity.MachineID, a)
	if execution == nil {
		stop()
		var expired cloudapi.AuthExpiredError
		if errors.As(err, &expired) {
			s.invalidateExpiredPlanSession(version, identity)
		}
		return err
	}
	active = &planExecution{execution: execution, coordinator: coordinator, scheduler: scheduler, identity: identity, version: version, done: make(chan struct{}), cancel: stop, err: err}
	s.activePlanExecution = active
	if err != nil {
		close(active.done)
		s.signalPlanExecutions()
		return err
	}
	options := s.startOptionsFromPayload(map[string]any{"cloud_api_base": identity.CloudBase, "token": identity.Token}, identity.MachineID)
	active.runtime = planrunner.NewM1ExecutionRuntime(coordinator, execution.Held, a, options)
	go func() {
		defer func() {
			if recover() != nil {
				active.err = errors.New("执行计划运行意外中断，等待原步骤收尾")
			}
			close(active.done)
			s.signalPlanExecutions()
		}()
		_, active.err = planrunner.NewExecutionLoop(active.runtime, a.OwnerScope, s.planNow).Run(execCtx, execution.Held.Permit)
	}()
	log.Printf("[执行计划] 原领取已确认：计划=%s，运行=%s", execution.Plan.ID, execution.Held.Permit.Run.ID)
	return nil
}

// settlePlanExecution 只在执行协程退出后收尾；失败先保留原释放，规范云端确认后才完成排队记录。
func (s *Server) settlePlanExecution(parent context.Context, active *planExecution, identity *protectedsession.Session, version uint64) error {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(parent), 30*time.Second)
	defer cancel()
	var expired cloudapi.AuthExpiredError
	if errors.As(active.err, &expired) {
		s.invalidateExpiredPlanSession(active.version, active.identity)
	}
	if active.err != nil && !active.staged {
		state, reason := "blocked", "plan_execution_failed"
		if errors.Is(active.err, context.Canceled) || errors.Is(active.err, planrunner.ErrPlanAuthority) {
			state, reason = "stopped", "plan_authority_or_stop"
		}
		// 领取或准备跨过名义结束、但登录仍有效且没有外部取消时，按窗口等待处理。
		reservation := active.execution.Held.Reservation
		timeBoundary := errors.Is(active.err, planrunner.ErrOutsidePlanWindow) || errors.Is(active.err, planrunner.ErrPlanAuthority) && identity == active.identity && version == active.version && reservation != nil && reservation.Context().Err() == nil
		if timeBoundary {
			now := time.Now()
			if s.planNow != nil {
				now = s.planNow()
			}
			current, clockErr := active.execution.Plan.Config.Schedule.Current(now)
			if clockErr != nil {
				return clockErr
			}
			if current == nil {
				state, reason = "incomplete", "plan_window_closed_during_start"
				windows, clockErr := active.execution.Plan.Config.Schedule.OnDate(now)
				if clockErr != nil {
					return clockErr
				}
				for _, window := range windows {
					if window.Date == active.execution.Held.Permit.Run.ExecutionDate && window.Start.After(now) {
						state = "waiting_window"
						break
					}
				}
			}
		}
		var err error
		if active.runtime != nil {
			_, err = active.runtime.StageFailureRelease(ctx, state, reason)
			if err != nil && identity != nil && cloudapi.SessionOwnerScope(identity.CloudBase, identity.UserEmail) == active.execution.Request.OwnerScope && (active.execution.Plan.TenantID == "" || active.execution.Plan.TenantID == identity.TenantID) {
				a := s.planAuthority(identity, version)
				if refreshErr := active.runtime.UseSettlementAuthority(ctx, a); refreshErr == nil {
					_, err = active.runtime.StageFailureRelease(ctx, state, reason)
				}
			}
		} else {
			_, err = active.coordinator.ReleaseAfterCleanup(ctx, active.execution.Held, active.execution.Held.Permit.Run, uuid.NewString(), state, reason, true)
		}
		if err != nil {
			return err
		}
		active.staged = true
		s.signalPlanUploads()
	}
	if err := active.scheduler.ConfirmFinished(ctx, active.execution); err == nil {
		active.cancel()
		s.activePlanExecution = nil
		s.signalPlanExecutions()
		return nil
	} else if active.err == nil {
		return err
	}
	// 原释放已持久保存，后台补传仍属于原账号；其他账号只有本地引用确实释放后才能启动。
	if identity == nil || cloudapi.SessionOwnerScope(identity.CloudBase, identity.UserEmail) != active.execution.Request.OwnerScope {
		reservation := active.execution.Held.Reservation
		if reservation == nil || reservation.CleanupReleased() {
			active.cancel()
			s.activePlanExecution = nil
			return nil
		}
	}
	return fmt.Errorf("原计划已经收尾，等待原账号云端释放回执")
}

// shutdownPlanExecution 等原运行退出后保存收尾，超时仅记录保留占用，不能假称页面已清理。
func (s *Server) shutdownPlanExecution(parent context.Context) {
	s.planExecutionMu.Lock()
	defer s.planExecutionMu.Unlock()
	active := s.activePlanExecution
	if active == nil {
		return
	}
	active.cancel()
	timer := time.NewTimer(30 * time.Second)
	defer timer.Stop()
	select {
	case <-active.done:
		identity, version := s.currentPlanSession()
		if err := s.settlePlanExecution(parent, active, identity, version); err != nil {
			log.Printf("[执行计划] 关闭时原收尾待核对：%s", err.Error())
		}
	case <-timer.C:
		log.Printf("[执行计划] 关闭时原步骤尚未退出，保留原记录等待核对")
	}
}
