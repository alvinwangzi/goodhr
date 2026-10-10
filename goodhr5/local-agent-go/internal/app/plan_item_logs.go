// 本文件只读查询 HRPlus 原执行项本地进度日志，先核对当前真实账号和云端原运行，不从岗位猜测归属。
package app

import (
	"errors"
	"goodhr5/local-agent-go/internal/cloudapi"
	"goodhr5/local-agent-go/internal/planmodel"
	"goodhr5/local-agent-go/internal/response"
	"net/http"
	"strconv"
	"strings"
)

// handlePlanItemLogs 只支持请求头认证的原执行项读取；没有本地记录明确指出执行电脑位置。
func (s *Server) handlePlanItemLogs(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/v1/local/execution-plan-runs/"), "/")
	if len(parts) != 4 || !planmodel.ValidID(parts[0]) || parts[1] != "items" || !planmodel.ValidID(parts[2]) || parts[3] != "logs" {
		response.Error(w, 404, "没有找到执行项日志")
		return
	}
	if r.Method != http.MethodGet {
		response.Error(w, 405, "执行项日志只支持读取")
		return
	}
	identity, version := s.currentPlanSession()
	token := bearerToken(r)
	if identity == nil || token == "" || token != identity.Token {
		response.Error(w, 403, "网页登录与本地登录不一致，请重新连接")
		return
	}
	before := int64(0)
	if value := r.URL.Query().Get("before"); value != "" {
		parsed, err := strconv.ParseInt(value, 10, 64)
		if err != nil || parsed < 0 {
			response.Error(w, 400, "日志分页编号不正确")
			return
		}
		before = parsed
	}
	limit := 100
	if value := r.URL.Query().Get("limit"); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed < 1 || parsed > 200 {
			response.Error(w, 400, "每次读取一至两百条日志")
			return
		}
		limit = parsed
	}
	a := s.planAuthority(identity, version)
	client := cloudapi.New(identity.CloudBase)
	current, err := client.SessionIdentity(r.Context(), token)
	if err != nil {
		var expired cloudapi.AuthExpiredError
		if errors.As(err, &expired) {
			s.invalidateExpiredPlanSession(version, identity)
		}
		response.Error(w, 403, "登录状态暂时无法核对，请重新连接")
		return
	}
	if current.UserEmail != identity.UserEmail || current.TenantID != identity.TenantID || !a.StillCurrent() {
		response.Error(w, 403, "登录账号或团队已变化，请重新连接")
		return
	}
	run, err := client.ExecutionPlanRun(r.Context(), token, parts[0])
	if err != nil {
		var expired cloudapi.AuthExpiredError
		if errors.As(err, &expired) {
			s.invalidateExpiredPlanSession(version, identity)
			response.Error(w, 403, "登录已失效，请重新连接")
			return
		}
		var rejected cloudapi.PlanRequestError
		if errors.As(err, &rejected) && (rejected.Status == 403 || rejected.Status == 404) {
			response.Error(w, rejected.Status, "原运行不存在或没有读取权限")
			return
		}
		response.Error(w, 502, "原运行暂时无法核对，请稍后刷新")
		return
	}
	var taskID string
	for _, item := range run.Items {
		if item.ID == parts[2] {
			taskID = item.TaskRunID
			break
		}
	}
	if taskID == "" {
		response.Error(w, 404, "本项没有原任务记录")
		return
	}
	logs, err := s.db.ListPlanItemLogs(r.Context(), a.OwnerScope, run.ID, parts[2], before, limit)
	if err != nil {
		response.Error(w, 500, "执行项日志暂时无法读取")
		return
	}
	for _, item := range logs {
		if item.TaskRunID != taskID || item.PlanRunID != run.ID || item.ItemRunID != parts[2] {
			response.Error(w, 409, "日志原归属需要核对")
			return
		}
	}
	if !a.StillCurrent() {
		response.Error(w, 409, "登录连接已变化，请重试")
		return
	}
	message := "原执行项进度日志"
	if len(logs) == 0 {
		message = "当前电脑没有这段进度日志，请在实际执行电脑查看。日志尚未上传云端。"
	}
	next := int64(0)
	if len(logs) == limit {
		next = logs[len(logs)-1].ID
	}
	response.Success(w, map[string]any{"run_id": run.ID, "item_run_id": parts[2], "task_run_id": taskID, "logs": logs, "next_before": next, "message": message, "local_only": true})
}
