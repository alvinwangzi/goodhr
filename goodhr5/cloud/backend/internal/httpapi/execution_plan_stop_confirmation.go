// 本文件处理 HRPlus 指定执行电脑的停止收尾确认；显示结束或设备离线不代替实际释放。
package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

// ExecutionPlanStopConfirmation 绑定原停止批次、设备与收尾确认，不接受网页猜测执行状态。
type ExecutionPlanStopConfirmation struct {
	RequestID        string `json:"request_id"`
	ExpectedVersion  int64  `json:"expected_version"`
	ActivationID     string `json:"activation_id"`
	MachineID        string `json:"machine_id"`
	CleanupConfirmed bool   `json:"cleanup_confirmed"`
}

// validate 拒绝缺失真实收尾确认、设备或批次的停止回执。
func (c ExecutionPlanStopConfirmation) validate() error {
	if !executionPlanUUID.MatchString(c.RequestID) || !executionPlanUUID.MatchString(c.ActivationID) || c.MachineID == "" || c.ExpectedVersion < 1 || !c.CleanupConfirmed {
		return ErrExecutionPlanRequest
	}
	return nil
}

// planStopConfirmationHash 将收尾回执的全部字段绑定原请求，避免借用启用或停止意图编号。
func planStopConfirmationHash(c ExecutionPlanStopConfirmation) string {
	raw, _ := json.Marshal([]any{"confirm_stop", c})
	return accountCredentialHash(string(raw))
}

// verifyPlanStopConfirmation 核对指定设备、配置及停止批次，旧回执不能开放新批次编辑。
func verifyPlanStopConfirmation(p ExecutionPlan, c ExecutionPlanStopConfirmation) error {
	if p.Version != c.ExpectedVersion {
		return ErrExecutionPlanVersion
	}
	if p.MachineID != c.MachineID || p.ActivationID != c.ActivationID || p.State != "stopped" || !p.StopRequested {
		return ErrExecutionPlanRequest
	}
	return nil
}

// ConfirmStopped 在同一内存锁下结算该停止批次的未结束记录，账号仍占用则拒绝。
func (s *MemoryExecutionPlanStore) ConfirmStopped(ctx context.Context, tenant, email, id string, c ExecutionPlanStopConfirmation) (ExecutionPlan, error) {
	if err := c.validate(); err != nil {
		return ExecutionPlan{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return ExecutionPlan{}, err
	}
	p, ok := s.plans[id]
	if !ok || s.deleted[id] || p.TenantID != tenant || p.UserEmail != email {
		return ExecutionPlan{}, ErrNotFound
	}
	key := id + "/" + c.RequestID
	hash := planStopConfirmationHash(c)
	if receipt, ok := s.intents[key]; ok {
		if receipt.Hash != hash {
			return ExecutionPlan{}, ErrExecutionPlanRequest
		}
		return cloneExecutionPlan(receipt.Result), nil
	}
	if err := verifyPlanStopConfirmation(p, c); err != nil {
		return ExecutionPlan{}, err
	}
	for _, run := range s.runs {
		if run.PlanID == id {
			if _, held := s.owners[run.OwnerID]; held {
				return ExecutionPlan{}, ErrExecutionPlanBusy
			}
		}
	}
	for runID, run := range s.runs {
		if run.PlanID == id && run.ActivationID == c.ActivationID && activeExecutionPlanState(run.State) {
			run.State = "stopped"
			run.Sequence++
			run.EndReason = "user_stopped"
			s.runs[runID] = run
		}
	}
	p.StopRequested = false
	p.StateSequence++
	p.UpdatedAt = time.Now().UTC()
	s.plans[id] = cloneExecutionPlan(p)
	if s.intents == nil {
		s.intents = map[string]executionPlanIntentReceipt{}
	}
	s.intents[key] = executionPlanIntentReceipt{Hash: hash, Result: cloneExecutionPlan(p)}
	return cloneExecutionPlan(p), nil
}

// ConfirmStopped 在账号锁和计划行锁内确认实际占用已释放，然后结算并解除停止保护。
func (s *PostgresExecutionPlanStore) ConfirmStopped(ctx context.Context, tenant, email, id string, c ExecutionPlanStopConfirmation) (ExecutionPlan, error) {
	if err := c.validate(); err != nil {
		return ExecutionPlan{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ExecutionPlan{}, err
	}
	defer tx.Rollback()
	if err = lockAccountExecution(ctx, tx, email); err != nil {
		return ExecutionPlan{}, err
	}
	p, err := scanExecutionPlan(tx.QueryRowContext(ctx, `SELECT `+executionPlanColumns+` FROM execution_plans WHERE id=$1 AND COALESCE(tenant_id::text,'')=$2 AND user_email=$3 AND deleted_at IS NULL FOR UPDATE`, id, tenant, email))
	if err != nil {
		return p, err
	}
	var stored string
	var raw []byte
	err = tx.QueryRowContext(ctx, `SELECT body_hash,result FROM execution_plan_requests WHERE plan_id=$1 AND request_id=$2`, id, c.RequestID).Scan(&stored, &raw)
	hash := planStopConfirmationHash(c)
	if err == nil {
		if stored != hash {
			return p, ErrExecutionPlanRequest
		}
		err = json.Unmarshal(raw, &p)
		return p, err
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return p, err
	}
	if err = verifyPlanStopConfirmation(p, c); err != nil {
		return p, err
	}
	var held bool
	err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM execution_plan_runs r JOIN account_execution_owners o ON o.owner_id=r.owner_id WHERE r.plan_id=$1)`, id).Scan(&held)
	if err != nil {
		return p, err
	}
	if held {
		return p, ErrExecutionPlanBusy
	}
	_, err = tx.ExecContext(ctx, `UPDATE execution_plan_item_runs i SET state='stopped',updated_at=NOW() FROM execution_plan_runs r WHERE i.run_id=r.id AND r.plan_id=$1 AND r.activation_id=$2 AND i.state NOT IN ('completed','incomplete','stopped','blocked')`, id, c.ActivationID)
	if err != nil {
		return p, err
	}
	_, err = tx.ExecContext(ctx, `UPDATE execution_plan_runs SET state='stopped',sequence=sequence+1,end_reason='user_stopped',finished_at=NOW() WHERE plan_id=$1 AND activation_id=$2 AND state NOT IN ('completed','incomplete','stopped','blocked')`, id, c.ActivationID)
	if err != nil {
		return p, err
	}
	p.StopRequested = false
	p.StateSequence++
	p.UpdatedAt = time.Now().UTC()
	_, err = tx.ExecContext(ctx, `UPDATE execution_plans SET stop_requested=false,state_sequence=$2,updated_at=$3 WHERE id=$1`, id, p.StateSequence, p.UpdatedAt)
	if err != nil {
		return p, err
	}
	raw, err = json.Marshal(p)
	if err != nil {
		return p, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO execution_plan_requests(plan_id,request_id,kind,body_hash,result) VALUES($1,$2,'confirm_stop',$3,$4)`, id, c.RequestID, hash, string(raw))
	if err != nil {
		return p, err
	}
	if err = tx.Commit(); err != nil {
		return p, err
	}
	return p, nil
}
