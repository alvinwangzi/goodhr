// 本文件通过 PostgreSQL 行锁和事务保存 HRPlus 计划，不允许配置更新与未结束运行交叉写入。
package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
)

// PostgresExecutionPlanStore 使用现有连接池，配置和规范化执行项同事务提交。
type PostgresExecutionPlanStore struct{ db *sql.DB }

// NewPostgresExecutionPlanStore 创建真实数据库计划存储。
func NewPostgresExecutionPlanStore(db *sql.DB) *PostgresExecutionPlanStore {
	return &PostgresExecutionPlanStore{db: db}
}

const executionPlanColumns = `id::text,COALESCE(tenant_id::text,''),user_email,machine_id,config_version,state_sequence,COALESCE(activation_id::text,''),state,stop_requested,config,created_at,updated_at`

// scanExecutionPlan 复用单行与列表解析，配置解码失败时不返回半套编排。
func scanExecutionPlan(row interface{ Scan(...any) error }) (ExecutionPlan, error) {
	var p ExecutionPlan
	var raw []byte
	err := row.Scan(&p.ID, &p.TenantID, &p.UserEmail, &p.MachineID, &p.Version, &p.StateSequence, &p.ActivationID, &p.State, &p.StopRequested, &raw, &p.CreatedAt, &p.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return p, ErrNotFound
	}
	if err != nil {
		return p, err
	}
	err = json.Unmarshal(raw, &p.Config)
	return p, err
}

// Get 读取团队和用户都匹配的未删除计划。
func (s *PostgresExecutionPlanStore) Get(ctx context.Context, tenant, email, id string) (ExecutionPlan, error) {
	return scanExecutionPlan(s.db.QueryRowContext(ctx, `SELECT `+executionPlanColumns+` FROM execution_plans WHERE id=$1 AND COALESCE(tenant_id::text,'')=$2 AND user_email=$3 AND deleted_at IS NULL`, id, tenant, email))
}

// List 不依赖本机任务状态，返回当前用户真实保存的计划。
func (s *PostgresExecutionPlanStore) List(ctx context.Context, tenant, email string) ([]ExecutionPlan, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+executionPlanColumns+` FROM execution_plans WHERE COALESCE(tenant_id::text,'')=$1 AND user_email=$2 AND deleted_at IS NULL ORDER BY created_at DESC,id`, tenant, email)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ExecutionPlan{}
	for rows.Next() {
		p, err := scanExecutionPlan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// lockExecutionPlan 锁定当前配置行，并核对未结算运行或尚未释放的实际账号占用。
func lockExecutionPlan(ctx context.Context, tx *sql.Tx, tenant, email, id string, expected int64) (ExecutionPlan, error) {
	p, err := scanExecutionPlan(tx.QueryRowContext(ctx, `SELECT `+executionPlanColumns+` FROM execution_plans WHERE id=$1 AND COALESCE(tenant_id::text,'')=$2 AND user_email=$3 AND deleted_at IS NULL FOR UPDATE`, id, tenant, email))
	if err != nil {
		return p, err
	}
	if p.Version != expected {
		return p, ErrExecutionPlanVersion
	}
	if p.StopRequested {
		return p, ErrExecutionPlanBusy
	}
	var busy bool
	err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM execution_plan_runs r WHERE r.plan_id=$1 AND (r.state NOT IN ('completed','incomplete','stopped','blocked') OR EXISTS(SELECT 1 FROM account_execution_owners o WHERE o.owner_id=r.owner_id)))`, id).Scan(&busy)
	if err != nil {
		return p, err
	}
	if busy {
		return p, ErrExecutionPlanBusy
	}
	return p, nil
}

// Save 持锁校验版本和运行，任何子项写入失败均回滚原配置、版本和顺序。
func (s *PostgresExecutionPlanStore) Save(ctx context.Context, p ExecutionPlan, expected int64) (ExecutionPlan, error) {
	if err := p.Config.Validate(); err != nil {
		return p, err
	}
	if p.UserEmail == "" || p.MachineID == "" {
		return p, fmt.Errorf("计划缺少所有者或执行电脑")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return p, err
	}
	defer tx.Rollback()
	raw, err := json.Marshal(p.Config)
	if err != nil {
		return p, err
	}
	if p.ID == "" {
		if expected != 0 {
			return p, ErrExecutionPlanVersion
		}
		p.ID, err = newExecutionPlanID()
		if err != nil {
			return p, err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO execution_plans(id,tenant_id,user_email,machine_id,name,config) VALUES($1,NULLIF($2,'')::uuid,$3,$4,$5,$6)`, p.ID, p.TenantID, p.UserEmail, p.MachineID, p.Config.Name, string(raw))
	} else {
		if _, err = lockExecutionPlan(ctx, tx, p.TenantID, p.UserEmail, p.ID, expected); err != nil {
			return p, err
		}
		_, err = tx.ExecContext(ctx, `UPDATE execution_plans SET machine_id=$2,name=$3,config=$4,config_version=config_version+1,state_sequence=state_sequence+1,state='stopped',stop_requested=false,updated_at=NOW() WHERE id=$1`, p.ID, p.MachineID, p.Config.Name, string(raw))
	}
	if err != nil {
		return p, err
	}
	for _, table := range []string{"execution_plan_windows", "execution_plan_items"} {
		if _, err = tx.ExecContext(ctx, `DELETE FROM `+table+` WHERE plan_id=$1`, p.ID); err != nil {
			return p, err
		}
	}
	for _, w := range p.Config.Schedule.Windows {
		if _, err = tx.ExecContext(ctx, `INSERT INTO execution_plan_windows(plan_id,ordinal,start_minute,end_minute) VALUES($1,$2,$3,$4)`, p.ID, w.Order, w.StartMinute, w.EndMinute); err != nil {
			return p, err
		}
	}
	for _, item := range p.Config.Items {
		actions, _ := json.Marshal(item.Actions)
		if _, err = tx.ExecContext(ctx, `INSERT INTO execution_plan_items(plan_id,item_id,position_id,ordinal,actions,prioritize_reply) VALUES($1,$2,$3,$4,$5,$6)`, p.ID, item.ID, item.PositionID, item.Order, string(actions), item.PrioritizeReply); err != nil {
			return p, err
		}
	}
	saved, err := scanExecutionPlan(tx.QueryRowContext(ctx, `SELECT `+executionPlanColumns+` FROM execution_plans WHERE id=$1`, p.ID))
	if err != nil {
		return p, err
	}
	if err = tx.Commit(); err != nil {
		return p, err
	}
	return saved, nil
}

// Delete 只隐藏配置，收据、任务、子运行和报告均不删除。
func (s *PostgresExecutionPlanStore) Delete(ctx context.Context, tenant, email, id string, expected int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = lockExecutionPlan(ctx, tx, tenant, email, id, expected); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE execution_plans SET deleted_at=NOW(),state='disabled',state_sequence=state_sequence+1,updated_at=NOW() WHERE id=$1`, id)
	if err != nil {
		return err
	}
	return tx.Commit()
}
