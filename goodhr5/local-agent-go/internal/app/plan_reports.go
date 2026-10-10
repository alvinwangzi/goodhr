// 本文件从 HRPlus 已核对结束运行保存原报告，离线历史上线后补生成，不改变计划启用状态。
package app

import (
	"context"
	"database/sql"
	"errors"
	"goodhr5/local-agent-go/internal/planmodel"
	"goodhr5/local-agent-go/internal/planoperations"
	"time"
)

// saveEndedPlanReports 为已结束原运行保存一份摘要；后续只更新同步状态，不重新生成原内容。
func (s *Server) saveEndedPlanReports(ctx context.Context, plans []planmodel.Plan, a planoperations.Authority) error {
	runs, err := s.db.PlanRunSnapshots(ctx, a.OwnerScope)
	if err != nil {
		return err
	}
	for _, run := range runs {
		if !a.StillCurrent() {
			return errors.New("报告保存登录证明已变化")
		}
		switch run.State {
		case "completed", "incomplete", "stopped", "blocked":
		default:
			continue
		}
		if original, err := s.db.PlanReportSnapshot(ctx, a.OwnerScope, run.ID); err == nil {
			pending, err := s.db.PlanRecoveryPending(ctx, a.OwnerScope, run.ID)
			if err != nil {
				return err
			}
			state := "confirmed"
			if pending {
				state = "pending"
			}
			if state != original.SyncState {
				if err := s.db.SetPlanReportSync(ctx, a.OwnerScope, run.ID, original.BodyHash, state); err != nil {
					return err
				}
			}
			continue
		} else if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		enabled := false
		for _, plan := range plans {
			if plan.ID == run.PlanID && plan.State == "enabled" && !plan.StopRequested {
				enabled = true
			}
		}
		now := time.Now()
		if s.planNow != nil {
			now = s.planNow()
		}
		report, err := s.db.BuildPlanReport(ctx, a.OwnerScope, run, now, enabled)
		if err != nil {
			return err
		}
		if a.ConfirmCurrent != nil {
			err = a.ConfirmCurrent(func() error { _, err := s.db.SavePlanReportSnapshot(ctx, a.OwnerScope, report); return err })
		} else {
			if !a.StillCurrent() {
				return errors.New("报告保存登录证明已变化")
			}
			_, err = s.db.SavePlanReportSnapshot(ctx, a.OwnerScope, report)
		}
		if err != nil {
			return err
		}
	}
	return nil
}
