// 本文件负责定义执行任务运行记录的数据模型、存储接口和内存实现。
// 执行任务表示一次岗位启动，用于后台展示本次运行的状态、统计和候选人名单。
package httpapi

import (
	"strings"
	"sync"
	"time"
)

// TaskRun 表示一次岗位启动产生的执行任务记录。
type TaskRun struct {
	ID                   string     `json:"id"`
	TenantID             string     `json:"tenant_id"`
	UserEmail            string     `json:"user_email"`
	PositionID           string     `json:"position_id"`
	PositionName         string     `json:"position_name"`
	PlatformID           string     `json:"platform_id"`
	TaskType             string     `json:"task_type"`
	MachineID            string     `json:"machine_id"`
	Status               string     `json:"status"`
	ScannedCount         int        `json:"scanned_count"`
	GreetedCount         int        `json:"greeted_count"`
	ResumeRequestedCount int        `json:"resume_requested_count"`
	SkippedCount         int        `json:"skipped_count"`
	FailedCount          int        `json:"failed_count"`
	ErrorMessage         string     `json:"error_message"`
	CreatedAt            time.Time  `json:"created_at"`
	StartedAt            *time.Time `json:"started_at,omitempty"`
	FinishedAt           *time.Time `json:"finished_at"`
}

// TaskRunCandidate 表示执行任务名单里的一条候选人记录。
type TaskRunCandidate struct {
	CandidateID    string     `json:"candidate_id"`
	CandidateName  string     `json:"candidate_name"`
	Phone          string     `json:"phone"`
	AIDetailScore  *float64   `json:"ai_detail_score"`
	AIDetailReason string     `json:"ai_detail_reason"`
	AIGreetScore   *float64   `json:"ai_greet_score"`
	AIGreetReason  string     `json:"ai_greet_reason"`
	ActionAt       *time.Time `json:"action_at"`
	MessageText    string     `json:"message_text"`
}

// TaskRunListResult 表示执行任务分页查询结果。
type TaskRunListResult struct {
	Items    []TaskRun `json:"items"`
	Total    int       `json:"total"`
	Page     int       `json:"page"`
	PageSize int       `json:"page_size"`
}

// TaskRunStore 定义执行任务运行记录的持久化能力。
type TaskRunStore interface {
	// CreateTaskRun 创建一条运行记录并返回带 ID 的结果。
	CreateTaskRun(run TaskRun) (TaskRun, error)
	// FinishTaskRun 结束运行并重算本运行的候选人统计。
	FinishTaskRun(runID string, status string, errorMessage string, skipped int, failed int) error
	// ActiveTaskRunByPosition 返回岗位当前运行中的记录，没有时返回 ErrNotFound。
	ActiveTaskRunByPosition(positionID string) (TaskRun, error)
	// TaskRunByID 读取团队内一条运行记录详情。
	TaskRunByID(tenantID string, runID string) (TaskRun, error)
	// ListTaskRuns 按团队分页读取运行记录列表。
	ListTaskRuns(tenantID string, userEmail string, isAdmin bool, page int, pageSize int) (TaskRunListResult, error)
	// TaskRunCandidates 按名单类型读取本次运行的候选人名单，filter 支持 greeted 和 resume。
	TaskRunCandidates(tenantID string, runID string, filter string) ([]TaskRunCandidate, error)
}

// MemoryTaskRunStore 提供开发期使用的内存执行任务存储。
type MemoryTaskRunStore struct {
	mu        sync.Mutex
	runs      map[string]TaskRun
	runEvents map[string]map[string]TaskRunCandidate
	now       func() time.Time
}

// NewMemoryTaskRunStore 创建开发期内存执行任务存储。
func NewMemoryTaskRunStore() *MemoryTaskRunStore {
	return &MemoryTaskRunStore{
		runs:      make(map[string]TaskRun),
		runEvents: make(map[string]map[string]TaskRunCandidate),
		now:       time.Now,
	}
}

// SaveMemoryRunCandidate 预置一条名单记录，仅供测试和开发环境使用。
// runID 为运行 ID，filter 为名单类型 greeted 或 resume，candidate 为名单条目。
func (s *MemoryTaskRunStore) SaveMemoryRunCandidate(runID string, filter string, candidate TaskRunCandidate) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.runEvents[runID] == nil {
		s.runEvents[runID] = make(map[string]TaskRunCandidate)
	}
	s.runEvents[runID][filter+"/"+candidate.CandidateID] = candidate
}

