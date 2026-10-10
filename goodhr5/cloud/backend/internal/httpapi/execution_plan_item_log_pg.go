// 本文件以 PostgreSQL 事务保存 HRPlus 原日志及幂等摘要，读取按原账号和执行项分页。
package httpapi

import (
	"context"
	"database/sql"
	"errors"
	"sort"
)

// AppendItemLogs 整批核对和插入原日志，相同原编号的修改与另一 TaskRun 均回滚。
func (s *PostgresExecutionPlanStore) AppendItemLogs(ctx context.Context, tenant, email, runID, itemID, machine string, logs []ExecutionPlanItemLog) error {
	if err := s.VerifyReportMachine(ctx, tenant, email, runID, machine); err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var planID string
	err = tx.QueryRowContext(ctx, `SELECT r.plan_id::text FROM execution_plan_runs r JOIN execution_plans p ON p.id=r.plan_id WHERE r.id=$1 AND p.user_email=$2 AND COALESCE(p.tenant_id::text,'')=$3 FOR UPDATE OF r`, runID, email, tenant).Scan(&planID)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	run, err := scanPlanRun(tx.QueryRowContext(ctx, `SELECT `+executionPlanRunColumns+` FROM execution_plan_runs WHERE id=$1`, runID))
	if err != nil {
		return err
	}
	run, err = loadPlanItemRuns(ctx, tx, run)
	if err != nil {
		return err
	}
	if err := validatePlanLogs(run, itemID, logs); err != nil {
		return err
	}
	for _, entry := range logs {
		hash := planLogHash(entry)
		var oldHash, oldMachine string
		err = tx.QueryRowContext(ctx, `SELECT body_hash,machine_id FROM execution_plan_item_logs WHERE run_id=$1 AND item_run_id=$2 AND local_run_id=$3 AND source_id=$4`, runID, itemID, entry.LocalRunID, entry.ID).Scan(&oldHash, &oldMachine)
		if err == nil {
			if oldHash != hash || oldMachine != machine {
				return ErrExecutionPlanRequest
			}
			continue
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO execution_plan_item_logs(run_id,item_run_id,task_run_id,local_run_id,source_id,position_id,machine_id,level,message,source_created_at,body_hash) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`, runID, itemID, entry.TaskRunID, entry.LocalRunID, entry.ID, entry.PositionID, machine, entry.Level, entry.Message, entry.CreatedAt, hash)
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ListItemLogs 通过原运行归属读取，不要求正在连接执行电脑，也不通过岗位当前任务猜测日志。
func (s *PostgresExecutionPlanStore) ListItemLogs(ctx context.Context, tenant, email, runID, itemID string, before int64, limit int) ([]ExecutionPlanItemLog, error) {
	if before < 0 || limit < 1 || limit > 201 {
		return nil, ErrExecutionPlanRequest
	}
	run, err := s.GetRun(ctx, tenant, email, runID)
	if err != nil {
		return nil, err
	}
	found := false
	for _, item := range run.Items {
		if item.ID == itemID && item.TaskRunID != "" {
			found = true
		}
	}
	if !found {
		return nil, ErrNotFound
	}
	rows, err := s.db.QueryContext(ctx, `SELECT l.id,l.run_id::text,l.item_run_id::text,l.task_run_id::text,l.local_run_id,l.position_id,l.level,l.message,l.source_created_at FROM execution_plan_item_logs l JOIN execution_plan_runs r ON r.id=l.run_id JOIN execution_plans p ON p.id=r.plan_id WHERE l.run_id=$1 AND l.item_run_id=$2 AND p.user_email=$3 AND COALESCE(p.tenant_id::text,'')=$4 AND ($5::bigint=0 OR l.id<$5) ORDER BY l.id DESC LIMIT $6`, runID, itemID, email, tenant, before, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []ExecutionPlanItemLog{}
	for rows.Next() {
		var entry ExecutionPlanItemLog
		if err := rows.Scan(&entry.ID, &entry.PlanRunID, &entry.ItemRunID, &entry.TaskRunID, &entry.LocalRunID, &entry.PositionID, &entry.Level, &entry.Message, &entry.CreatedAt); err != nil {
			return nil, err
		}
		result = append(result, entry)
	}
	return result, rows.Err()
}

// ListItemLogs 只读内存原日志，返回云端序号供分页，调用方不能改动保存的记录。
func (s *MemoryExecutionPlanStore) ListItemLogs(ctx context.Context, tenant, email, runID, itemID string, before int64, limit int) ([]ExecutionPlanItemLog, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if before < 0 || limit < 1 || limit > 201 {
		return nil, ErrExecutionPlanRequest
	}
	run, exists := s.runs[runID]
	plan, owned := s.plans[run.PlanID]
	if !exists || !owned || plan.UserEmail != email || plan.TenantID != tenant {
		return nil, ErrNotFound
	}
	found := false
	for _, item := range run.Items {
		if item.ID == itemID && item.TaskRunID != "" {
			found = true
		}
	}
	if !found {
		return nil, ErrNotFound
	}
	result := []ExecutionPlanItemLog{}
	for _, stored := range s.itemLogs {
		entry := stored.entry
		if entry.PlanRunID == runID && entry.ItemRunID == itemID && (before == 0 || entry.ID < before) {
			result = append(result, entry)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID > result[j].ID })
	if len(result) > limit {
		result = result[:limit]
	}
	return result, nil
}
