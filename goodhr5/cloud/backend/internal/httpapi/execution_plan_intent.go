// 本文件保存 HRPlus 计划启用与停止意图；云端不判断招聘页面动作和实际启动时间。
package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"time"
)

var executionPlanUUID = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
var ErrExecutionPlanRequest = errors.New("计划请求编号或启用批次不匹配，请刷新后重试")

// ExecutionPlanIntent 将开始方式和原请求绑定，停止必须指定当前启用批次。
type ExecutionPlanIntent struct {
	RequestID       string `json:"request_id"`
	ExpectedVersion int64  `json:"expected_version"`
	ActivationID    string `json:"activation_id,omitempty"`
	StartMode       string `json:"start_mode,omitempty"`
	Action          string `json:"-"`
}

// executionPlanIntentReceipt 保留成功请求的原结果，重试不会创建第二个启用批次。
type executionPlanIntentReceipt struct {
	Hash   string
	Result ExecutionPlan
}

// validate 校验请求结构，是否进入工作时段由本地计划时钟决定。
func (i ExecutionPlanIntent) validate() error {
	if !executionPlanUUID.MatchString(i.RequestID) || i.ExpectedVersion < 1 {
		return ErrExecutionPlanRequest
	}
	if i.Action == "arm" && i.ActivationID == "" && (i.StartMode == "scheduled" || i.StartMode == "immediate") {
		return nil
	}
	if i.Action == "stop" && executionPlanUUID.MatchString(i.ActivationID) && i.StartMode == "" {
		return nil
	}
	return ErrExecutionPlanRequest
}

// executionPlanIntentHash 包含内部动作名称，防止同一编号被挪用到另一接口。
func executionPlanIntentHash(i ExecutionPlanIntent) string {
	raw, _ := json.Marshal([]any{i.Action, i.RequestID, i.ExpectedVersion, i.ActivationID, i.StartMode})
	return accountCredentialHash(string(raw))
}

// applyExecutionPlanIntent 更新用户意图；停止执行中的计划要等待设备安全收尾。
func applyExecutionPlanIntent(p ExecutionPlan, i ExecutionPlanIntent, busy bool) (ExecutionPlan, error) {
	if p.Version != i.ExpectedVersion {
		return p, ErrExecutionPlanVersion
	}
	if i.Action == "arm" {
		if p.State != "stopped" || p.StopRequested || busy {
			return p, ErrExecutionPlanBusy
		}
		activation, err := newExecutionPlanID()
		if err != nil {
			return p, err
		}
		p.ActivationID = activation
		p.State = "enabled"
		p.StopRequested = false
	} else {
		if p.ActivationID != i.ActivationID {
			return p, ErrExecutionPlanRequest
		}
		p.State = "stopped"
		p.StopRequested = busy
	}
	p.StateSequence++
	p.UpdatedAt = time.Now().UTC()
	return p, nil
}

// Intent 在内存配置锁内更新启用与停止，重放返回原批次且不覆盖后来的状态。
func (s *MemoryExecutionPlanStore) Intent(ctx context.Context, tenant, email, id string, i ExecutionPlanIntent) (ExecutionPlan, error) {
	if err := i.validate(); err != nil {
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
	key := id + "/" + i.RequestID
	hash := executionPlanIntentHash(i)
	if r, ok := s.intents[key]; ok {
		if r.Hash != hash {
			return ExecutionPlan{}, ErrExecutionPlanRequest
		}
		return cloneExecutionPlan(r.Result), nil
	}
	busy := false
	for _, run := range s.runs {
		if run.PlanID == id && activeExecutionPlanState(run.State) {
			busy = true
			break
		}
	}
	changed, err := applyExecutionPlanIntent(p, i, busy)
	if err != nil {
		return ExecutionPlan{}, err
	}
	if s.intents == nil {
		s.intents = map[string]executionPlanIntentReceipt{}
	}
	s.plans[id] = cloneExecutionPlan(changed)
	s.intents[key] = executionPlanIntentReceipt{Hash: hash, Result: cloneExecutionPlan(changed)}
	return cloneExecutionPlan(changed), nil
}

// Intent 使用与保存相同的计划行锁，持久化原请求结果并阻止迟到停止影响新批次。
func (s *PostgresExecutionPlanStore) Intent(ctx context.Context, tenant, email, id string, i ExecutionPlanIntent) (ExecutionPlan, error) {
	if err := i.validate(); err != nil {
		return ExecutionPlan{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ExecutionPlan{}, err
	}
	defer tx.Rollback()
	p, err := scanExecutionPlan(tx.QueryRowContext(ctx, `SELECT `+executionPlanColumns+` FROM execution_plans WHERE id=$1 AND COALESCE(tenant_id::text,'')=$2 AND user_email=$3 AND deleted_at IS NULL FOR UPDATE`, id, tenant, email))
	if err != nil {
		return p, err
	}
	var oldHash string
	var result []byte
	err = tx.QueryRowContext(ctx, `SELECT body_hash,result FROM execution_plan_requests WHERE plan_id=$1 AND request_id=$2`, id, i.RequestID).Scan(&oldHash, &result)
	hash := executionPlanIntentHash(i)
	if err == nil {
		if hash != oldHash {
			return p, ErrExecutionPlanRequest
		}
		if err = json.Unmarshal(result, &p); err != nil {
			return p, err
		}
		return p, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return p, err
	}
	var busy bool
	err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM execution_plan_runs r WHERE r.plan_id=$1 AND (r.state NOT IN ('completed','incomplete','stopped','blocked') OR EXISTS(SELECT 1 FROM account_execution_owners o WHERE o.owner_id=r.owner_id)))`, id).Scan(&busy)
	if err != nil {
		return p, err
	}
	p, err = applyExecutionPlanIntent(p, i, busy)
	if err != nil {
		return p, err
	}
	_, err = tx.ExecContext(ctx, `UPDATE execution_plans SET activation_id=$2,state=$3,stop_requested=$4,state_sequence=$5,updated_at=$6 WHERE id=$1`, id, p.ActivationID, p.State, p.StopRequested, p.StateSequence, p.UpdatedAt)
	if err != nil {
		return p, err
	}
	result, err = json.Marshal(p)
	if err != nil {
		return p, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO execution_plan_requests(plan_id,request_id,kind,body_hash,result) VALUES($1,$2,$3,$4,$5)`, id, i.RequestID, i.Action, hash, string(result))
	if err != nil {
		return p, fmt.Errorf("保存计划请求: %w", err)
	}
	if err = tx.Commit(); err != nil {
		return p, err
	}
	return p, nil
}
