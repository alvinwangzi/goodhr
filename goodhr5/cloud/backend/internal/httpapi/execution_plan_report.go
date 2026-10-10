// 本文件保存 HRPlus 原执行报告，核对原运行归属和统计，报告失败不修改周期计划状态。
package httpapi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"time"
)

// ExecutionReportAction 区分确认数量、结果待核对、跳过与失败。
type ExecutionReportAction struct {
	State     string `json:"state"`
	Confirmed int64  `json:"confirmed"`
	Unknown   int64  `json:"unknown"`
	Skipped   int64  `json:"skipped"`
	Failed    int64  `json:"failed"`
}

// ExecutionReportItem 保留重复岗位的独立执行项与原 TaskRun。
type ExecutionReportItem struct {
	ID               string                           `json:"id"`
	ItemID           string                           `json:"item_id"`
	TaskRunID        string                           `json:"task_run_id,omitempty"`
	PositionID       string                           `json:"position_id"`
	Order            int                              `json:"order"`
	State            string                           `json:"state"`
	Scanned          int64                            `json:"scanned"`
	DetailsAvailable bool                             `json:"details_available"`
	Actions          map[string]ExecutionReportAction `json:"actions"`
	Information      map[string]ExecutionReportAction `json:"information"`
}

// ExecutionPlanReportSummary 是本地首次生成的原摘要，后续状态补传不覆盖正文。
type ExecutionPlanReportSummary struct {
	SchemaVersion     int                   `json:"schema_version"`
	RunID             string                `json:"run_id"`
	PlanID            string                `json:"plan_id"`
	ActivationID      string                `json:"activation_id"`
	ConfigVersion     int64                 `json:"config_version"`
	RunSequence       int64                 `json:"run_sequence"`
	ExecutionDate     string                `json:"execution_date"`
	PlanName          string                `json:"plan_name"`
	Kind              string                `json:"kind"`
	RunState          string                `json:"run_state"`
	EndReason         string                `json:"end_reason"`
	SyncState         string                `json:"sync_state"`
	GeneratedAt       time.Time             `json:"generated_at"`
	FinishedAt        *time.Time            `json:"finished_at,omitempty"`
	NextNominalAt     *time.Time            `json:"next_nominal_at,omitempty"`
	Items             []ExecutionReportItem `json:"items"`
	UnfinishedItemIDs []string              `json:"unfinished_item_ids"`
}

// ExecutionPlanReport 分离原摘要、当前同步状态与通知状态，未发送不称为已通知。
type ExecutionPlanReport struct {
	RunID             string                     `json:"run_id"`
	BodyHash          string                     `json:"body_hash"`
	Summary           ExecutionPlanReportSummary `json:"summary"`
	SyncState         string                     `json:"sync_state"`
	NotificationState string                     `json:"notification_state"`
	CreatedAt         time.Time                  `json:"created_at"`
	UpdatedAt         time.Time                  `json:"updated_at"`
}

// validateAgainst 对照原运行核对独立执行项及任务身份，不接受跨岗位混算或负数量。
func (r ExecutionPlanReportSummary) validateAgainst(run ExecutionPlanRun) error {
	states := map[string]string{"completed": "completed", "day_incomplete": "incomplete", "stopped": "stopped", "failed": "blocked"}
	expected, ok := states[r.Kind]
	if !ok || r.SchemaVersion != 1 || r.RunID != run.ID || r.PlanID != run.PlanID || r.ActivationID != run.ActivationID || r.ConfigVersion != run.ConfigVersion || r.ExecutionDate != run.ExecutionDate || r.PlanName != run.Snapshot.Name || r.RunState != expected || r.RunSequence < 1 || r.RunSequence > run.Sequence || r.GeneratedAt.IsZero() || len(r.Items) != len(run.Items) {
		return ErrExecutionPlanRequest
	}
	if r.SyncState != "pending" && r.SyncState != "confirmed" {
		return ErrExecutionPlanRequest
	}
	if r.SyncState == "confirmed" && (r.RunState != run.State || activeExecutionPlanState(run.State)) {
		return ErrExecutionPlanRequest
	}
	seen := map[string]bool{}
	for i, item := range r.Items {
		original := run.Items[i]
		if item.ID != original.ID || item.ItemID != original.ItemID || item.TaskRunID != original.TaskRunID || item.PositionID != original.Snapshot.PositionID || item.Order != original.Order || item.Scanned < 0 || len(item.Actions) != len(original.Actions) {
			return ErrExecutionPlanRequest
		}
		switch item.State {
		case "pending", "running", "completed", "failed", "stopped":
		default:
			return ErrExecutionPlanRequest
		}
		if r.SyncState == "confirmed" && item.State != original.State {
			return ErrExecutionPlanRequest
		}
		seen[item.ID] = true
		for action, value := range item.Actions {
			canonical, exists := original.Actions[action]
			if !exists || value.Confirmed < 0 || value.Unknown < 0 || value.Skipped < 0 || value.Failed < 0 {
				return ErrExecutionPlanRequest
			}
			switch value.State {
			case "pending", "active", "completed", "stopped":
			default:
				return ErrExecutionPlanRequest
			}
			if r.SyncState == "confirmed" && value.State != canonical.State {
				return ErrExecutionPlanRequest
			}
			if r.SyncState == "confirmed" && (value.Confirmed != canonical.Count || value.Unknown != canonical.UnknownCount) {
				return ErrExecutionPlanRequest
			}
		}
		for action, value := range item.Information {
			if action != "resume" && action != "phone" && action != "wechat" || value.Confirmed < 0 || value.Unknown < 0 || value.Failed < 0 || value.Skipped < 0 {
				return ErrExecutionPlanRequest
			}
		}
	}
	unfinished := map[string]bool{}
	for _, id := range r.UnfinishedItemIDs {
		if !seen[id] || unfinished[id] {
			return ErrExecutionPlanRequest
		}
		unfinished[id] = true
	}
	return nil
}

