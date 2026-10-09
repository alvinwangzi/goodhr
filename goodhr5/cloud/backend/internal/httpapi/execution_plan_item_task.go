// 本文件在父计划占用下准备 HRPlus 独立岗位 TaskRun，重复岗位保留不同任务且不另外抢账号锁。
package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
)

// ExecutionPlanItemTaskRequest 绑定原父运行、执行项与占用证明，客户端不能指定另一个岗位或 TaskRun。
type ExecutionPlanItemTaskRequest struct {
	PlanID     string `json:"-"`
	RunID      string `json:"-"`
	ItemRunID  string `json:"-"`
	RequestID  string `json:"request_id"`
	OwnerID    string `json:"owner_id"`
	MachineID  string `json:"machine_id"`
	Credential string `json:"credential"`
}

// validate 校验原编号与占用证明，实际身份由计划服务的真实登录决定。
func (i ExecutionPlanItemTaskRequest) validate() error {
	for _, id := range []string{i.PlanID, i.RunID, i.ItemRunID, i.RequestID, i.OwnerID} {
		if !executionPlanUUID.MatchString(id) {
			return ErrExecutionPlanRequest
		}
	}
	if i.MachineID == "" || len(i.Credential) < 32 {
		return ErrExecutionPlanRequest
	}
	return nil
}

// claim 将执行项准备转换成父占用的核对参数，不申请第二份账号占用。
func (i ExecutionPlanItemTaskRequest) claim(email string) AccountExecutionClaim {
	return AccountExecutionClaim{UserEmail: email, MachineID: i.MachineID, OwnerType: "plan", OwnerID: i.OwnerID, RequestID: i.RequestID, Credential: i.Credential, LocalReserved: true}
}

// planItemTaskHash 绑定全部路由和证明，摘要回执不保存凭证原文。
func planItemTaskHash(i ExecutionPlanItemTaskRequest) string {
	i.Credential = accountCredentialHash(i.Credential)
	raw, _ := json.Marshal([]any{"prepare_item", i.PlanID, i.RunID, i.ItemRunID, i})
	return accountCredentialHash(string(raw))
}

// preparedItemIndex 只允许当前主执行项准备；已结束或未来项不能借父占用提前启动。
func preparedItemIndex(p ExecutionPlan, r ExecutionPlanRun, i ExecutionPlanItemTaskRequest) (int, error) {
	if p.State != "enabled" || p.StopRequested || p.ActivationID != r.ActivationID || p.MachineID != i.MachineID || r.OwnerID != i.OwnerID || (r.State != "starting" && r.State != "running") {
		return 0, ErrExecutionPlanBusy
	}
	for index, item := range r.Items {
		if item.ID == i.ItemRunID {
			if index != r.CurrentItem || (item.State != "pending" && item.State != "running") {
				return 0, ErrExecutionPlanSequence
			}
			return index, nil
		}
	}
	return 0, ErrNotFound
}

