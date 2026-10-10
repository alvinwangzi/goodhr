// 本文件定义 HRPlus 计划保存契约及内存实现，配置更新与执行状态在同一锁内校验。
package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"
)

var (
	ErrExecutionPlanVersion = errors.New("计划已被更新，请刷新后再保存")
	ErrExecutionPlanBusy    = errors.New("计划尚未停止并完成收尾，暂不能编辑")
)

// ExecutionPlanStore 只保存当前配置及历史关联，不启动本地或招聘页面。
type ExecutionPlanStore interface {
	Save(context.Context, ExecutionPlan, int64) (ExecutionPlan, error)
	Get(context.Context, string, string, string) (ExecutionPlan, error)
	List(context.Context, string, string) ([]ExecutionPlan, error)
	Delete(context.Context, string, string, string, int64) error
	Intent(context.Context, string, string, string, ExecutionPlanIntent) (ExecutionPlan, error)
	ConfirmStopped(context.Context, string, string, string, ExecutionPlanStopConfirmation) (ExecutionPlan, error)
	ClaimRun(context.Context, string, string, ExecutionPlanRunClaim) (ExecutionPlanRunPermit, error)
	GetRun(context.Context, string, string, string) (ExecutionPlanRun, error)
	UpdateRun(context.Context, string, string, ExecutionPlanRunUpdate) (ExecutionPlanRunPermit, error)
	PrepareItemTask(context.Context, string, string, ExecutionPlanItemTaskRequest) (ExecutionPlanRunPermit, error)
	SaveReport(context.Context, string, string, ExecutionPlanReportSummary, string) (ExecutionPlanReport, error)
	GetReport(context.Context, string, string, string) (ExecutionPlanReport, error)
}

// newExecutionPlanID 生成真实 UUID，内存与 PostgreSQL 使用相同编号格式。
func newExecutionPlanID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	b[6] = (b[6] & 15) | 64
	b[8] = (b[8] & 63) | 128
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[:4], b[4:6], b[6:8], b[8:10], b[10:]), nil
}

// cloneExecutionPlan 深拷贝配置，调用者修改数组不能绕过版本与编辑检查。
func cloneExecutionPlan(p ExecutionPlan) ExecutionPlan {
	raw, _ := json.Marshal(p)
	var copy ExecutionPlan
	_ = json.Unmarshal(raw, &copy)
	return copy
}

// activeExecutionPlanState 只有明确终态才能结束编辑保护，未知状态保守地保留占用。
func activeExecutionPlanState(state string) bool {
	switch state {
	case "completed", "incomplete", "stopped", "blocked":
		return false
	default:
		return true
	}
}

// MemoryExecutionPlanStore 保存独立编排项和运行状态，删除仅隐藏当前配置，保留运行历史。
type MemoryExecutionPlanStore struct {
	mu               sync.Mutex
	plans            map[string]ExecutionPlan
	deleted          map[string]bool
	runs             map[string]ExecutionPlanRun
	intents          map[string]executionPlanIntentReceipt
	owners           map[string]AccountExecutionOwner
	accounts         map[string]string
	ownerHashes      map[string]string
	runClaims        map[string]executionPlanRunClaimReceipt
	positions        *MemoryPositionStore
	runRequestHashes map[string]string
	runUpdates       map[string]executionPlanRunClaimReceipt
	taskRuns         *MemoryTaskRunStore
	reports          map[string]ExecutionPlanReport
}

// NewMemoryExecutionPlanStore 创建开发和契约测试用计划存储。
func NewMemoryExecutionPlanStore() *MemoryExecutionPlanStore {
	return &MemoryExecutionPlanStore{plans: map[string]ExecutionPlan{}, deleted: map[string]bool{}, runs: map[string]ExecutionPlanRun{}}
}

// busyLocked 核对停止意图与所有未结算运行，不能只看长期计划是否启用。
func (s *MemoryExecutionPlanStore) busyLocked(p ExecutionPlan) bool {
	if p.StopRequested || p.State != "stopped" {
		return true
	}
	for _, run := range s.runs {
		if run.PlanID == p.ID {
			_, held := s.owners[run.OwnerID]
			if activeExecutionPlanState(run.State) || held {
				return true
			}
		}
	}
	return false
}

// Save 在同一临界区校验所有者、版本、停止确认，并将保存后的计划保持停止。
func (s *MemoryExecutionPlanStore) Save(ctx context.Context, p ExecutionPlan, expected int64) (ExecutionPlan, error) {
	if err := ctx.Err(); err != nil {
		return ExecutionPlan{}, err
	}
	if err := p.Config.Validate(); err != nil {
		return ExecutionPlan{}, err
	}
	if p.UserEmail == "" || p.MachineID == "" {
		return ExecutionPlan{}, fmt.Errorf("计划缺少所有者或执行电脑")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return ExecutionPlan{}, err
	}
	now := time.Now().UTC()
	if p.ID == "" {
		if expected != 0 {
			return ExecutionPlan{}, ErrExecutionPlanVersion
		}
		id, err := newExecutionPlanID()
		if err != nil {
			return ExecutionPlan{}, err
		}
		p.ID = id
		p.Version = 1
		p.StateSequence = 1
		p.ActivationID = ""
		p.CreatedAt = now
	} else {
		old, ok := s.plans[p.ID]
		if !ok || s.deleted[p.ID] || old.UserEmail != p.UserEmail || old.TenantID != p.TenantID {
			return ExecutionPlan{}, ErrNotFound
		}
		if old.Version != expected {
			return ExecutionPlan{}, ErrExecutionPlanVersion
		}
		if s.busyLocked(old) {
			return ExecutionPlan{}, ErrExecutionPlanBusy
		}
		p.Version = old.Version + 1
		p.StateSequence = old.StateSequence + 1
		p.CreatedAt = old.CreatedAt
		p.ActivationID = old.ActivationID
	}
	p.State = "stopped"
	p.StopRequested = false
	p.UpdatedAt = now
	s.plans[p.ID] = cloneExecutionPlan(p)
	return cloneExecutionPlan(p), nil
}

// Get 按团队与所有者获取计划，不暴露其他用户配置。
func (s *MemoryExecutionPlanStore) Get(ctx context.Context, tenant, email, id string) (ExecutionPlan, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return ExecutionPlan{}, err
	}
	p, ok := s.plans[id]
	if !ok || s.deleted[id] || p.TenantID != tenant || p.UserEmail != email {
		return ExecutionPlan{}, ErrNotFound
	}
	return cloneExecutionPlan(p), nil
}

// List 返回当前用户的未删除计划，配置数组都是独立副本。
func (s *MemoryExecutionPlanStore) List(ctx context.Context, tenant, email string) ([]ExecutionPlan, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	out := []ExecutionPlan{}
	for _, p := range s.plans {
		if !s.deleted[p.ID] && p.TenantID == tenant && p.UserEmail == email {
			out = append(out, cloneExecutionPlan(p))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out, nil
}

// Delete 核对版本与收尾后软删除，历史运行保持不变。
func (s *MemoryExecutionPlanStore) Delete(ctx context.Context, tenant, email, id string, expected int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	p, ok := s.plans[id]
	if !ok || s.deleted[id] || p.TenantID != tenant || p.UserEmail != email {
		return ErrNotFound
	}
	if p.Version != expected {
		return ErrExecutionPlanVersion
	}
	if s.busyLocked(p) {
		return ErrExecutionPlanBusy
	}
	s.deleted[id] = true
	return nil
}
