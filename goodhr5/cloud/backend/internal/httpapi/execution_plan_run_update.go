// 本文件保存 HRPlus 计划运行的递增状态与严格释放，旧消息和旧占用凭证不能覆盖新运行。
package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

var ErrExecutionPlanSequence = errors.New("运行状态序号已过期或状态转换不匹配")

// ExecutionPlanRunUpdate 将状态或收尾更新绑定实际占用者及原请求编号。
type ExecutionPlanRunUpdate struct {
	PlanID           string `json:"-"`
	RunID            string `json:"-"`
	Action           string `json:"-"`
	RequestID        string `json:"request_id"`
	OwnerID          string `json:"owner_id"`
	MachineID        string `json:"machine_id"`
	Credential       string `json:"credential"`
	Sequence         int64  `json:"sequence"`
	State            string `json:"state"`
	CurrentItem      int    `json:"current_item"`
	EndReason        string `json:"end_reason,omitempty"`
	CleanupConfirmed bool   `json:"cleanup_confirmed"`
}

// validate 区分持有执行权的状态与收尾后释放的状态，不接受缺失收尾确认。
func (u ExecutionPlanRunUpdate) validate() error {
	for _, id := range []string{u.PlanID, u.RunID, u.RequestID, u.OwnerID} {
		if !executionPlanUUID.MatchString(id) {
			return ErrExecutionPlanRequest
		}
	}
	if u.MachineID == "" || len(u.Credential) < 32 || u.Sequence < 1 || u.CurrentItem < 0 || len(u.EndReason) > 2048 {
		return ErrExecutionPlanRequest
	}
	if u.Action == "status" && !u.CleanupConfirmed && (u.State == "running" || u.State == "draining") {
		return nil
	}
	if u.Action == "release" && u.CleanupConfirmed {
		switch u.State {
		case "waiting_window", "completed", "incomplete", "stopped", "blocked":
			return nil
		}
	}
	return ErrExecutionPlanRequest
}

// planRunUpdateHash 绑定路由运行和操作类型，只将凭证摘要写入原请求回执。
func planRunUpdateHash(u ExecutionPlanRunUpdate) string {
	u.Credential = accountCredentialHash(u.Credential)
	raw, _ := json.Marshal([]any{u.PlanID, u.RunID, u.Action, u})
	return accountCredentialHash(string(raw))
}

// accountClaim 使用真实账号作用域核对当前占用，不重复领取新的执行权。
func (u ExecutionPlanRunUpdate) accountClaim(email string) AccountExecutionClaim {
	return AccountExecutionClaim{UserEmail: email, MachineID: u.MachineID, OwnerType: "plan", OwnerID: u.OwnerID, RequestID: u.RequestID, Credential: u.Credential, LocalReserved: true}
}

// applyPlanRunUpdate 校验单调序号、主项游标与停止意图，保留原首次开始时间。
func applyPlanRunUpdate(p ExecutionPlan, r ExecutionPlanRun, u ExecutionPlanRunUpdate) (ExecutionPlanRun, error) {
	if p.MachineID != u.MachineID || r.OwnerID != u.OwnerID || p.ActivationID != r.ActivationID {
		return r, ErrAccountExecutionProof
	}
	if u.Sequence <= r.Sequence || u.CurrentItem < r.CurrentItem || u.CurrentItem > len(r.Snapshot.Items) || (r.State != "starting" && r.State != "running" && r.State != "draining") {
		return r, ErrExecutionPlanSequence
	}
	if u.State == "running" && (r.State == "draining" || p.StopRequested || p.State != "enabled") {
		return r, ErrExecutionPlanSequence
	}
	if p.StopRequested && u.Action == "release" && u.State != "stopped" && u.State != "blocked" {
		return r, ErrExecutionPlanSequence
	}
	if u.State == "completed" && (u.CurrentItem != len(r.Snapshot.Items) || r.StartedAt == nil) {
		return r, ErrExecutionPlanSequence
	}
	now := time.Now().UTC()
	r.State = u.State
	r.Sequence = u.Sequence
	r.CurrentItem = u.CurrentItem
	r.EndReason = u.EndReason
	if u.State == "running" && r.StartedAt == nil {
		r.StartedAt = &now
	}
	if u.Action == "release" && !activeExecutionPlanState(u.State) {
		r.FinishedAt = &now
	}
	return r, nil
}

