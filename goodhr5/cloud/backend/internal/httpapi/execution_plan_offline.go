// 本文件根据 HRPlus 原启用及停止回执补记完全离线的未执行日，不制造排队、任务、执行权或页面动作。
package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"sort"
	"time"
)

// offlinePlanPeriod 固定一次原启用的配置与实际允许期间，后续编辑不能改变原日的岗位。
type offlinePlanPeriod struct {
	plan  ExecutionPlan
	until *time.Time
}

// offlinePlanPeriods 从原成功意图回执恢复启用期间，不根据当前配置猜测过去的版本。
func offlinePlanPeriods(current ExecutionPlan, receipts []ExecutionPlan) ([]offlinePlanPeriod, error) {
	starts := map[string]ExecutionPlan{}
	ends := map[string]time.Time{}
	for _, p := range receipts {
		if p.ID != current.ID || p.UserEmail != current.UserEmail || p.TenantID != current.TenantID || p.ActivationID == "" {
			continue
		}
		if p.UpdatedAt.IsZero() {
			return nil, ErrExecutionPlanRequest
		}
		if p.State == "enabled" {
			if p.Version < 1 || p.MachineID == "" || !executionPlanUUID.MatchString(p.ActivationID) || p.StopRequested {
				return nil, ErrExecutionPlanRequest
			}
			if err := p.Config.Validate(); err != nil {
				return nil, err
			}
			if previous, ok := starts[p.ActivationID]; !ok || p.UpdatedAt.Before(previous.UpdatedAt) {
				starts[p.ActivationID] = cloneExecutionPlan(p)
			}
		} else if p.State == "stopped" {
			if previous, ok := ends[p.ActivationID]; !ok || p.UpdatedAt.Before(previous) {
				ends[p.ActivationID] = p.UpdatedAt
			}
		}
	}
	periods := []offlinePlanPeriod{}
	for activation, p := range starts {
		period := offlinePlanPeriod{plan: p}
		if end, ok := ends[activation]; ok {
			copy := end
			period.until = &copy
		}
		periods = append(periods, period)
	}
	sort.Slice(periods, func(i, j int) bool {
		if periods[i].plan.UpdatedAt.Equal(periods[j].plan.UpdatedAt) {
			return periods[i].plan.ActivationID < periods[j].plan.ActivationID
		}
		return periods[i].plan.UpdatedAt.Before(periods[j].plan.UpdatedAt)
	})
	return periods, nil
}

// offlineReportTriggers 返回已经超过最后名义时段及六分钟收尾、且原启用期间确有执行机会的缺失日期。
func offlineReportTriggers(ctx context.Context, period offlinePlanPeriod, now time.Time, existing map[string]bool, limit int) ([]time.Time, error) {
	p := period.plan
	loc, err := time.LoadLocation(p.Config.Schedule.Timezone)
	if err != nil {
		return nil, err
	}
	cutoff := now
	if period.until != nil && period.until.Before(cutoff) {
		cutoff = *period.until
	}
	armed := p.UpdatedAt.In(loc)
	day := time.Date(armed.Year(), armed.Month(), armed.Day(), 0, 0, 0, 0, loc)
	last := 0
	for _, window := range p.Config.Schedule.Windows {
		if window.EndMinute > last {
			last = window.EndMinute
		}
	}
	result := []time.Time{}
	for !day.After(cutoff.In(loc)) && len(result) < limit {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		date := day.Format("2006-01-02")
		ended := time.Date(day.Year(), day.Month(), day.Day(), last/60, last%60, 0, 0, loc).Add(6 * time.Minute)
		if runtimeDateAllowed(p.Config.Schedule, day) && !cutoff.Before(ended) && !existing[p.ActivationID+"/"+date] {
			for _, window := range p.Config.Schedule.Windows {
				end := time.Date(day.Year(), day.Month(), day.Day(), window.EndMinute/60, window.EndMinute%60, 0, 0, loc)
				if end.After(p.UpdatedAt) {
					result = append(result, time.Date(day.Year(), day.Month(), day.Day(), window.StartMinute/60, window.StartMinute%60, 0, 0, loc))
					break
				}
			}
		}
		day = day.AddDate(0, 0, 1)
	}
	return result, nil
}

// offlineReport 使用已确认启用事实产生零动作摘要；下次名义安排只取仍启用的同一原批次。
func offlineReport(current ExecutionPlan, period offlinePlanPeriod, trigger, now time.Time) (ExecutionPlanRun, ExecutionPlanReportSummary, error) {
	p := period.plan
	wait := ExecutionPlanWait{PlanID: p.ID, ActivationID: p.ActivationID, ConfigVersion: p.Version, MachineID: p.MachineID, TriggeredAt: trigger}
	run, summary, due, err := unstartedReport(wait, p.Config, now)
	if err != nil {
		return run, summary, err
	}
	if !due {
		return run, summary, ErrExecutionPlanRequest
	}
	if current.State == "enabled" && !current.StopRequested && current.ActivationID == p.ActivationID && current.Version == p.Version {
		view, err := buildPlanRuntimeView(ExecutionPlanRuntimeSnapshot{Plan: current, Runs: []ExecutionPlanRun{run}}, now)
		if err != nil {
			return run, summary, err
		}
		summary.NextNominalAt = view.NominalAt
	}
	return run, summary, nil
}

// saveUnstartedReportLocked 保存一次原日摘要并唤醒原账号通知，调用方必须持有计划存储锁。
func (s *MemoryExecutionPlanStore) saveUnstartedReportLocked(run ExecutionPlanRun, summary ExecutionPlanReportSummary, now time.Time) error {
	if _, collision := s.runs[run.ID]; collision {
		return ErrExecutionPlanRequest
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
		p := s.plans[run.PlanID]
		s.reportNotifyChanged(p.TenantID, p.UserEmail)
	}
	return nil
}