// CreateTaskRun 创建一条内存运行记录。
// run 为运行记录内容，返回保存后的记录。
func (s *MemoryTaskRunStore) CreateTaskRun(run TaskRun) (TaskRun, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.createTaskRunLocked(run)
}

// createTaskRunLocked 复用当前任务锁生成独立 UUID，同秒创建的重复岗位不会覆盖原记录。
func (s *MemoryTaskRunStore) createTaskRunLocked(run TaskRun) (TaskRun, error) {
	now := s.now()
	id, err := newExecutionPlanID()
	if err != nil {
		return TaskRun{}, err
	}
	run.ID = id
	run.CreatedAt = now
	if run.Status != "starting" {
		run.StartedAt = &now
	}
	if run.Status == "" {
		run.Status = "running"
	}
	if run.TaskType == "" {
		run.TaskType = "greeting"
	}
	s.runs[run.ID] = run
	return run, nil
}

// FinishTaskRun 结束内存运行记录并按事件重算统计。
// runID 为运行 ID，status 为结束状态，skipped 和 failed 为本地同步的计数。
func (s *MemoryTaskRunStore) FinishTaskRun(runID string, status string, errorMessage string, skipped int, failed int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	run, ok := s.runs[runID]
	if !ok {
		return ErrNotFound
	}
	now := s.now()
	run.Status = status
	run.ErrorMessage = strings.TrimSpace(errorMessage)
	run.SkippedCount = maxIntValue(0, skipped)
	run.FailedCount = maxIntValue(0, failed)
	run.FinishedAt = &now
	s.runs[runID] = run
	return nil
}

// ActiveTaskRunByPosition 返回内存中岗位运行中的记录。
// positionID 为岗位 ID，没有运行中记录时返回 ErrNotFound。
func (s *MemoryTaskRunStore) ActiveTaskRunByPosition(positionID string) (TaskRun, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var latest TaskRun
	found := false
	for _, run := range s.runs {
		if run.PositionID != positionID || run.Status != "running" {
			continue
		}
		if !found || run.CreatedAt.After(latest.CreatedAt) {
			latest = run
			found = true
		}
	}
	if !found {
		return TaskRun{}, ErrNotFound
	}
	return latest, nil
}

// TaskRunByID 读取内存中一条运行记录。
// tenantID 为团队 ID，runID 为运行 ID。
func (s *MemoryTaskRunStore) TaskRunByID(tenantID string, runID string) (TaskRun, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	run, ok := s.runs[runID]
	if !ok || (tenantID != "" && run.TenantID != tenantID) {
		return TaskRun{}, ErrNotFound
	}
	return run, nil
}

// ListTaskRuns 按团队分页返回内存运行记录列表。
// tenantID 为团队 ID，userEmail 为普通用户过滤邮箱，isAdmin 为管理员时不过滤。
func (s *MemoryTaskRunStore) ListTaskRuns(tenantID string, userEmail string, isAdmin bool, page int, pageSize int) (TaskRunListResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	page, pageSize = normalizeCandidatePage(page, pageSize)
	items := make([]TaskRun, 0)
	for _, run := range s.runs {
		if tenantID != "" && run.TenantID != tenantID {
			continue
		}
		if !isAdmin && userEmail != "" && !strings.EqualFold(run.UserEmail, userEmail) {
			continue
		}
		items = append(items, run)
	}
	// 按创建时间倒序排列，最新任务展示在最前面。
	for i := 0; i < len(items); i++ {
		for j := i + 1; j < len(items); j++ {
			if items[j].CreatedAt.After(items[i].CreatedAt) {
				items[i], items[j] = items[j], items[i]
			}
		}
	}
	total := len(items)
	start := (page - 1) * pageSize
	if start >= total {
		items = []TaskRun{}
	} else {
		end := start + pageSize
		if end > total {
			end = total
		}
		items = items[start:end]
	}
	return TaskRunListResult{Items: items, Total: total, Page: page, PageSize: pageSize}, nil
}

// TaskRunCandidates 返回内存名单，按预置的名单记录过滤。
// tenantID 为团队 ID，runID 为运行 ID，filter 支持 greeted 和 resume。
func (s *MemoryTaskRunStore) TaskRunCandidates(tenantID string, runID string, filter string) ([]TaskRunCandidate, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.runs[runID]; !ok {
		return nil, ErrNotFound
	}
	items := make([]TaskRunCandidate, 0)
	for key, candidate := range s.runEvents[runID] {
		if strings.HasPrefix(key, filter+"/") {
			items = append(items, candidate)
		}
	}
	return items, nil
}
