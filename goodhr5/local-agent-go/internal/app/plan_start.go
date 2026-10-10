// 本文件提供 HRPlus 计划立即开始入口，复用后台启动和持久队列，网页连接断开不取消已领取任务。
package app

import (
	"database/sql"
	"encoding/json"
	"errors"
	"goodhr5/local-agent-go/internal/cloudapi"
	"goodhr5/local-agent-go/internal/planmodel"
	"goodhr5/local-agent-go/internal/planrunner"
	"goodhr5/local-agent-go/internal/positionrunner"
	"goodhr5/local-agent-go/internal/response"
	"io"
	"net/http"
	"strings"
	"time"
)

// planStartRequest 绑定网页看到的原版本及启用批次，不允许通过请求更改时间或岗位配置。
type planStartRequest struct {
	ExpectedVersion int64  `json:"expected_version"`
	ActivationID    string `json:"activation_id"`
	Token           string `json:"token,omitempty"`
}

// handleLocalPlanStart 只允许当前已核对账号请求指定计划；时间段外等待定时，资源冲突保持原排队顺序。
func (s *Server) handleLocalPlanStart(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/v1/local/execution-plans/"), "/")
	if len(parts) != 2 || !planmodel.ValidID(parts[0]) || parts[1] != "start" {
		response.Error(w, 404, "没有找到这个计划操作")
		return
	}
	if r.Method != http.MethodPost {
		response.Error(w, 405, "此接口只支持开始计划")
		return
	}
	var input planStartRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		response.Error(w, 400, "请提供当前计划版本和启用批次")
		return
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF || input.ExpectedVersion < 1 || !planmodel.ValidID(input.ActivationID) {
		response.Error(w, 400, "计划请求内容不完整，请刷新后重试")
		return
	}
	identity, version := s.currentPlanSession()
	token := bearerToken(r)
	if token == "" {
		token = input.Token
	} else if input.Token != "" && input.Token != token {
		response.Error(w, 403, "网页登录与本地登录不一致，请重新连接")
		return
	}
	if identity == nil || token == "" || token != identity.Token {
		response.Error(w, 403, "网页登录与本地登录不一致，请重新连接")
		return
	}
	a := s.planAuthority(identity, version)
	client := cloudapi.New(identity.CloudBase)
	plans, err := client.ListExecutionPlans(r.Context(), token)
	if err != nil {
		var expired cloudapi.AuthExpiredError
		if errors.As(err, &expired) {
			s.invalidateExpiredPlanSession(version, identity)
		}
		response.Error(w, 502, "计划暂时无法核对，请重试")
		return
	}
	if !a.StillCurrent() {
		response.Error(w, 409, "登录连接已变化，请重试")
		return
	}
	var plan *planmodel.Plan
	for index := range plans {
		if plans[index].ID == parts[0] {
			plan = &plans[index]
			break
		}
	}
	if plan == nil {
		response.Error(w, 404, "没有找到这个计划")
		return
	}
	if plan.Version != input.ExpectedVersion || plan.ActivationID != input.ActivationID {
		response.Error(w, 409, "计划已变化，请刷新后重试")
		return
	}
	if plan.UserEmail != identity.UserEmail || (plan.TenantID != "" && plan.TenantID != identity.TenantID) {
		response.Error(w, 403, "这个计划不属于当前登录账号")
		return
	}
	if plan.MachineID != identity.MachineID {
		response.Success(w, map[string]any{"status": "waiting_device", "message": "计划由另一台电脑执行"})
		return
	}
	if plan.State != "enabled" || plan.StopRequested {
		response.Error(w, 409, "请先启用计划，再开始执行")
		return
	}
	now := time.Now()
	if s.planNow != nil {
		now = s.planNow()
	}
	window, err := plan.Config.Schedule.Current(now)
	if err != nil {
		response.Error(w, 400, err.Error())
		return
	}
	if window == nil {
		response.Success(w, map[string]any{"status": "waiting_time", "message": "等待下个执行时间"})
		return
	}
	s.planExecutionMu.Lock()
	background := s.planExecutionContext
	s.planExecutionMu.Unlock()
	if background == nil || background.Err() != nil {
		response.Error(w, 503, "本地后台尚未启动，请重启本地程序")
		return
	}
	scheduler := planrunner.NewScheduler(planrunner.New(s.db, s.runner, client, s.planNow))
	if err := scheduler.EnqueueDue(r.Context(), []planmodel.Plan{*plan}, identity.MachineID, a); err != nil {
		response.Error(w, 409, err.Error())
		return
	}
	s.signalPlanExecutions()
	err = s.processPlanExecutionsRequest(background, plan)
	var busy cloudapi.PlanRequestError
	if err != nil && !errors.Is(err, planrunner.ErrOutsidePlanWindow) && !errors.Is(err, planrunner.ErrPlanNeedsSettlement) && !errors.Is(err, positionrunner.ErrPlanBrowserBusy) && !(errors.As(err, &busy) && busy.Code == "EXECUTION_BUSY") {
		response.Error(w, 409, err.Error())
		return
	}
	if !a.StillCurrent() {
		response.Error(w, 409, "登录连接已变化，请重试")
		return
	}
	run, readErr := s.db.PlanRunForDate(r.Context(), a.OwnerScope, plan.ID, plan.ActivationID, window.Date)
	status, message := "waiting_device", "已排队等待执行"
	if readErr == nil {
		switch run.State {
		case "running":
			status, message = "started", "任务已开始"
		case "starting":
			status, message = "starting", "正在准备执行"
		case "completed":
			status, message = "completed", "本次计划已经完成"
		case "blocked", "incomplete", "stopped":
			status, message = run.State, "本次任务已结束，请停止后重新开始"
		case "draining":
			status, message = "settling", "原任务正在收尾"
		}
	} else if !errors.Is(readErr, sql.ErrNoRows) {
		response.Error(w, 503, "原运行状态暂时无法核对，请重试")
		return
	}
	s.planExecutionMu.Lock()
	active := s.activePlanExecution
	if active != nil && active.execution.Plan.ID == plan.ID && active.execution.Plan.ActivationID == plan.ActivationID {
		select {
		case <-active.done:
			status, message = "settling", "原任务正在收尾"
		default:
		}
	}
	s.planExecutionMu.Unlock()
	if status == "waiting_device" {
		latest := time.Now()
		if s.planNow != nil {
			latest = s.planNow()
		}
		if current, err := plan.Config.Schedule.Current(latest); err == nil && current == nil {
			status, message = "waiting_time", "等待下个执行时间"
		}
	}
	response.Success(w, map[string]any{"status": status, "message": message, "run_id": run.ID})
}