// settleOfflineArmsLocked 在与实际领取相同的锁内核对全部原运行，不覆盖任何已领取或已结束的日。
func (s *MemoryExecutionPlanStore) settleOfflineArmsLocked(ctx context.Context, now time.Time, limit int) (int, error) {
	receipts := []ExecutionPlan{}
	for _, receipt := range s.intents {
		receipts = append(receipts, receipt.Result)
	}
	ids := []string{}
	for id := range s.plans {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	count := 0
	for _, id := range ids {
		current := s.plans[id]
		periods, err := offlinePlanPeriods(current, receipts)
		if err != nil {
			return count, err
		}
		existing := map[string]bool{}
		for _, run := range s.runs {
			if run.PlanID == id {
				existing[run.ActivationID+"/"+run.ExecutionDate] = true
			}
		}
		for _, period := range periods {
			triggers, err := offlineReportTriggers(ctx, period, now, existing, limit-count)
			if err != nil {
				return count, err
			}
			for _, trigger := range triggers {
				if err := ctx.Err(); err != nil {
					return count, err
				}
				run, summary, err := offlineReport(current, period, trigger, now)
				if err != nil {
					return count, err
				}
				if err := s.saveUnstartedReportLocked(run, summary, now); err != nil {
					return count, err
				}
				count++
				if count >= limit {
					return count, nil
				}
			}
		}
	}
	return count, nil
}

// insertUnstartedReportTx 原父运行、零动作项和报告同事务保存，没有账号占用或 TaskRun 写入。
func insertUnstartedReportTx(ctx context.Context, tx *sql.Tx, run ExecutionPlanRun, summary ExecutionPlanReportSummary) error {
	configRaw, _ := json.Marshal(run.Snapshot)
	if _, err := tx.ExecContext(ctx, `INSERT INTO execution_plan_runs(id,plan_id,activation_id,execution_date,config_version,snapshot,state,owner_id,finished_at,end_reason) VALUES($1,$2,$3,$4,$5,$6,'incomplete',NULL,$7,'plan_never_started')`, run.ID, run.PlanID, run.ActivationID, run.ExecutionDate, run.ConfigVersion, string(configRaw), run.FinishedAt); err != nil {
		return err
	}
	if err := insertPlanItemRuns(ctx, tx, run.ID, run.Items); err != nil {
		return err
	}
	hash, body, err := reportHash(summary)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO execution_plan_reports(run_id,summary,body_hash,sync_state,notification_state) VALUES($1,$2,$3,'confirmed','pending')`, run.ID, string(body), hash)
	return err
}

// settleOfflineArms 只选择已有原启用回执的计划，逐计划事务锁与真实领取互斥，按实际新增数量限制批次。
func (s *PostgresExecutionPlanStore) settleOfflineArms(ctx context.Context, now time.Time, limit int) (int, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT plan_id::text FROM execution_plan_requests WHERE kind='arm' ORDER BY plan_id::text`)
	if err != nil {
		return 0, err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return 0, err
	}
	count := 0
	for _, id := range ids {
		added, err := s.settleOfflinePlan(ctx, id, now, limit-count)
		if err != nil {
			return count, err
		}
		count += added
		if count >= limit {
			break
		}
	}
	return count, nil
}

// settleOfflinePlan 在原计划行锁内读取冻结回执及所有日期，停止、编辑、软删除也不改写已经允许的历史日。
func (s *PostgresExecutionPlanStore) settleOfflinePlan(ctx context.Context, id string, now time.Time, limit int) (int, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	current, err := scanExecutionPlan(tx.QueryRowContext(ctx, `SELECT `+executionPlanColumns+` FROM execution_plans WHERE id=$1 FOR UPDATE`, id))
	if err != nil {
		return 0, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT result FROM execution_plan_requests WHERE plan_id=$1 AND kind IN ('arm','stop')`, id)
	if err != nil {
		return 0, err
	}
	receipts := []ExecutionPlan{}
	for rows.Next() {
		var raw []byte
		if err = rows.Scan(&raw); err != nil {
			rows.Close()
			return 0, err
		}
		var p ExecutionPlan
		if err = json.Unmarshal(raw, &p); err != nil {
			rows.Close()
			return 0, err
		}
		receipts = append(receipts, p)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return 0, err
	}
	periods, err := offlinePlanPeriods(current, receipts)
	if err != nil {
		return 0, err
	}
	rows, err = tx.QueryContext(ctx, `SELECT activation_id::text,execution_date::text FROM execution_plan_runs WHERE plan_id=$1`, id)
	if err != nil {
		return 0, err
	}
	existing := map[string]bool{}
	for rows.Next() {
		var activation, date string
		if err = rows.Scan(&activation, &date); err != nil {
			rows.Close()
			return 0, err
		}
		existing[activation+"/"+date] = true
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return 0, err
	}
	count := 0
	for _, period := range periods {
		triggers, err := offlineReportTriggers(ctx, period, now, existing, limit-count)
		if err != nil {
			return 0, err
		}
		for _, trigger := range triggers {
			run, summary, err := offlineReport(current, period, trigger, now)
			if err != nil {
				return 0, err
			}
			if err = insertUnstartedReportTx(ctx, tx, run, summary); err != nil {
				return 0, err
			}
			count++
		}
		if count >= limit {
			break
		}
	}
	if err = tx.Commit(); err != nil {
		return 0, err
	}
	return count, nil
}