// GetRun 读取真实归属的内存运行，包括已经软删除计划的历史，不接受客户端账号声明。
func (s *MemoryExecutionPlanStore) GetRun(ctx context.Context, tenant, email, id string) (ExecutionPlanRun, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return ExecutionPlanRun{}, err
	}
	r, ok := s.runs[id]
	p, owned := s.plans[r.PlanID]
	if !ok || !owned || p.TenantID != tenant || p.UserEmail != email {
		return ExecutionPlanRun{}, ErrNotFound
	}
	return clonePlanRunPermit(ExecutionPlanRunPermit{Run: r}).Run, nil
}

// GetRun 先核对计划归属再读取 PostgreSQL 历史运行，不通过设备在线状态猜测实际占用。
func (s *PostgresExecutionPlanStore) GetRun(ctx context.Context, tenant, email, id string) (ExecutionPlanRun, error) {
	var planID string
	err := s.db.QueryRowContext(ctx, `SELECT r.plan_id FROM execution_plan_runs r JOIN execution_plans p ON p.id=r.plan_id WHERE r.id=$1 AND COALESCE(p.tenant_id::text,'')=$2 AND p.user_email=$3`, id, tenant, email).Scan(&planID)
	if errors.Is(err, sql.ErrNoRows) {
		return ExecutionPlanRun{}, ErrNotFound
	}
	if err != nil {
		return ExecutionPlanRun{}, err
	}
	return scanPlanRun(s.db.QueryRowContext(ctx, `SELECT `+executionPlanRunColumns+` FROM execution_plan_runs WHERE id=$1 AND plan_id=$2`, id, planID))
}

