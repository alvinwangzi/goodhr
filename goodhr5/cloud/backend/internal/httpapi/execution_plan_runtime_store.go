// 本文件一致读取 HRPlus 当前计划、原运行与安全账号占用，读取不启动或结束任务。
package httpapi

import (
	"context"
	"database/sql"
	"errors"
	"sort"
)

// ExecutionPlanRuntimeSnapshot 保留同一读取边界的配置和执行事实，不含凭证或页面信息。
type ExecutionPlanRuntimeSnapshot struct {
	Plan         ExecutionPlan          `json:"plan"`
	Runs         []ExecutionPlanRun     `json:"runs"`
	AccountOwner *AccountExecutionOwner `json:"account_owner,omitempty"`
}

// RuntimeSnapshot 在与写入相同的内存锁边界读取当前计划、原运行和账号占用。
func (s *MemoryExecutionPlanStore) RuntimeSnapshot(ctx context.Context, tenant, email, id string) (ExecutionPlanRuntimeSnapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.positions != nil {
		s.positions.mu.Lock()
		defer s.positions.mu.Unlock()
	}
	if err := ctx.Err(); err != nil {
		return ExecutionPlanRuntimeSnapshot{}, err
	}
	plan, ok := s.plans[id]
	if !ok || s.deleted[id] || plan.TenantID != tenant || plan.UserEmail != email {
		return ExecutionPlanRuntimeSnapshot{}, ErrNotFound
	}
	value := ExecutionPlanRuntimeSnapshot{Plan: cloneExecutionPlan(plan), Runs: []ExecutionPlanRun{}}
	for _, run := range s.runs {
		if run.PlanID == id {
			value.Runs = append(value.Runs, clonePlanRunPermit(ExecutionPlanRunPermit{Run: run}).Run)
		}
	}
	sort.Slice(value.Runs, func(i, j int) bool {
		if value.Runs[i].ExecutionDate != value.Runs[j].ExecutionDate {
			return value.Runs[i].ExecutionDate > value.Runs[j].ExecutionDate
		}
		return value.Runs[i].ID > value.Runs[j].ID
	})
	if len(value.Runs) > 50 {
		value.Runs = value.Runs[:50]
	}
	if owner, exists := s.owners[s.accounts[email]]; exists {
		copy := owner
		value.AccountOwner = &copy
	}
	if s.positions != nil {
		if owner, exists := s.positions.accountOwners[email]; exists {
			copy := owner
			value.AccountOwner = &copy
		}
	}
	return value, nil
}

// RuntimeSnapshot 用只读一致事务核对当前配置、原执行项与账号占用，不创建用户或更新心跳。
func (s *PostgresExecutionPlanStore) RuntimeSnapshot(ctx context.Context, tenant, email, id string) (ExecutionPlanRuntimeSnapshot, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return ExecutionPlanRuntimeSnapshot{}, err
	}
	defer tx.Rollback()
	plan, err := scanExecutionPlan(tx.QueryRowContext(ctx, `SELECT `+executionPlanColumns+` FROM execution_plans WHERE id=$1 AND COALESCE(tenant_id::text,'')=$2 AND user_email=$3 AND deleted_at IS NULL`, id, tenant, email))
	if err != nil {
		return ExecutionPlanRuntimeSnapshot{}, err
	}
	value := ExecutionPlanRuntimeSnapshot{Plan: plan, Runs: []ExecutionPlanRun{}}
	rows, err := tx.QueryContext(ctx, `SELECT `+executionPlanRunColumns+` FROM execution_plan_runs WHERE plan_id=$1 ORDER BY execution_date DESC,id DESC LIMIT 50`, id)
	if err != nil {
		return value, err
	}
	for rows.Next() {
		run, e := scanPlanRun(rows)
		if e != nil {
			rows.Close()
			return value, e
		}
		value.Runs = append(value.Runs, run)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return value, err
	}
	for index, run := range value.Runs {
		value.Runs[index], err = loadPlanItemRuns(ctx, tx, run)
		if err != nil {
			return value, err
		}
	}
	var owner AccountExecutionOwner
	err = tx.QueryRowContext(ctx, `SELECT owner_id::text,owner_type,machine_id,state FROM account_execution_owners WHERE account_key=$1`, email).Scan(&owner.OwnerID, &owner.OwnerType, &owner.MachineID, &owner.State)
	if err == nil {
		value.AccountOwner = &owner
	} else if !errors.Is(err, sql.ErrNoRows) {
		return value, err
	}
	return value, tx.Commit()
}
