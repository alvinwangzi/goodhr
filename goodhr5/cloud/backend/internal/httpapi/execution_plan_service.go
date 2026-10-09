// 本文件提供 HRPlus 执行计划配置 API，复用真实登录、设备、岗位和会员校验，不执行浏览器动作。
package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
)

// ExecutionPlanService 负责配置读写；运行领取与状态上报使用后续独立执行契约。
type ExecutionPlanService struct {
	positions *PositionService
	agents    AgentStore
	store     ExecutionPlanStore
}

// NewExecutionPlanService 复用岗位服务的登录与权限依赖。
func NewExecutionPlanService(positions *PositionService, agents AgentStore, store ExecutionPlanStore) *ExecutionPlanService {
	return &ExecutionPlanService{positions: positions, agents: agents, store: store}
}

// identity 以真实会话决定所有者与团队，不接受客户端传入用户归属。
func (s *ExecutionPlanService) identity(w http.ResponseWriter, r *http.Request) (string, string, bool) {
	session, ok := s.positions.currentSession(w, r)
	if !ok {
		return "", "", false
	}
	tenant := ""
	if s.positions.auth.tenantStore != nil {
		value, err := s.positions.auth.tenantStore.GetOrCreateTenant(session.Email)
		if err != nil {
			writeError(w, 500, "团队信息暂时无法读取")
			return "", "", false
		}
		tenant = value.ID
	}
	return tenant, session.Email, true
}

// decodePlanBody 限制请求大小并拒绝未知字段，防止把凭证或伪造状态混入配置。
func decodePlanBody(w http.ResponseWriter, r *http.Request, value any) error {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return errors.New("请求只能包含一份计划配置")
	}
	return nil
}

// writePlanStoreError 保留版本和运行冲突的明确提示，不向前端输出数据库内部错误。
func writePlanStoreError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrNotFound):
		writeError(w, 404, "计划不存在或没有权限")
	case errors.Is(err, ErrExecutionPlanVersion), errors.Is(err, ErrExecutionPlanBusy), errors.Is(err, ErrExecutionPlanRequest):
		writeError(w, 409, err.Error())
	default:
		writeError(w, 500, "计划暂时无法保存或读取，请稍后重试")
	}
}

// Collection 读取当前用户计划或保存停止状态的编排，保存不触发本地运行。
func (s *ExecutionPlanService) Collection(w http.ResponseWriter, r *http.Request) {
	tenant, email, ok := s.identity(w, r)
	if !ok {
		return
	}
	if r.Method == http.MethodGet {
		plans, err := s.store.List(r.Context(), tenant, email)
		if err != nil {
			writePlanStoreError(w, err)
			return
		}
		writeJSON(w, 200, map[string]any{"ok": true, "plans": plans})
		return
	}
	if r.Method != http.MethodPost {
		writeError(w, 405, "此接口只支持读取或保存计划")
		return
	}
	var request struct {
		ID              string              `json:"id,omitempty"`
		MachineID       string              `json:"machine_id"`
		ExpectedVersion int64               `json:"expected_version"`
		Config          ExecutionPlanConfig `json:"config"`
	}
	if err := decodePlanBody(w, r, &request); err != nil {
		writeError(w, 400, "计划格式不正确或含不支持字段")
		return
	}
	if request.Config.Schedule.Timezone == "" {
		request.Config.Schedule.Timezone = "Asia/Shanghai"
	}
	if err := request.Config.Validate(); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	if !s.validatePlanPermissions(w, tenant, email, request.MachineID, request.Config) {
		return
	}
	plan, err := s.store.Save(r.Context(), ExecutionPlan{ID: request.ID, TenantID: tenant, UserEmail: email, MachineID: request.MachineID, Config: request.Config}, request.ExpectedVersion)
	if err != nil {
		writePlanStoreError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "plan": plan})
}

// validatePlanPermissions 在保存和启用时复用真实设备、岗位、平台和会员权限检查。
func (s *ExecutionPlanService) validatePlanPermissions(w http.ResponseWriter, tenant, email, machine string, config ExecutionPlanConfig) (allowed bool) {
	bound, err := s.agents.HasActiveBinding(email, machine)
	if err != nil {
		writeError(w, 503, "设备绑定暂时无法核对")
		return
	}
	if machine == "" || !bound {
		writeError(w, 403, "请选择已经绑定到当前账号的执行电脑")
		return
	}
	for _, item := range config.Items {
		position, err := s.positions.store.PositionByID(tenant, email, item.PositionID, false)
		if err != nil || position.UserEmail != email {
			writeError(w, 403, "计划含无权执行或已删除的岗位")
			return
		}
		platform := strings.ToLower(position.PlatformID)
		if platform != "boss" && platform != "hliepin" && platform != "liepin" && platform != "zhaopin" {
			writeError(w, 400, "岗位平台暂未支持计划执行")
			return
		}
		cfg, err := s.positions.systemConfigs.Get("platform." + platform)
		var capability struct {
			Open bool `json:"open"`
		}
		if err != nil || !cfg.Enabled || json.Unmarshal([]byte(cfg.ConfigValue), &capability) != nil || !capability.Open {
			writeError(w, 400, "岗位平台尚未开放，不能加入执行计划")
			return
		}
		needsMessages := false
		for _, action := range item.Actions {
			needsMessages = needsMessages || action == "auto_reply" || action == "re_greet"
		}
		if needsMessages && platform != "boss" {
			writeError(w, 400, "该平台尚未验证自动回复和复打能力")
			return
		}
		if needsMessages && !s.positions.requireAutoReplyMembership(w, email) {
			return
		}
		if positionUsesAI(position) && !s.positions.requireAIMembership(w, email) {
			return
		}
	}
	return true
}

