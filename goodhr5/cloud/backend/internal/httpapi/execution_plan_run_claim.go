// 本文件领取 HRPlus 当天计划运行，与账号占用和原请求结果同事务保存，尚未确认开始时保持 starting。
package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

// ExecutionPlanRunClaim 携带本地预留后已持久保存的运行编号和占用凭证。
type ExecutionPlanRunClaim struct {
	PlanID          string `json:"plan_id"`
	ActivationID    string `json:"activation_id"`
	ExpectedVersion int64  `json:"expected_version"`
	ExecutionDate   string `json:"execution_date"`
	RunID           string `json:"run_id"`
	RequestID       string `json:"request_id"`
	MachineID       string `json:"machine_id"`
	OwnerID         string `json:"owner_id"`
	Credential      string `json:"credential"`
	LocalReserved   bool   `json:"local_reserved"`
}

// ExecutionPlanRunPermit 只包含已保存的运行和安全占用快照，不回传凭证原文。
type ExecutionPlanRunPermit struct {
	Run   ExecutionPlanRun      `json:"run"`
	Owner AccountExecutionOwner `json:"owner"`
}

// executionPlanRunClaimReceipt 保存原领取结果及摘要，避免重试创建第二次运行。
type executionPlanRunClaimReceipt struct {
	Hash   string
	Result ExecutionPlanRunPermit
}

// validate 校验日期与编号结构，招聘时段许可由本地时钟决定，云端不操作招聘页面。
func (c ExecutionPlanRunClaim) validate() error {
	for _, id := range []string{c.PlanID, c.ActivationID, c.RunID, c.RequestID, c.OwnerID} {
		if !executionPlanUUID.MatchString(id) {
			return ErrExecutionPlanRequest
		}
	}
	if _, err := time.Parse("2006-01-02", c.ExecutionDate); err != nil {
		return ErrExecutionPlanRequest
	}
	if c.ExpectedVersion < 1 || c.MachineID == "" || len(c.Credential) < 32 || !c.LocalReserved {
		return ErrExecutionPlanRequest
	}
	return nil
}

// accountClaim 将已认证账号加入执行权请求，不信任客户端传入的账号作用域。
func (c ExecutionPlanRunClaim) accountClaim(email string) AccountExecutionClaim {
	return AccountExecutionClaim{UserEmail: email, MachineID: c.MachineID, OwnerType: "plan", OwnerID: c.OwnerID, RequestID: c.RequestID, Credential: c.Credential, LocalReserved: c.LocalReserved}
}

// planRunClaimHash 摘要化凭证后绑定全部领取字段，不将凭证原文写入回执。
func planRunClaimHash(c ExecutionPlanRunClaim) string {
	c.Credential = accountCredentialHash(c.Credential)
	raw, _ := json.Marshal(c)
	return accountCredentialHash(string(raw))
}

// verifyPlanRunClaim 核对配置、原批次和指定电脑，停止意图与领取共享计划锁。
func verifyPlanRunClaim(p ExecutionPlan, c ExecutionPlanRunClaim) error {
	if p.Version != c.ExpectedVersion {
		return ErrExecutionPlanVersion
	}
	if p.State != "enabled" || p.StopRequested || p.ActivationID != c.ActivationID || p.MachineID != c.MachineID {
		return ErrExecutionPlanRequest
	}
	return nil
}

// resumablePlanRun 只有尚未开始或在等下一窗口的同一天运行可以重新领取。
func resumablePlanRun(r ExecutionPlanRun, c ExecutionPlanRunClaim) error {
	if r.ID != c.RunID || r.ConfigVersion != c.ExpectedVersion {
		return ErrExecutionPlanRequest
	}
	if r.State != "pending" && r.State != "waiting_resource" && r.State != "waiting_window" {
		return ErrExecutionPlanBusy
	}
	return nil
}

// clonePlanRunPermit 深拷贝不可变快照，调用者不能改变已保存的编排或计数游标。
func clonePlanRunPermit(p ExecutionPlanRunPermit) ExecutionPlanRunPermit {
	raw, _ := json.Marshal(p)
	var out ExecutionPlanRunPermit
	_ = json.Unmarshal(raw, &out)
	return out
}

