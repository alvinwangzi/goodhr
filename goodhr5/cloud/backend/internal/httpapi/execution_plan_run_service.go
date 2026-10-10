// 本文件接收 HRPlus 指定电脑的计划运行领取请求，使用真实权限并只保存运行事实。
package httpapi

import (
	"errors"
	"net/http"
	"strings"
)

// ClaimRun 复用设备、会员、岗位和余额核对，许可只代表 starting，不代表页面已经开始。
func (s *ExecutionPlanService) ClaimRun(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, 405, "此接口只支持 POST")
		return
	}
	tenant, email, ok := s.identity(w, r)
	if !ok {
		return
	}
	var input ExecutionPlanRunClaim
	if err := decodePlanBody(w, r, &input); err != nil || input.validate() != nil {
		writeError(w, 400, "运行领取需包含原计划、批次、设备、请求编号和本地预留确认")
		return
	}
	plan, err := s.store.Get(r.Context(), tenant, email, input.PlanID)
	if err != nil {
		writePlanStoreError(w, err)
		return
	}
	if plan.MachineID != input.MachineID {
		writeError(w, 403, "只有指定执行电脑可以领取计划运行")
		return
	}
	if !s.validatePlanPermissions(w, tenant, email, input.MachineID, plan.Config) {
		return
	}
	if s.execution == nil {
		writeError(w, 503, "计划运行权限暂时无法核对")
		return
	}
	for _, item := range plan.Config.Items {
		position, e := s.positions.store.PositionByID(tenant, email, item.PositionID, false)
		if e != nil || position.UserEmail != email {
			writeError(w, 403, "计划含无权执行或已删除的岗位")
			return
		}
		if failure := s.execution.checkPositionStart(email, position, strings.Join(item.Actions, ",")); failure != nil {
			writePositionStartError(w, failure.status, failure.code, failure.message)
			return
		}
	}
	result, err := s.store.ClaimRun(r.Context(), tenant, email, input)
	if err != nil {
		if errors.Is(err, ErrAccountExecutionBusy) || errors.Is(err, ErrAccountExecutionConflict) || errors.Is(err, ErrAccountExecutionReleased) {
			code := "REQUEST_CONFLICT"
			if errors.Is(err, ErrAccountExecutionBusy) {
				code = "EXECUTION_BUSY"
			}
			if errors.Is(err, ErrAccountExecutionReleased) {
				code = "LEASE_RELEASED"
			}
			writeJSON(w, 409, map[string]any{"ok": false, "error": err.Error(), "error_code": code})
			return
		}
		writePlanStoreError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "permit": result})
}

// Run 提供运行快照、递增状态与收尾释放；释放不因设备重新绑定而跳过占用凭证核对。
func (s *ExecutionPlanService) Run(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/execution-plan-runs/"), "/")
	if len(parts) == 4 && parts[1] == "items" && parts[3] == "logs" && executionPlanUUID.MatchString(parts[0]) && executionPlanUUID.MatchString(parts[2]) {
		s.itemLogs(w, r, parts[0], parts[2])
		return
	}
	if len(parts) == 2 && parts[1] == "report" && executionPlanUUID.MatchString(parts[0]) {
		s.Report(w, r, parts[0])
		return
	}
	if len(parts) == 4 && parts[1] == "items" && parts[3] == "prepare" && executionPlanUUID.MatchString(parts[0]) && executionPlanUUID.MatchString(parts[2]) {
		s.PrepareItem(w, r, parts[0], parts[2])
		return
	}
	if len(parts) > 2 || !executionPlanUUID.MatchString(parts[0]) || (len(parts) == 2 && parts[1] != "status" && parts[1] != "release") {
		writeError(w, 404, "运行接口不存在")
		return
	}
	if (len(parts) == 1 && r.Method != http.MethodGet) || (len(parts) == 2 && r.Method != http.MethodPost) {
		writeError(w, 405, "此接口不支持当前请求方式")
		return
	}
	tenant, email, ok := s.identity(w, r)
	if !ok {
		return
	}
	run, err := s.store.GetRun(r.Context(), tenant, email, parts[0])
	if err != nil {
		writePlanStoreError(w, err)
		return
	}
	if len(parts) == 1 {
		writeJSON(w, 200, map[string]any{"ok": true, "run": run})
		return
	}
	var input ExecutionPlanRunUpdate
	if decodePlanBody(w, r, &input) != nil {
		writeError(w, 400, "运行状态格式不正确或含不支持字段")
		return
	}
	input.PlanID = run.PlanID
	input.RunID = run.ID
	input.Action = parts[1]
	if err = input.validate(); err != nil {
		writeError(w, 400, "运行状态需包含原请求、占用凭证、序号和收尾确认")
		return
	}
	// 继续执行必须仍为绑定设备；失去绑定的原电脑仍可凭原占用证明完成收尾释放。
	if input.State == "running" {
		if s.execution == nil {
			writeError(w, 503, "执行设备暂时无法核对")
			return
		}
		if failure := s.execution.verifyActiveDevice(email, input.MachineID); failure != nil {
			writePositionStartError(w, failure.status, failure.code, failure.message)
			return
		}
	}
	result, err := s.store.UpdateRun(r.Context(), tenant, email, input)
	if err != nil {
		writePlanStoreError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "permit": result})
}

// PrepareItem 复用真实设备和岗位启动校验，父占用下只准备当前项的数据关联，不启动页面。
func (s *ExecutionPlanService) PrepareItem(w http.ResponseWriter, r *http.Request, runID, itemID string) {
	if r.Method != http.MethodPost {
		writeError(w, 405, "此接口只支持 POST")
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
	var input ExecutionPlanItemTaskRequest
	if decodePlanBody(w, r, &input) != nil {
		writeError(w, 400, "执行项准备格式不正确")
		return
	}
	input.PlanID = run.PlanID
	input.RunID = run.ID
	input.ItemRunID = itemID
	if input.validate() != nil {
		writeError(w, 400, "执行项准备缺少原编号或占用证明")
		return
	}
	if s.execution == nil {
		writeError(w, 503, "执行权限暂时无法核对")
		return
	}
	if failure := s.execution.verifyActiveDevice(email, input.MachineID); failure != nil {
		writePositionStartError(w, failure.status, failure.code, failure.message)
		return
	}
	var item *ExecutionPlanItemRun
	for index := range run.Items {
		if run.Items[index].ID == itemID {
			item = &run.Items[index]
			break
		}
	}
	if item == nil {
		writePlanStoreError(w, ErrNotFound)
		return
	}
	position, err := s.positions.store.PositionByID(tenant, email, item.Snapshot.PositionID, false)
	if err != nil || position.UserEmail != email {
		writeError(w, 403, "岗位不存在或没有执行权限")
		return
	}
	if failure := s.execution.checkPositionStart(email, position, strings.Join(item.Snapshot.Actions, ",")); failure != nil {
		writePositionStartError(w, failure.status, failure.code, failure.message)
		return
	}
	result, err := s.store.PrepareItemTask(r.Context(), tenant, email, input)
	if err != nil {
		writePlanStoreError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "permit": result})
}