// UpdateRun 在内存和岗位锁内同步状态或释放，状态上报不会自行解除账号占用。
func (s *MemoryExecutionPlanStore) UpdateRun(ctx context.Context, tenant, email string, u ExecutionPlanRunUpdate) (ExecutionPlanRunPermit, error) {
	if err := u.validate(); err != nil {
		return ExecutionPlanRunPermit{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.positions != nil {
		s.positions.mu.Lock()
		defer s.positions.mu.Unlock()
	}
	if err := ctx.Err(); err != nil {
		return ExecutionPlanRunPermit{}, err
	}
	p, ok := s.plans[u.PlanID]
	r, found := s.runs[u.RunID]
	if !ok || !found || s.deleted[u.PlanID] || p.TenantID != tenant || p.UserEmail != email || r.PlanID != p.ID {
		return ExecutionPlanRunPermit{}, ErrNotFound
	}
	key := u.PlanID + "/" + u.RequestID
	hash := planRunUpdateHash(u)
	if _, used := s.intents[key]; used {
		return ExecutionPlanRunPermit{}, ErrExecutionPlanRequest
	}
	if _, used := s.runClaims[key]; used {
		return ExecutionPlanRunPermit{}, ErrExecutionPlanRequest
	}
	if receipt, exists := s.runUpdates[key]; exists {
		if receipt.Hash != hash {
			return ExecutionPlanRunPermit{}, ErrExecutionPlanRequest
		}
		return clonePlanRunPermit(receipt.Result), nil
	}
	if s.accounts[email] != u.OwnerID || s.ownerHashes[u.OwnerID] != accountCredentialHash(u.Credential) {
		return ExecutionPlanRunPermit{}, ErrAccountExecutionProof
	}
	owner, held := s.owners[u.OwnerID]
	if !held || owner.MachineID != u.MachineID {
		return ExecutionPlanRunPermit{}, ErrAccountExecutionProof
	}
	changed, err := applyPlanRunUpdate(p, r, u)
	if err != nil {
		return ExecutionPlanRunPermit{}, err
	}
	if u.Action == "release" {
		requestKey := email + "/" + u.RequestID
		if _, used := s.runRequestHashes[requestKey]; used {
			return ExecutionPlanRunPermit{}, ErrAccountExecutionConflict
		}
		if s.runRequestHashes == nil {
			s.runRequestHashes = map[string]string{}
		}
		s.runRequestHashes[requestKey] = hash
		delete(s.owners, u.OwnerID)
		delete(s.accounts, email)
		owner.State = "released"
		if s.positions != nil {
			delete(s.positions.accountOwners, email)
		}
	} else {
		if u.State == "draining" {
			owner.State = "releasing"
		} else {
			owner.State = "running"
		}
		s.owners[u.OwnerID] = owner
		if s.positions != nil {
			s.positions.accountOwners[email] = owner
		}
	}
	s.runs[u.RunID] = changed
	result := ExecutionPlanRunPermit{Run: changed, Owner: owner}
	if s.runUpdates == nil {
		s.runUpdates = map[string]executionPlanRunClaimReceipt{}
	}
	s.runUpdates[key] = executionPlanRunClaimReceipt{Hash: hash, Result: clonePlanRunPermit(result)}
	return clonePlanRunPermit(result), nil
}

// UpdateRun 在账号锁及计划运行行锁内保存单调状态、原请求与释放结果，任何失败完整回滚。
func (s *PostgresExecutionPlanStore) UpdateRun(ctx context.Context, tenant, email string, u ExecutionPlanRunUpdate) (ExecutionPlanRunPermit, error) {
	if err := u.validate(); err != nil {
		return ExecutionPlanRunPermit{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ExecutionPlanRunPermit{}, err
	}
	defer tx.Rollback()
	if err = lockAccountExecution(ctx, tx, email); err != nil {
		return ExecutionPlanRunPermit{}, err
	}
	p, err := scanExecutionPlan(tx.QueryRowContext(ctx, `SELECT `+executionPlanColumns+` FROM execution_plans WHERE id=$1 AND COALESCE(tenant_id::text,'')=$2 AND user_email=$3 AND deleted_at IS NULL FOR UPDATE`, u.PlanID, tenant, email))
	if err != nil {
		return ExecutionPlanRunPermit{}, err
	}
	r, err := scanPlanRun(tx.QueryRowContext(ctx, `SELECT `+executionPlanRunColumns+` FROM execution_plan_runs WHERE id=$1 AND plan_id=$2 FOR UPDATE`, u.RunID, u.PlanID))
	if errors.Is(err, sql.ErrNoRows) {
		return ExecutionPlanRunPermit{}, ErrNotFound
	}
	if err != nil {
		return ExecutionPlanRunPermit{}, err
	}
	hash := planRunUpdateHash(u)
	var stored string
	var raw []byte
	err = tx.QueryRowContext(ctx, `SELECT body_hash,result FROM execution_plan_requests WHERE plan_id=$1 AND request_id=$2`, u.PlanID, u.RequestID).Scan(&stored, &raw)
	if err == nil {
		if hash != stored {
			return ExecutionPlanRunPermit{}, ErrExecutionPlanRequest
		}
		var result ExecutionPlanRunPermit
		err = json.Unmarshal(raw, &result)
		return result, err
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return ExecutionPlanRunPermit{}, err
	}
	owner, err := accountOwnerProof(ctx, tx, u.accountClaim(email))
	if err != nil {
		return ExecutionPlanRunPermit{}, err
	}
	changed, err := applyPlanRunUpdate(p, r, u)
	if err != nil {
		return ExecutionPlanRunPermit{}, err
	}
	_, err = tx.ExecContext(ctx, `UPDATE execution_plan_runs SET state=$2,sequence=$3,current_item=$4,end_reason=$5,started_at=$6,finished_at=$7 WHERE id=$1`, u.RunID, changed.State, changed.Sequence, changed.CurrentItem, changed.EndReason, changed.StartedAt, changed.FinishedAt)
	if err != nil {
		return ExecutionPlanRunPermit{}, err
	}
	if u.Action == "release" {
		if err = releaseAccountExecutionTx(ctx, tx, u.accountClaim(email), true); err != nil {
			return ExecutionPlanRunPermit{}, err
		}
		owner.State = "released"
	} else {
		if u.State == "draining" {
			owner.State = "releasing"
		} else {
			owner.State = "running"
		}
		_, err = tx.ExecContext(ctx, `UPDATE account_execution_owners SET state=$2 WHERE account_key=$1`, email, owner.State)
		if err != nil {
			return ExecutionPlanRunPermit{}, err
		}
	}
	// 读取数据库实际保存的时间精度，首次回执与后续重读必须使用同一开始时间。
	changed, err = scanPlanRun(tx.QueryRowContext(ctx, `SELECT `+executionPlanRunColumns+` FROM execution_plan_runs WHERE id=$1`, u.RunID))
	if err != nil {
		return ExecutionPlanRunPermit{}, err
	}
	result := ExecutionPlanRunPermit{Run: changed, Owner: owner}
	raw, err = json.Marshal(result)
	if err != nil {
		return ExecutionPlanRunPermit{}, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO execution_plan_requests(plan_id,request_id,kind,body_hash,result) VALUES($1,$2,$3,$4,$5)`, u.PlanID, u.RequestID, u.Action, hash, string(raw))
	if err != nil {
		return ExecutionPlanRunPermit{}, err
	}
	if err = tx.Commit(); err != nil {
		return ExecutionPlanRunPermit{}, err
	}
	return result, nil
}