// Item 按真实所有者读取或软删除计划，删除不抹去历史运行和报告。
func (s *ExecutionPlanService) Item(w http.ResponseWriter, r *http.Request) {
	tenant, email, ok := s.identity(w, r)
	if !ok {
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/api/execution-plans/")
	parts := strings.Split(id, "/")
	if len(parts) == 2 && parts[1] == "confirm-stop" {
		s.confirmStop(w, r, tenant, email, parts[0])
		return
	}
	if len(parts) == 2 && (parts[1] == "arm" || parts[1] == "stop") {
		s.intent(w, r, tenant, email, parts[0], parts[1])
		return
	}
	if id == "" || strings.Contains(id, "/") {
		writeError(w, 404, "计划接口不存在")
		return
	}
	switch r.Method {
	case http.MethodGet:
		plan, err := s.store.Get(r.Context(), tenant, email, id)
		if err != nil {
			writePlanStoreError(w, err)
			return
		}
		writeJSON(w, 200, map[string]any{"ok": true, "plan": plan})
	case http.MethodDelete:
		var request struct {
			ExpectedVersion int64 `json:"expected_version"`
		}
		if decodePlanBody(w, r, &request) != nil {
			writeError(w, 400, "请提供当前配置版本")
			return
		}
		if err := s.store.Delete(r.Context(), tenant, email, id, request.ExpectedVersion); err != nil {
			writePlanStoreError(w, err)
			return
		}
		writeJSON(w, 200, map[string]any{"ok": true})
	default:
		writeError(w, 405, "此接口只支持读取或删除计划")
	}
}

// confirmStop 接受指定设备的真实收尾确认，关闭窗口等待记录后才解除编辑保护。
func (s *ExecutionPlanService) confirmStop(w http.ResponseWriter, r *http.Request, tenant, email, id string) {
	if r.Method != http.MethodPost {
		writeError(w, 405, "此接口只支持 POST")
		return
	}
	var input ExecutionPlanStopConfirmation
	if err := decodePlanBody(w, r, &input); err != nil || input.validate() != nil {
		writeError(w, 400, "停止确认需包含原批次、设备、请求编号和收尾结果")
		return
	}
	plan, err := s.store.Get(r.Context(), tenant, email, id)
	if err != nil {
		writePlanStoreError(w, err)
		return
	}
	bound, err := s.agents.HasActiveBinding(email, input.MachineID)
	if err != nil {
		writeError(w, 503, "设备绑定暂时无法核对")
		return
	}
	if !bound || input.MachineID != plan.MachineID {
		writeError(w, 403, "只有指定执行电脑可以确认收尾")
		return
	}
	result, err := s.store.ConfirmStopped(r.Context(), tenant, email, id, input)
	if err != nil {
		writePlanStoreError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "plan": result})
}

// intent 登记用户启用或停止意图，启用不代表本地已取得执行权或进入工作时段。
func (s *ExecutionPlanService) intent(w http.ResponseWriter, r *http.Request, tenant, email, id, action string) {
	if r.Method != http.MethodPost {
		writeError(w, 405, "此接口只支持 POST")
		return
	}
	var input ExecutionPlanIntent
	if err := decodePlanBody(w, r, &input); err != nil {
		writeError(w, 400, "计划请求格式不正确或含不支持字段")
		return
	}
	input.Action = action
	if err := input.validate(); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	plan, err := s.store.Get(r.Context(), tenant, email, id)
	if err != nil {
		writePlanStoreError(w, err)
		return
	}
	if action == "arm" && !s.validatePlanPermissions(w, tenant, email, plan.MachineID, plan.Config) {
		return
	}
	// 使用刚核对权限的配置版本；并发编辑后的另一套岗位不能沿用本次许可。
	if plan.Version != input.ExpectedVersion {
		writePlanStoreError(w, ErrExecutionPlanVersion)
		return
	}
	result, err := s.store.Intent(r.Context(), tenant, email, id, input)
	if err != nil {
		writePlanStoreError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "plan": result})
}