// reportHash 对原摘要编码计算稳定摘要，用于拒绝同一运行改写原内容。
func reportHash(r ExecutionPlanReportSummary) (string, []byte, error) {
	raw, err := json.Marshal(r)
	if err != nil {
		return "", nil, err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), raw, nil
}

// cloneExecutionReport 避免内存返回值被调用方修改原摘要。
func cloneExecutionReport(r ExecutionPlanReport) ExecutionPlanReport {
	raw, _ := json.Marshal(r)
	var result ExecutionPlanReport
	_ = json.Unmarshal(raw, &result)
	return result
}

// SaveReport 保存原摘要并独立更新同步状态，不改写计划启用状态。
func (s *MemoryExecutionPlanStore) SaveReport(ctx context.Context, tenant, email string, summary ExecutionPlanReportSummary, syncState string) (ExecutionPlanReport, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return ExecutionPlanReport{}, err
	}
	run, ok := s.runs[summary.RunID]
	p, owned := s.plans[run.PlanID]
	if !ok || !owned || p.UserEmail != email || p.TenantID != tenant {
		return ExecutionPlanReport{}, ErrNotFound
	}
	if syncState != "pending" && syncState != "confirmed" {
		return ExecutionPlanReport{}, ErrExecutionPlanRequest
	}
	if syncState == "confirmed" && activeExecutionPlanState(run.State) {
		return ExecutionPlanReport{}, ErrExecutionPlanRequest
	}
	hash, _, err := reportHash(summary)
	if err != nil {
		return ExecutionPlanReport{}, err
	}
	if old, exists := s.reports[run.ID]; exists {
		if old.BodyHash != hash {
			return ExecutionPlanReport{}, ErrExecutionPlanRequest
		}
		if old.SyncState != "confirmed" {
			old.SyncState = syncState
		}
		old.UpdatedAt = time.Now().UTC()
		s.reports[run.ID] = old
		return cloneExecutionReport(old), nil
	}
	if err := summary.validateAgainst(run); err != nil {
		return ExecutionPlanReport{}, err
	}
	if s.reports == nil {
		s.reports = map[string]ExecutionPlanReport{}
	}
	now := time.Now().UTC()
	result := ExecutionPlanReport{RunID: run.ID, BodyHash: hash, Summary: summary, SyncState: syncState, NotificationState: "pending", CreatedAt: now, UpdatedAt: now}
	s.reports[run.ID] = result
	return cloneExecutionReport(result), nil
}

// GetReport 只读取真实账号和团队拥有的原报告，保留软删除计划的历史。
func (s *MemoryExecutionPlanStore) GetReport(ctx context.Context, tenant, email, id string) (ExecutionPlanReport, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return ExecutionPlanReport{}, err
	}
	run, ok := s.runs[id]
	p, owned := s.plans[run.PlanID]
	if !ok || !owned || p.UserEmail != email || p.TenantID != tenant {
		return ExecutionPlanReport{}, ErrNotFound
	}
	r, ok := s.reports[id]
	if !ok {
		return r, ErrNotFound
	}
	return cloneExecutionReport(r), nil
}

// Report 提供原报告上传与读取，只核对数据，不执行本地流程或发送候选人消息。
func (s *ExecutionPlanService) Report(w http.ResponseWriter, r *http.Request, id string) {
	tenant, email, ok := s.identity(w, r)
	if !ok {
		return
	}
	if r.Method == http.MethodGet {
		result, err := s.store.GetReport(r.Context(), tenant, email, id)
		if err != nil {
			writePlanStoreError(w, err)
			return
		}
		writeJSON(w, 200, map[string]any{"ok": true, "report": result})
		return
	}
	if r.Method != http.MethodPost {
		writeError(w, 405, "此接口只支持报告上传或读取")
		return
	}
	var input struct {
		Summary   ExecutionPlanReportSummary `json:"summary"`
		SyncState string                     `json:"sync_state"`
		MachineID string                     `json:"machine_id"`
	}
	if err := decodePlanBody(w, r, &input); err != nil || input.Summary.RunID != id {
		writeError(w, 400, "请提供完整原报告及执行电脑")
		return
	}
	run, err := s.store.GetRun(r.Context(), tenant, email, id)
	if err != nil {
		writePlanStoreError(w, err)
		return
	}
	p, err := s.store.Get(r.Context(), tenant, email, run.PlanID)
	if err != nil {
		writePlanStoreError(w, err)
		return
	}
	bound, err := s.agents.HasActiveBinding(email, input.MachineID)
	if err != nil || !bound || p.MachineID != input.MachineID {
		writeError(w, 403, "只有指定执行电脑可以上传原报告")
		return
	}
	result, err := s.store.SaveReport(r.Context(), tenant, email, input.Summary, input.SyncState)
	if err != nil {
		writePlanStoreError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "report": result})
}
