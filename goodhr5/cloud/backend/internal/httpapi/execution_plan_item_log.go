// 本文件保存和读取 HRPlus 原执行项日志，原内容去重、原账号授权和历史来源核对均不执行招聘动作。
package httpapi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// ExecutionPlanItemLog 与原本地安全日志字段一致，ID 在上传时是原流水，在读取时是云端分页流水。
type ExecutionPlanItemLog struct {
	ID         int64  `json:"id"`
	PlanRunID  string `json:"plan_run_id"`
	ItemRunID  string `json:"item_run_id"`
	TaskRunID  string `json:"task_run_id"`
	LocalRunID string `json:"local_run_id"`
	PositionID string `json:"position_id"`
	Level      string `json:"level"`
	Message    string `json:"message"`
	CreatedAt  string `json:"created_at"`
}

// planLogHash 固定完整原日志内容，用于逐条去重和整批回执核对。
func planLogHash(value any) string {
	raw, _ := json.Marshal(value)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// validatePlanLogs 核对原运行原项原任务，拒绝把另一项的候选人日志混入当前项。
func validatePlanLogs(run ExecutionPlanRun, itemID string, logs []ExecutionPlanItemLog) error {
	if len(logs) < 1 || len(logs) > 100 {
		return ErrExecutionPlanRequest
	}
	var item *ExecutionPlanItemRun
	for index := range run.Items {
		if run.Items[index].ID == itemID {
			item = &run.Items[index]
		}
	}
	if item == nil || item.TaskRunID == "" {
		return ErrExecutionPlanRequest
	}
	seen := map[int64]bool{}
	for _, entry := range logs {
		if entry.ID < 1 || seen[entry.ID] || entry.PlanRunID != run.ID || entry.ItemRunID != item.ID || entry.TaskRunID != item.TaskRunID || entry.LocalRunID != item.ID || entry.PositionID != item.Snapshot.PositionID || strings.TrimSpace(entry.Message) == "" || len(entry.Message) > 65536 {
			return ErrExecutionPlanRequest
		}
		if _, err := time.Parse(time.RFC3339Nano, entry.CreatedAt); err != nil {
			return ErrExecutionPlanRequest
		}
		if entry.Level != "info" && entry.Level != "warning" && entry.Level != "error" && entry.Level != "debug" {
			return ErrExecutionPlanRequest
		}
		seen[entry.ID] = true
	}
	return nil
}

// itemLogs 提供只读分页及原电脑批量上传，上传不会修改任务计数或角色状态。
func (s *ExecutionPlanService) itemLogs(w http.ResponseWriter, r *http.Request, runID, itemID string) {
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		writeError(w, 405, "此接口只支持原日志读取或补传")
		return
	}
	tenant, email, ok := s.identity(w, r)
	if !ok {
		return
	}
	run, err := s.store.GetRun(r.Context(), tenant, email, runID)
	if err != nil {
		writePlanStoreError(w, err)
		return
	}
	task := ""
	for _, item := range run.Items {
		if item.ID == itemID {
			task = item.TaskRunID
		}
	}
	if task == "" {
		writeError(w, 404, "原执行项不存在或尚未准备任务")
		return
	}
	if r.Method == http.MethodGet {
		before := int64(0)
		limit := 100
		if raw := r.URL.Query().Get("before"); raw != "" {
			before, err = strconv.ParseInt(raw, 10, 64)
			if err != nil || before < 0 {
				writeError(w, 400, "日志分页编号不正确")
				return
			}
		}
		if raw := r.URL.Query().Get("limit"); raw != "" {
			limit, err = strconv.Atoi(raw)
			if err != nil || limit < 1 || limit > 200 {
				writeError(w, 400, "每次读取一至两百条日志")
				return
			}
		}
		logs, err := s.store.ListItemLogs(r.Context(), tenant, email, runID, itemID, before, limit+1)
		if err != nil {
			writePlanStoreError(w, err)
			return
		}
		next := int64(0)
		if len(logs) > limit {
			logs = logs[:limit]
			next = logs[len(logs)-1].ID
		}
		writeJSON(w, 200, map[string]any{"ok": true, "run_id": runID, "item_run_id": itemID, "task_run_id": task, "logs": logs, "next_before": next, "message": "云端已同步的原任务日志；执行电脑尚未补传的记录不会出现在这里"})
		return
	}
	var input struct {
		MachineID string                 `json:"machine_id"`
		Logs      []ExecutionPlanItemLog `json:"logs"`
	}
	if decodePlanBody(w, r, &input) != nil || validatePlanLogs(run, itemID, input.Logs) != nil {
		writeError(w, 400, "日志内容或原执行项来源不一致")
		return
	}
	bound, err := s.agents.HasActiveBinding(email, input.MachineID)
	if err != nil || !bound {
		writeError(w, 403, "请连接原执行电脑再补传日志")
		return
	}
	if err := s.store.VerifyReportMachine(r.Context(), tenant, email, runID, input.MachineID); err != nil {
		writeError(w, 403, "日志不属于这台原执行电脑")
		return
	}
	if err := s.store.AppendItemLogs(r.Context(), tenant, email, runID, itemID, input.MachineID, input.Logs); err != nil {
		writePlanStoreError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "run_id": runID, "item_run_id": itemID, "task_run_id": task, "batch_hash": planLogHash(input.Logs), "count": len(input.Logs)})
}

// AppendItemLogs 在内存锁内核对完整原来源后整批保存，原编号任何内容变化都会拒绝整个批次。
func (s *MemoryExecutionPlanStore) AppendItemLogs(ctx context.Context, tenant, email, runID, itemID, machine string, logs []ExecutionPlanItemLog) error {
	if err := s.VerifyReportMachine(ctx, tenant, email, runID, machine); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	run, exists := s.runs[runID]
	plan, owned := s.plans[run.PlanID]
	if !exists || !owned || plan.UserEmail != email || plan.TenantID != tenant {
		return ErrNotFound
	}
	if err := validatePlanLogs(run, itemID, logs); err != nil {
		return err
	}
	if s.itemLogs == nil {
		s.itemLogs = map[string]storedPlanItemLog{}
	}
	for _, entry := range logs {
		key := runID + "/" + itemID + "/" + entry.LocalRunID + "/" + strconv.FormatInt(entry.ID, 10)
		if old, exists := s.itemLogs[key]; exists && (old.hash != planLogHash(entry) || old.machine != machine) {
			return ErrExecutionPlanRequest
		}
	}
	for _, entry := range logs {
		key := runID + "/" + itemID + "/" + entry.LocalRunID + "/" + strconv.FormatInt(entry.ID, 10)
		if _, exists := s.itemLogs[key]; exists {
			continue
		}
		s.itemLogSequence++
		hash := planLogHash(entry)
		entry.ID = s.itemLogSequence
		s.itemLogs[key] = storedPlanItemLog{entry, machine, hash}
	}
	return nil
}

// storedPlanItemLog 分开云端分页与原内容摘要，不把分页编号当作原本地流水。
type storedPlanItemLog struct {
	entry         ExecutionPlanItemLog
	machine, hash string
}