// PrepareItemTask 在计划、岗位和任务锁内建立内存任务关联，不把 starting 写成实际运行。
func (s *MemoryExecutionPlanStore) PrepareItemTask(ctx context.Context, tenant, email string, i ExecutionPlanItemTaskRequest) (ExecutionPlanRunPermit, error) {
	if err := i.validate(); err != nil {
		return ExecutionPlanRunPermit{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return ExecutionPlanRunPermit{}, err
	}
	p, owned := s.plans[i.PlanID]
	r, found := s.runs[i.RunID]
	if !owned || !found || s.deleted[i.PlanID] || p.TenantID != tenant || p.UserEmail != email || r.PlanID != p.ID {
		return ExecutionPlanRunPermit{}, ErrNotFound
	}
	if s.positions == nil || s.taskRuns == nil {
		return ExecutionPlanRunPermit{}, errors.New("任务存储未连接")
	}
	s.positions.mu.Lock()
	defer s.positions.mu.Unlock()
	s.taskRuns.mu.Lock()
	defer s.taskRuns.mu.Unlock()
	key := i.PlanID + "/" + i.RequestID
	hash := planItemTaskHash(i)
	if _, used := s.intents[key]; used {
		return ExecutionPlanRunPermit{}, ErrExecutionPlanRequest
	}
	if _, used := s.runClaims[key]; used {
		return ExecutionPlanRunPermit{}, ErrExecutionPlanRequest
	}
	if s.accounts[email] != i.OwnerID || s.ownerHashes[i.OwnerID] != accountCredentialHash(i.Credential) {
		return ExecutionPlanRunPermit{}, ErrAccountExecutionProof
	}
	index, err := preparedItemIndex(p, r, i)
	if err != nil {
		return ExecutionPlanRunPermit{}, err
	}
	if prior, exists := s.runUpdates[key]; exists {
		if prior.Hash != hash {
			return ExecutionPlanRunPermit{}, ErrExecutionPlanRequest
		}
		return clonePlanRunPermit(prior.Result), nil
	}
	r = clonePlanRunPermit(ExecutionPlanRunPermit{Run: r}).Run
	item := r.Items[index]
	position, exists := s.positions.positions[item.Snapshot.PositionID]
	if !exists || position.UserEmail != email {
		return ExecutionPlanRunPermit{}, ErrNotFound
	}
	if item.TaskRunID == "" {
		task, err := s.taskRuns.createTaskRunLocked(TaskRun{TenantID: tenant, UserEmail: email, PositionID: position.ID, PositionName: position.Name, PlatformID: position.PlatformID, TaskType: strings.Join(item.Snapshot.Actions, ","), MachineID: i.MachineID, Status: "starting"})
		if err != nil {
			return ExecutionPlanRunPermit{}, err
		}
		r.Items[index].TaskRunID = task.ID
		r.Sequence++
		s.runs[r.ID] = r
	}
	result := ExecutionPlanRunPermit{Run: r, Owner: s.owners[i.OwnerID]}
	if s.runUpdates == nil {
		s.runUpdates = map[string]executionPlanRunClaimReceipt{}
	}
	s.runUpdates[key] = executionPlanRunClaimReceipt{Hash: hash, Result: clonePlanRunPermit(result)}
	return clonePlanRunPermit(result), nil
}

// PrepareItemTask 在同一账号和计划运行事务中插入 TaskRun 与关联，任何失败都不留下孤立任务。
func (s *PostgresExecutionPlanStore) PrepareItemTask(ctx context.Context, tenant, email string, i ExecutionPlanItemTaskRequest) (ExecutionPlanRunPermit, error) {
	if err := i.validate(); err != nil {
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
	p, err := scanExecutionPlan(tx.QueryRowContext(ctx, `SELECT `+executionPlanColumns+` FROM execution_plans WHERE id=$1 AND COALESCE(tenant_id::text,'')=$2 AND user_email=$3 AND deleted_at IS NULL FOR UPDATE`, i.PlanID, tenant, email))
	if err != nil {
		return ExecutionPlanRunPermit{}, err
	}
	r, err := scanPlanRun(tx.QueryRowContext(ctx, `SELECT `+executionPlanRunColumns+` FROM execution_plan_runs WHERE id=$1 AND plan_id=$2 FOR UPDATE`, i.RunID, i.PlanID))
	if errors.Is(err, sql.ErrNoRows) {
		return ExecutionPlanRunPermit{}, ErrNotFound
	}
	if err != nil {
		return ExecutionPlanRunPermit{}, err
	}
	r, err = loadPlanItemRuns(ctx, tx, r)
	if err != nil {
		return ExecutionPlanRunPermit{}, err
	}
	owner, err := accountOwnerProof(ctx, tx, i.claim(email))
	if err != nil {
		return ExecutionPlanRunPermit{}, err
	}
	index, err := preparedItemIndex(p, r, i)
	if err != nil {
		return ExecutionPlanRunPermit{}, err
	}
	hash := planItemTaskHash(i)
	var oldHash string
	var raw []byte
	err = tx.QueryRowContext(ctx, `SELECT body_hash,result FROM execution_plan_requests WHERE plan_id=$1 AND request_id=$2`, i.PlanID, i.RequestID).Scan(&oldHash, &raw)
	if err == nil {
		if oldHash != hash {
			return ExecutionPlanRunPermit{}, ErrExecutionPlanRequest
		}
		var result ExecutionPlanRunPermit
		err = json.Unmarshal(raw, &result)
		return result, err
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return ExecutionPlanRunPermit{}, err
	}
	item := r.Items[index]
	if item.TaskRunID == "" {
		var taskID string
		err = tx.QueryRowContext(ctx, `INSERT INTO task_runs(tenant_id,user_id,position_id,platform_id,task_type,machine_id,status,started_at) SELECT $1::uuid,u.id,p.id,p.platform_id,$4,$5,'starting',NULL FROM positions p JOIN users u ON u.id=p.user_id WHERE p.id=$2 AND u.email=$3 RETURNING id::text`, tenant, item.Snapshot.PositionID, email, strings.Join(item.Snapshot.Actions, ","), i.MachineID).Scan(&taskID)
		if errors.Is(err, sql.ErrNoRows) {
			return ExecutionPlanRunPermit{}, ErrNotFound
		}
		if err != nil {
			return ExecutionPlanRunPermit{}, err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE execution_plan_item_runs SET task_run_id=$2,updated_at=NOW() WHERE id=$1`, item.ID, taskID); err != nil {
			return ExecutionPlanRunPermit{}, err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE execution_plan_runs SET sequence=sequence+1 WHERE id=$1`, r.ID); err != nil {
			return ExecutionPlanRunPermit{}, err
		}
		r, err = scanPlanRun(tx.QueryRowContext(ctx, `SELECT `+executionPlanRunColumns+` FROM execution_plan_runs WHERE id=$1`, r.ID))
		if err != nil {
			return ExecutionPlanRunPermit{}, err
		}
		r, err = loadPlanItemRuns(ctx, tx, r)
		if err != nil {
			return ExecutionPlanRunPermit{}, err
		}
	}
	result := ExecutionPlanRunPermit{Run: r, Owner: owner}
	raw, err = json.Marshal(result)
	if err != nil {
		return result, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO execution_plan_requests(plan_id,request_id,kind,body_hash,result) VALUES($1,$2,'prepare_item',$3,$4)`, i.PlanID, i.RequestID, hash, string(raw)); err != nil {
		return ExecutionPlanRunPermit{}, err
	}
	if err = tx.Commit(); err != nil {
		return ExecutionPlanRunPermit{}, err
	}
	return result, nil
}
