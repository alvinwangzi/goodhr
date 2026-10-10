// 本文件只结算 HRPlus 已持久排队却没有原运行的过期日，生成零动作报告，不生成 TaskRun 或账号执行权。
package httpapi

import (
	"context"
	"crypto/sha1"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// scheduledReportRunID 复用本地 UUID v5 OID 规则，原计划、批次和执行日产生同一记录身份。
func scheduledReportRunID(plan, activation, date string) string {
	namespace := []byte{0x6b, 0xa7, 0xb8, 0x12, 0x9d, 0xad, 0x11, 0xd1, 0x80, 0xb4, 0x00, 0xc0, 0x4f, 0xd4, 0x30, 0xc8}
	hash := sha1.New()
	_, _ = hash.Write(namespace)
	_, _ = hash.Write([]byte("hrplus/run/" + plan + "/" + activation + "/" + date))
	sum := hash.Sum(nil)[:16]
	sum[6] = (sum[6] & 15) | 0x50
	sum[8] = (sum[8] & 63) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", sum[:4], sum[4:6], sum[6:8], sum[8:10], sum[10:])
}

// unstartedReport 使用原时区窗口及冻结配置，日末六分钟后核对无运行再记账，不把生成时间当开始时间。
func unstartedReport(wait ExecutionPlanWait, config ExecutionPlanConfig, now time.Time) (ExecutionPlanRun, ExecutionPlanReportSummary, bool, error) {
	if err := config.Validate(); err != nil {
		return ExecutionPlanRun{}, ExecutionPlanReportSummary{}, false, err
	}
	loc, _ := time.LoadLocation(config.Schedule.Timezone)
	day := wait.TriggeredAt.In(loc)
	last := 0
	for _, window := range config.Schedule.Windows {
		if window.EndMinute > last {
			last = window.EndMinute
		}
	}
	ended := time.Date(day.Year(), day.Month(), day.Day(), last/60, last%60, 0, 0, loc).Add(6 * time.Minute).UTC()
	if now.Before(ended) {
		return ExecutionPlanRun{}, ExecutionPlanReportSummary{}, false, nil
	}
	run := ExecutionPlanRun{ID: scheduledReportRunID(wait.PlanID, wait.ActivationID, day.Format("2006-01-02")), PlanID: wait.PlanID, ActivationID: wait.ActivationID, ExecutionDate: day.Format("2006-01-02"), ConfigVersion: wait.ConfigVersion, Sequence: 1, State: "incomplete", Snapshot: config, EndReason: "plan_never_started", FinishedAt: &ended}
	var err error
	run.Items, err = initialPlanItemRuns(config)
	if err != nil {
		return run, ExecutionPlanReportSummary{}, false, err
	}
	summary := ExecutionPlanReportSummary{SchemaVersion: 1, RunID: run.ID, PlanID: run.PlanID, ActivationID: run.ActivationID, ConfigVersion: run.ConfigVersion, RunSequence: run.Sequence, ExecutionDate: run.ExecutionDate, PlanName: config.Name, Kind: "day_incomplete", RunState: "incomplete", EndReason: run.EndReason, SyncState: "confirmed", GeneratedAt: now.UTC(), FinishedAt: &ended, Items: []ExecutionReportItem{}, UnfinishedItemIDs: []string{}}
	for _, item := range run.Items {
		row := ExecutionReportItem{ID: item.ID, ItemID: item.ItemID, PositionID: item.Snapshot.PositionID, Order: item.Order, State: "pending", DetailsAvailable: true, Actions: map[string]ExecutionReportAction{}, Information: map[string]ExecutionReportAction{}}
		for _, action := range item.Snapshot.Actions {
			row.Actions[action] = ExecutionReportAction{State: "pending"}
		}
		summary.Items = append(summary.Items, row)
		summary.UnfinishedItemIDs = append(summary.UnfinishedItemIDs, item.ID)
	}
	return run, summary, true, summary.validateAgainst(run)
}

// SettleUnstartedWaits 在与领取相同的计划锁下核对原日期，已有任何原运行时不另造未开始结果。
func (s *MemoryExecutionPlanStore) SettleUnstartedWaits(ctx context.Context, now time.Time, limit int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if limit < 1 || limit > 100 {
		return ErrExecutionPlanRequest
	}
	count := 0
	for key, wait := range s.waits {
		config, exists := s.waitSnapshots[key]
		if !exists {
			continue
		}
		run, summary, due, err := unstartedReport(wait, config, now)
		if err != nil {
			return err
		}
		if !due {
			continue
		}
		found := false
		for _, existing := range s.runs {
			if existing.PlanID == run.PlanID && existing.ActivationID == run.ActivationID && existing.ExecutionDate == run.ExecutionDate {
				found = true
			}
		}
		if found {
			continue
		}
		if _, collision := s.runs[run.ID]; collision {
			return ErrExecutionPlanRequest
		}
		if plan := s.plans[run.PlanID]; plan.State == "enabled" && !plan.StopRequested && plan.ActivationID == run.ActivationID && plan.Version == run.ConfigVersion {
			view, err := buildPlanRuntimeView(ExecutionPlanRuntimeSnapshot{Plan: plan, Runs: []ExecutionPlanRun{run}}, now)
			if err != nil {
				return err
			}
			summary.NextNominalAt = view.NominalAt
		}
		hash, _, err := reportHash(summary)
		if err != nil {
			return err
		}
		s.runs[run.ID] = run
		if s.reports == nil {
			s.reports = map[string]ExecutionPlanReport{}
		}
		s.reports[run.ID] = ExecutionPlanReport{RunID: run.ID, BodyHash: hash, Summary: summary, SyncState: "confirmed", NotificationState: "pending", CreatedAt: now.UTC(), UpdatedAt: now.UTC()}
		if s.reportNotifyChanged != nil {
			plan := s.plans[run.PlanID]
			s.reportNotifyChanged(plan.TenantID, plan.UserEmail)
		}
		count++
		if count >= limit {
			break
		}
	}
	return nil
}

// SettleUnstartedWaits 原运行、独立项和报告同事务提交，领取竞态共享原计划锁，重复检查不改原报告。
func (s *PostgresExecutionPlanStore) SettleUnstartedWaits(ctx context.Context, now time.Time, limit int) error {
	if limit < 1 || limit > 100 {
		return ErrExecutionPlanRequest
	}
	rows, err := s.db.QueryContext(ctx, `SELECT w.plan_id::text,w.request_id::text FROM execution_plan_waits w JOIN execution_plan_wait_snapshots f ON f.plan_id=w.plan_id AND f.request_id=w.request_id WHERE w.triggered_at<$1 AND NOT EXISTS(SELECT 1 FROM execution_plan_runs r WHERE r.plan_id=w.plan_id AND r.activation_id=w.activation_id AND r.execution_date=f.execution_date) ORDER BY w.triggered_at,w.request_id LIMIT $2`, now, limit)
	if err != nil {
		return err
	}
	type key struct{ plan, request string }
	keys := []key{}
	for rows.Next() {
		var item key
		if err := rows.Scan(&item.plan, &item.request); err != nil {
			rows.Close()
			return err
		}
		keys = append(keys, item)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, item := range keys {
		if err := s.settleUnstartedWait(ctx, item.plan, item.request, now); err != nil {
			return err
		}
	}
	return nil
}

// settleUnstartedWait 锁原计划后重新读取冻结日期及任何原运行，不能覆盖已领取的动作和计数。
func (s *PostgresExecutionPlanStore) settleUnstartedWait(ctx context.Context, planID, requestID string, now time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	plan, err := scanExecutionPlan(tx.QueryRowContext(ctx, `SELECT `+executionPlanColumns+` FROM execution_plans WHERE id=$1 FOR UPDATE`, planID))
	if err != nil {
		return err
	}
	var wait ExecutionPlanWait
	var raw []byte
	err = tx.QueryRowContext(ctx, `SELECT w.plan_id::text,w.request_id::text,w.activation_id::text,w.config_version,w.machine_id,w.triggered_at,w.queued_at,f.snapshot FROM execution_plan_waits w JOIN execution_plan_wait_snapshots f ON f.plan_id=w.plan_id AND f.request_id=w.request_id WHERE w.plan_id=$1 AND w.request_id=$2`, planID, requestID).Scan(&wait.PlanID, &wait.RequestID, &wait.ActivationID, &wait.ConfigVersion, &wait.MachineID, &wait.TriggeredAt, &wait.QueuedAt, &raw)
	if err != nil {
		return err
	}
	var config ExecutionPlanConfig
	if err = json.Unmarshal(raw, &config); err != nil {
		return err
	}
	run, summary, due, err := unstartedReport(wait, config, now)
	if err != nil || !due {
		return err
	}
	if plan.State == "enabled" && !plan.StopRequested && plan.ActivationID == run.ActivationID && plan.Version == run.ConfigVersion {
		view, err := buildPlanRuntimeView(ExecutionPlanRuntimeSnapshot{Plan: plan, Runs: []ExecutionPlanRun{run}}, now)
		if err != nil {
			return err
		}
		summary.NextNominalAt = view.NominalAt
	}
	var existing string
	err = tx.QueryRowContext(ctx, `SELECT id::text FROM execution_plan_runs WHERE plan_id=$1 AND activation_id=$2 AND execution_date=$3`, run.PlanID, run.ActivationID, run.ExecutionDate).Scan(&existing)
	if err == nil {
		return tx.Commit()
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	configRaw, _ := json.Marshal(config)
	_, err = tx.ExecContext(ctx, `INSERT INTO execution_plan_runs(id,plan_id,activation_id,execution_date,config_version,snapshot,state,owner_id,finished_at,end_reason) VALUES($1,$2,$3,$4,$5,$6,'incomplete',NULL,$7,'plan_never_started')`, run.ID, run.PlanID, run.ActivationID, run.ExecutionDate, run.ConfigVersion, string(configRaw), run.FinishedAt)
	if err != nil {
		return err
	}
	if err = insertPlanItemRuns(ctx, tx, run.ID, run.Items); err != nil {
		return err
	}
	hash, body, err := reportHash(summary)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO execution_plan_reports(run_id,summary,body_hash,sync_state,notification_state) VALUES($1,$2,$3,'confirmed','pending')`, run.ID, string(body), hash)
	if err != nil {
		return err
	}
	return tx.Commit()
}
