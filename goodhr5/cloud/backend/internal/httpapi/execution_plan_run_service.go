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
			writeError(w, 409, err.Error())
			return
		}
		writePlanStoreError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "permit": result})
}
