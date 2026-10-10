// 本文件恢复 HRPlus 原报告通知，确定未发的记录可补发，发送中断只标未知，不自动重发。
package httpapi

import (
	"context"
	"database/sql"
	"log"
	"sort"
	"time"
)

// ExecutionPlanNotificationWork 保留原报告接收作用域，不接受网页传入新的邮箱。
type ExecutionPlanNotificationWork struct{ TenantID, UserEmail, RunID string }

// ReportNotificationWork 将过期发送标为未知，再挑选确定尚未发送的原报告。
func (s *MemoryExecutionPlanStore) ReportNotificationWork(ctx context.Context, expireBefore time.Time, ready bool, limit int) ([]ExecutionPlanNotificationWork, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if limit < 1 || limit > 100 {
		return nil, ErrExecutionPlanRequest
	}
	type candidate struct {
		work ExecutionPlanNotificationWork
		at   time.Time
	}
	var candidates []candidate
	for id, report := range s.reports {
		run, exists := s.runs[id]
		plan, owned := s.plans[run.PlanID]
		if !exists || !owned {
			continue
		}
		if report.NotificationState == "sending" && (report.NotificationStartedAt == nil || report.NotificationStartedAt.Before(expireBefore)) {
			report.NotificationState = "unknown"
			report.NotificationError = "发送过程未完成确认，结果待核对，未自动重发"
			report.UpdatedAt = time.Now().UTC()
			s.reports[id] = report
			if s.reportNotifyChanged != nil {
				s.reportNotifyChanged(plan.TenantID, plan.UserEmail)
			}
		}
		if report.NotificationState == "pending" || ready && report.NotificationState == "not_configured" {
			candidates = append(candidates, candidate{ExecutionPlanNotificationWork{plan.TenantID, plan.UserEmail, id}, report.CreatedAt})
		}
	}
	sort.Slice(candidates, func(i, j int) bool {
		if !candidates[i].at.Equal(candidates[j].at) {
			return candidates[i].at.Before(candidates[j].at)
		}
		return candidates[i].work.RunID < candidates[j].work.RunID
	})
	if len(candidates) > limit {
		candidates = candidates[:limit]
	}
	result := []ExecutionPlanNotificationWork{}
	for _, item := range candidates {
		result = append(result, item.work)
	}
	return result, nil
}

// ReportNotificationWork 使用事务恢复原发送账本，旧开始时间未知也不领取成新的发送。
func (s *PostgresExecutionPlanStore) ReportNotificationWork(ctx context.Context, expireBefore time.Time, ready bool, limit int) ([]ExecutionPlanNotificationWork, error) {
	if limit < 1 || limit > 100 {
		return nil, ErrExecutionPlanRequest
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, `UPDATE execution_plan_reports SET notification_state='unknown',notification_error='发送过程未完成确认，结果待核对，未自动重发',updated_at=NOW() WHERE notification_state='sending' AND (notification_started_at IS NULL OR notification_started_at<$1)`, expireBefore)
	if err != nil {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT COALESCE(p.tenant_id::text,''),p.user_email,q.run_id::text FROM execution_plan_reports q JOIN execution_plan_runs r ON r.id=q.run_id JOIN execution_plans p ON p.id=r.plan_id WHERE q.notification_state='pending' OR ($1 AND q.notification_state='not_configured') ORDER BY q.created_at,q.run_id LIMIT $2`, ready, limit)
	if err != nil {
		return nil, err
	}
	result := []ExecutionPlanNotificationWork{}
	for rows.Next() {
		var item ExecutionPlanNotificationWork
		if err = rows.Scan(&item.TenantID, &item.UserEmail, &item.RunID); err != nil {
			rows.Close()
			return nil, err
		}
		result = append(result, item)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	return result, tx.Commit()
}

// processReportNotifications 每轮只处理有限原记录，成功、未知和未配置仍是不同结论。
func (s *ExecutionPlanService) processReportNotifications(ctx context.Context, now time.Time) error {
	if err := s.store.SettleUnstartedWaits(ctx, now, 20); err != nil {
		return err
	}
	ready := s.execution != nil && reportMailerReady(s.execution.mailer)
	work, err := s.store.ReportNotificationWork(ctx, now.Add(-5*time.Minute), ready, 20)
	if err != nil {
		return err
	}
	for _, item := range work {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := s.notifyExecutionReport(ctx, item.TenantID, item.UserEmail, item.RunID); err != nil {
			return err
		}
		if s.eventDSN == "" && s.events != nil {
			s.events.publish(planEventScope(item.TenantID, item.UserEmail))
		}
	}
	return nil
}

// RunExecutionReportNotifications 随云端生命周期恢复确定未发的通知，不依赖用户再次打开报告。
func (s *Server) RunExecutionReportNotifications(ctx context.Context) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			return
		}
		if err := s.executionPlans.processReportNotifications(ctx, time.Now()); err != nil && ctx.Err() == nil {
			log.Printf("[执行计划报告] 通知等待恢复：%v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
