// 本文件按真实账号和团队读取 HRPlus 原计划执行记录，提供前端刷新后的状态，不重新开始任务。
package httpapi

import (
	"context"
	"database/sql"
	"errors"
	"sort"
)

// ListRuns 读取内存原运行历史，重复岗位仍保留独立执行项和原 TaskRun。
func (s *MemoryExecutionPlanStore) ListRuns(ctx context.Context, tenant, email, planID string) ([]ExecutionPlanRun, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	p, exists := s.plans[planID]
	if !exists || p.TenantID != tenant || p.UserEmail != email {
		return nil, ErrNotFound
	}
	items := []ExecutionPlanRun{}
	for _, run := range s.runs {
		if run.PlanID == planID {
			items = append(items, clonePlanRunPermit(ExecutionPlanRunPermit{Run: run}).Run)
		}
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].ExecutionDate != items[j].ExecutionDate {
			return items[i].ExecutionDate > items[j].ExecutionDate
		}
		return items[i].ID > items[j].ID
	})
	if len(items) > 50 {
		items = items[:50]
	}
	return items, nil
}

// ListRuns 在一致只读事务中读取最近五十次原运行及执行项，旧设备或软删除不扩大读取权限。
func (s *PostgresExecutionPlanStore) ListRuns(ctx context.Context, tenant, email, planID string) ([]ExecutionPlanRun, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var owned string
	err = tx.QueryRowContext(ctx, `SELECT id FROM execution_plans WHERE id=$1 AND user_email=$2 AND COALESCE(tenant_id::text,'')=$3`, planID, email, tenant).Scan(&owned)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT `+executionPlanRunColumns+` FROM execution_plan_runs WHERE plan_id=$1 ORDER BY execution_date DESC,id DESC LIMIT 50`, planID)
	if err != nil {
		return nil, err
	}
	items := []ExecutionPlanRun{}
	for rows.Next() {
		run, err := scanPlanRun(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		items = append(items, run)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	for i, run := range items {
		items[i], err = loadPlanItemRuns(ctx, tx, run)
		if err != nil {
			return nil, err
		}
	}
	return items, tx.Commit()
}