// ClaimRun 在计划与岗位锁内领取内存账号占用，旧手动入口也会检查同一占用快照。
func (s *MemoryExecutionPlanStore) ClaimRun(ctx context.Context, tenant, email string, c ExecutionPlanRunClaim) (ExecutionPlanRunPermit, error) {
	if err := c.validate(); err != nil {
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
	p, ok := s.plans[c.PlanID]
	if !ok || s.deleted[c.PlanID] || p.TenantID != tenant || p.UserEmail != email {
		return ExecutionPlanRunPermit{}, ErrNotFound
	}
	key := c.PlanID + "/" + c.RequestID
	hash := planRunClaimHash(c)
	if err := verifyPlanRunClaim(p, c); err != nil {
		return ExecutionPlanRunPermit{}, err
	}
	if _, used := s.intents[key]; used {
		return ExecutionPlanRunPermit{}, ErrExecutionPlanRequest
	}
	if receipt, ok := s.runClaims[key]; ok {
		if receipt.Hash != hash {
			return ExecutionPlanRunPermit{}, ErrExecutionPlanRequest
		}
		if s.accounts[email] != c.OwnerID || s.ownerHashes[c.OwnerID] != accountCredentialHash(c.Credential) {
			return ExecutionPlanRunPermit{}, ErrAccountExecutionReleased
		}
		result := clonePlanRunPermit(receipt.Result)
		result.Owner = s.owners[c.OwnerID]
		result.Run = s.runs[c.RunID]
		return clonePlanRunPermit(result), nil
	}
	requestKey := email + "/" + c.RequestID
	if _, used := s.runRequestHashes[requestKey]; used {
		return ExecutionPlanRunPermit{}, ErrAccountExecutionConflict
	}
	if _, held := s.accounts[email]; held {
		return ExecutionPlanRunPermit{}, ErrAccountExecutionBusy
	}
	if _, used := s.ownerHashes[c.OwnerID]; used {
		return ExecutionPlanRunPermit{}, ErrAccountExecutionReleased
	}
	if s.positions != nil {
		for _, position := range s.positions.positions {
			if position.UserEmail == email && position.Status == "running" {
				return ExecutionPlanRunPermit{}, ErrAccountExecutionBusy
			}
		}
	}
	var run ExecutionPlanRun
	for _, old := range s.runs {
		if old.PlanID == c.PlanID && old.ActivationID == c.ActivationID && old.ExecutionDate == c.ExecutionDate {
			if err := resumablePlanRun(old, c); err != nil {
				return ExecutionPlanRunPermit{}, err
			}
			run = old
			break
		}
	}
	if run.ID == "" {
		if _, exists := s.runs[c.RunID]; exists {
			return ExecutionPlanRunPermit{}, ErrExecutionPlanRequest
		}
		run = ExecutionPlanRun{ID: c.RunID, PlanID: p.ID, ActivationID: p.ActivationID, ExecutionDate: c.ExecutionDate, ConfigVersion: p.Version, Sequence: 1, Snapshot: cloneExecutionPlan(p).Config}
	} else {
		run.Sequence++
	}
	run.State = "starting"
	run.OwnerID = c.OwnerID
	owner := AccountExecutionOwner{OwnerID: c.OwnerID, OwnerType: "plan", MachineID: c.MachineID, State: "starting"}
	if s.owners == nil {
		s.owners = map[string]AccountExecutionOwner{}
	}
	if s.accounts == nil {
		s.accounts = map[string]string{}
	}
	if s.ownerHashes == nil {
		s.ownerHashes = map[string]string{}
	}
	if s.runClaims == nil {
		s.runClaims = map[string]executionPlanRunClaimReceipt{}
	}
	s.owners[c.OwnerID] = owner
	s.accounts[email] = c.OwnerID
	s.ownerHashes[c.OwnerID] = accountCredentialHash(c.Credential)
	s.runs[c.RunID] = run
	result := ExecutionPlanRunPermit{Run: run, Owner: owner}
	s.runClaims[key] = executionPlanRunClaimReceipt{Hash: hash, Result: clonePlanRunPermit(result)}
	if s.runRequestHashes == nil {
		s.runRequestHashes = map[string]string{}
	}
	s.runRequestHashes[requestKey] = hash
	if s.positions != nil {
		if s.positions.accountOwners == nil {
			s.positions.accountOwners = map[string]AccountExecutionOwner{}
		}
		s.positions.accountOwners[email] = owner
	}
	return clonePlanRunPermit(result), nil
}

const executionPlanRunColumns = `id::text,plan_id::text,activation_id::text,execution_date::text,config_version,sequence,state,current_item,snapshot,end_reason,COALESCE(owner_id::text,'')`

// scanPlanRun 复用运行读取字段，无法解析完整快照时拒绝返回半套许可。
func scanPlanRun(row interface{ Scan(...any) error }) (ExecutionPlanRun, error) {
	var r ExecutionPlanRun
	var raw []byte
	err := row.Scan(&r.ID, &r.PlanID, &r.ActivationID, &r.ExecutionDate, &r.ConfigVersion, &r.Sequence, &r.State, &r.CurrentItem, &raw, &r.EndReason, &r.OwnerID)
	if err != nil {
		return r, err
	}
	err = json.Unmarshal(raw, &r.Snapshot)
	return r, err
}

// ClaimRun 在账号锁及计划行锁内将当天运行、执行权和原许可回执一起提交。
func (s *PostgresExecutionPlanStore) ClaimRun(ctx context.Context, tenant, email string, c ExecutionPlanRunClaim) (ExecutionPlanRunPermit, error) {
	if err := c.validate(); err != nil {
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
	p, err := scanExecutionPlan(tx.QueryRowContext(ctx, `SELECT `+executionPlanColumns+` FROM execution_plans WHERE id=$1 AND COALESCE(tenant_id::text,'')=$2 AND user_email=$3 AND deleted_at IS NULL FOR UPDATE`, c.PlanID, tenant, email))
	if err != nil {
		return ExecutionPlanRunPermit{}, err
	}
	hash := planRunClaimHash(c)
	if err = verifyPlanRunClaim(p, c); err != nil {
		return ExecutionPlanRunPermit{}, err
	}
	var stored string
	var raw []byte
	err = tx.QueryRowContext(ctx, `SELECT body_hash,result FROM execution_plan_requests WHERE plan_id=$1 AND request_id=$2`, c.PlanID, c.RequestID).Scan(&stored, &raw)
	if err == nil {
		if stored != hash {
			return ExecutionPlanRunPermit{}, ErrExecutionPlanRequest
		}
		var result ExecutionPlanRunPermit
		if err = json.Unmarshal(raw, &result); err != nil {
			return result, err
		}
		result.Owner, err = accountOwnerProof(ctx, tx, c.accountClaim(email))
		if errors.Is(err, ErrAccountExecutionProof) {
			err = ErrAccountExecutionReleased
		}
		if err != nil {
			return ExecutionPlanRunPermit{}, err
		}
		result.Run, err = scanPlanRun(tx.QueryRowContext(ctx, `SELECT `+executionPlanRunColumns+` FROM execution_plan_runs WHERE id=$1`, c.RunID))
		return result, err
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return ExecutionPlanRunPermit{}, err
	}
	run, err := scanPlanRun(tx.QueryRowContext(ctx, `SELECT `+executionPlanRunColumns+` FROM execution_plan_runs WHERE plan_id=$1 AND activation_id=$2 AND execution_date=$3 FOR UPDATE`, c.PlanID, c.ActivationID, c.ExecutionDate))
	newRun := errors.Is(err, sql.ErrNoRows)
	if err != nil && !newRun {
		return ExecutionPlanRunPermit{}, err
	}
	if !newRun {
		if err = resumablePlanRun(run, c); err != nil {
			return ExecutionPlanRunPermit{}, err
		}
	}
	owner, err := claimAccountExecutionTx(ctx, tx, c.accountClaim(email))
	if err != nil {
		return ExecutionPlanRunPermit{}, err
	}
	if newRun {
		raw, err = json.Marshal(p.Config)
		if err != nil {
			return ExecutionPlanRunPermit{}, err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO execution_plan_runs(id,plan_id,activation_id,execution_date,config_version,snapshot,state,owner_id) VALUES($1,$2,$3,$4,$5,$6,'starting',$7)`, c.RunID, c.PlanID, c.ActivationID, c.ExecutionDate, p.Version, string(raw), c.OwnerID)
	} else {
		_, err = tx.ExecContext(ctx, `UPDATE execution_plan_runs SET state='starting',owner_id=$2,sequence=sequence+1 WHERE id=$1`, c.RunID, c.OwnerID)
	}
	if err != nil {
		return ExecutionPlanRunPermit{}, err
	}
	run, err = scanPlanRun(tx.QueryRowContext(ctx, `SELECT `+executionPlanRunColumns+` FROM execution_plan_runs WHERE id=$1`, c.RunID))
	if err != nil {
		return ExecutionPlanRunPermit{}, err
	}
	result := ExecutionPlanRunPermit{Run: run, Owner: owner}
	raw, err = json.Marshal(result)
	if err != nil {
		return result, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO execution_plan_requests(plan_id,request_id,kind,body_hash,result) VALUES($1,$2,'claim',$3,$4)`, c.PlanID, c.RequestID, hash, string(raw))
	if err != nil {
		return ExecutionPlanRunPermit{}, err
	}
	if err = tx.Commit(); err != nil {
		return ExecutionPlanRunPermit{}, err
	}
	return result, nil
}
