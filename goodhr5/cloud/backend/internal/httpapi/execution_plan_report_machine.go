// 本文件从 HRPlus 原领取回执核对历史报告电脑，不使用计划后来选择的电脑，也不授予当前执行权。
package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
)

// verifyReportClaim 核对原运行、原占用和冻结配置，不能用另一个运行的电脑证明代替。
func verifyReportClaim(run ExecutionPlanRun, claim ExecutionPlanRunPermit, machine string) error {
	if !executionPlanUUID.MatchString(run.OwnerID) {
		return ErrAccountExecutionProof
	}
	if machine == "" || claim.Run.ID != run.ID || claim.Run.PlanID != run.PlanID || claim.Run.ActivationID != run.ActivationID || claim.Run.ConfigVersion != run.ConfigVersion || claim.Run.ExecutionDate != run.ExecutionDate || claim.Run.OwnerID != run.OwnerID || claim.Owner.OwnerID != run.OwnerID || claim.Owner.OwnerType != "plan" || claim.Owner.MachineID != machine || !reflect.DeepEqual(claim.Run.Snapshot, run.Snapshot) {
		return ErrAccountExecutionProof
	}
	return nil
}

// VerifyReportMachine 在原账号与团队锁内读取不可变领取回执，原电脑即使已释放也能上传旧报告。
func (s *MemoryExecutionPlanStore) VerifyReportMachine(ctx context.Context, tenant, email, id, machine string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	run, exists := s.runs[id]
	plan, owned := s.plans[run.PlanID]
	if !exists || !owned || plan.UserEmail != email || plan.TenantID != tenant {
		return ErrNotFound
	}
	found := false
	for _, receipt := range s.runClaims {
		claim := receipt.Result
		if claim.Run.ID != run.ID || claim.Run.PlanID != run.PlanID || claim.Owner.OwnerID != run.OwnerID {
			continue
		}
		if err := verifyReportClaim(run, claim, machine); err != nil {
			return err
		}
		found = true
	}
	if !found {
		return ErrAccountExecutionProof
	}
	return nil
}

// VerifyReportMachine 只读核对原占用对应的持久领取回执，配置换电脑或软删除不改写历史归属。
func (s *PostgresExecutionPlanStore) VerifyReportMachine(ctx context.Context, tenant, email, id, machine string) error {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var planID string
	err = tx.QueryRowContext(ctx, `SELECT r.plan_id::text FROM execution_plan_runs r JOIN execution_plans p ON p.id=r.plan_id WHERE r.id=$1 AND COALESCE(p.tenant_id::text,'')=$2 AND p.user_email=$3`, id, tenant, email).Scan(&planID)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	run, err := scanPlanRun(tx.QueryRowContext(ctx, `SELECT `+executionPlanRunColumns+` FROM execution_plan_runs WHERE id=$1 AND plan_id=$2`, id, planID))
	if err != nil {
		return err
	}
	rows, err := tx.QueryContext(ctx, `SELECT result FROM execution_plan_requests WHERE plan_id=$1 AND kind='claim' AND result->'run'->>'id'=$2 AND result->'owner'->>'owner_id'=$3`, planID, id, run.OwnerID)
	if err != nil {
		return err
	}
	found := false
	for rows.Next() {
		var raw []byte
		var claim ExecutionPlanRunPermit
		if err = rows.Scan(&raw); err != nil {
			rows.Close()
			return err
		}
		if json.Unmarshal(raw, &claim) != nil {
			rows.Close()
			return ErrAccountExecutionProof
		}
		if err = verifyReportClaim(run, claim, machine); err != nil {
			rows.Close()
			return err
		}
		found = true
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if !found {
		return ErrAccountExecutionProof
	}
	return tx.Commit()
}
