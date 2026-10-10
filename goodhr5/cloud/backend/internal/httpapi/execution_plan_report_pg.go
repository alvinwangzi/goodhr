// 本文件使用 PostgreSQL 原报告行锁保存 HRPlus 摘要和同步状态，不执行页面或修改计划启用。
package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

const executionReportColumns = `run_id::text,body_hash,summary,sync_state,notification_state,created_at,updated_at,notification_recipient,notification_error,notification_token`

// scanExecutionReport 统一读取原摘要，不把数据库正文作为执行指令。
func scanExecutionReport(row interface{ Scan(...any) error }) (ExecutionPlanReport, error) {
	var r ExecutionPlanReport
	var raw []byte
	err := row.Scan(&r.RunID, &r.BodyHash, &raw, &r.SyncState, &r.NotificationState, &r.CreatedAt, &r.UpdatedAt, &r.NotificationRecipient, &r.NotificationError, &r.NotificationToken)
	if errors.Is(err, sql.ErrNoRows) {
		return r, ErrNotFound
	}
	if err != nil {
		return r, err
	}
	if err := json.Unmarshal(raw, &r.Summary); err != nil {
		return r, err
	}
	hash, _, err := reportHash(r.Summary)
	if err != nil {
		return r, err
	}
	if hash != r.BodyHash || r.Summary.RunID != r.RunID {
		return r, ErrExecutionPlanRequest
	}
	return r, nil
}

// SaveReport 以原运行行锁串行保存，重复正文相同则只更新同步状态，原报告及通知状态不变。
func (s *PostgresExecutionPlanStore) SaveReport(ctx context.Context, tenant, email string, summary ExecutionPlanReportSummary, syncState string) (ExecutionPlanReport, error) {
	if syncState != "pending" && syncState != "confirmed" {
		return ExecutionPlanReport{}, ErrExecutionPlanRequest
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ExecutionPlanReport{}, err
	}
	defer tx.Rollback()
	var planID string
	err = tx.QueryRowContext(ctx, `SELECT r.plan_id FROM execution_plan_runs r JOIN execution_plans p ON p.id=r.plan_id WHERE r.id=$1 AND p.user_email=$2 AND COALESCE(p.tenant_id::text,'')=$3 FOR UPDATE OF r`, summary.RunID, email, tenant).Scan(&planID)
	if errors.Is(err, sql.ErrNoRows) {
		return ExecutionPlanReport{}, ErrNotFound
	}
	if err != nil {
		return ExecutionPlanReport{}, err
	}
	run, err := scanPlanRun(tx.QueryRowContext(ctx, `SELECT `+executionPlanRunColumns+` FROM execution_plan_runs WHERE id=$1`, summary.RunID))
	if err != nil {
		return ExecutionPlanReport{}, err
	}
	run, err = loadPlanItemRuns(ctx, tx, run)
	if err != nil {
		return ExecutionPlanReport{}, err
	}
	if syncState == "confirmed" && activeExecutionPlanState(run.State) {
		return ExecutionPlanReport{}, ErrExecutionPlanRequest
	}
	hash, raw, err := reportHash(summary)
	if err != nil {
		return ExecutionPlanReport{}, err
	}
	old, err := scanExecutionReport(tx.QueryRowContext(ctx, `SELECT `+executionReportColumns+` FROM execution_plan_reports WHERE run_id=$1 FOR UPDATE`, run.ID))
	if err == nil {
		if old.BodyHash != hash {
			return ExecutionPlanReport{}, ErrExecutionPlanRequest
		}
		_, err = tx.ExecContext(ctx, `UPDATE execution_plan_reports SET sync_state=CASE WHEN sync_state='confirmed' THEN sync_state ELSE $2 END,updated_at=NOW() WHERE run_id=$1`, run.ID, syncState)
	} else if errors.Is(err, ErrNotFound) {
		if err := summary.validateAgainst(run); err != nil {
			return ExecutionPlanReport{}, err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO execution_plan_reports(run_id,summary,body_hash,sync_state) VALUES($1,$2,$3,$4)`, run.ID, string(raw), hash, syncState)
	}
	if err != nil {
		return ExecutionPlanReport{}, err
	}
	result, err := scanExecutionReport(tx.QueryRowContext(ctx, `SELECT `+executionReportColumns+` FROM execution_plan_reports WHERE run_id=$1`, run.ID))
	if err != nil {
		return result, err
	}
	return result, tx.Commit()
}

// GetReport 从计划所有者与团队过滤历史报告，不依赖设备在线状态授权读取。
func (s *PostgresExecutionPlanStore) GetReport(ctx context.Context, tenant, email, id string) (ExecutionPlanReport, error) {
	return scanExecutionReport(s.db.QueryRowContext(ctx, `SELECT q.run_id::text,q.body_hash,q.summary,q.sync_state,q.notification_state,q.created_at,q.updated_at,q.notification_recipient,q.notification_error,q.notification_token FROM execution_plan_reports q JOIN execution_plan_runs r ON r.id=q.run_id JOIN execution_plans p ON p.id=r.plan_id WHERE q.run_id=$1 AND p.user_email=$2 AND COALESCE(p.tenant_id::text,'')=$3`, id, email, tenant))
}

// ClaimReportNotification 原子固定原所有者邮箱及本次发送编号，同一报告并发只领取一次。
func (s *PostgresExecutionPlanStore) ClaimReportNotification(ctx context.Context, tenant, email, id, token string) (ExecutionPlanReport, bool, error) {
	if !executionPlanUUID.MatchString(token) {
		return ExecutionPlanReport{}, false, ErrExecutionPlanRequest
	}
	result, err := s.db.ExecContext(ctx, `UPDATE execution_plan_reports q SET notification_state='sending',notification_recipient=p.user_email,notification_token=$4,attempts=attempts+1,updated_at=NOW() FROM execution_plan_runs r JOIN execution_plans p ON p.id=r.plan_id WHERE q.run_id=r.id AND q.run_id=$1 AND p.user_email=$2 AND COALESCE(p.tenant_id::text,'')=$3 AND q.notification_state='pending'`, id, email, tenant, token)
	if err != nil {
		return ExecutionPlanReport{}, false, err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return ExecutionPlanReport{}, false, err
	}
	r, err := s.GetReport(ctx, tenant, email, id)
	return r, n == 1, err
}

// FinishReportNotification 以原发送编号结算，不自动重试发送不明的邮件。
func (s *PostgresExecutionPlanStore) FinishReportNotification(ctx context.Context, tenant, email, id, token, state, reason string) error {
	if state != "sent" && state != "unknown" && state != "not_configured" {
		return ErrExecutionPlanRequest
	}
	result, err := s.db.ExecContext(ctx, `UPDATE execution_plan_reports q SET notification_state=$5,notification_error=$6,updated_at=NOW() FROM execution_plan_runs r JOIN execution_plans p ON p.id=r.plan_id WHERE q.run_id=r.id AND q.run_id=$1 AND p.user_email=$2 AND COALESCE(p.tenant_id::text,'')=$3 AND q.notification_token=$4 AND q.notification_state='sending'`, id, email, tenant, token, state, reason)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrExecutionPlanRequest
	}
	return nil
}
