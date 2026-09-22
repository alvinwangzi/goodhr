// 本文件负责执行任务的后台查询接口：任务列表、任务详情和本次运行的候选人名单。
package httpapi

import (
	"net/http"
	"strings"
)

// TaskRunService 处理执行任务的后台查询请求。
type TaskRunService struct {
	auth    *AuthService
	runs    TaskRunStore
	tenants TenantStore
}

// NewTaskRunService 创建执行任务查询服务。
func NewTaskRunService(auth *AuthService, runs TaskRunStore, tenants TenantStore) *TaskRunService {
	return &TaskRunService{auth: auth, runs: runs, tenants: tenants}
}

// Collection 处理 /api/task-runs 和 /api/task-runs/{id}/candidates 的查询请求。
// w 为响应对象，r 为请求对象。
func (s *TaskRunService) Collection(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	path := strings.Trim(r.URL.Path, "/")
	if path == "api/task-runs" {
		s.list(w, r)
		return
	}
	if strings.HasSuffix(r.URL.Path, "/candidates") {
		s.candidates(w, r)
		return
	}
	s.detail(w, r)
}

// list 返回执行任务分页列表。
// w 为响应对象，r 为携带分页参数的请求。
func (s *TaskRunService) list(w http.ResponseWriter, r *http.Request) {
	session, ok := s.currentSession(w, r)
	if !ok {
		return
	}
	tenantID, isAdmin := s.tenantInfo(session.Email)
	if tenantID == "" {
		writeError(w, http.StatusInternalServerError, "failed to load tenant info")
		return
	}
	result, err := s.runs.ListTaskRuns(tenantID, session.Email, isAdmin, parsePositiveInt(r.URL.Query().Get("page")), parsePositiveInt(r.URL.Query().Get("page_size")))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list task runs")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "runs": result.Items, "total": result.Total, "page": result.Page, "page_size": result.PageSize})
}

// detail 返回一条执行任务详情。
// w 为响应对象，r 为请求对象；路径格式为 /api/task-runs/{runID}。
func (s *TaskRunService) detail(w http.ResponseWriter, r *http.Request) {
	session, ok := s.currentSession(w, r)
	if !ok {
		return
	}
	tenantID, _ := s.tenantInfo(session.Email)
	runID := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/task-runs/"), "/")
	if strings.Contains(runID, "/") || strings.TrimSpace(runID) == "" {
		writeError(w, http.StatusNotFound, "task run not found")
		return
	}
	run, err := s.runs.TaskRunByID(tenantID, runID)
	if err == ErrNotFound {
		writeError(w, http.StatusNotFound, "task run not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load task run")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "run": run})
}

// candidates 返回执行任务的候选人名单，filter 支持 greeted（打过招呼）和 resume（要过简历）。
// w 为响应对象，r 为请求对象；路径格式为 /api/task-runs/{runID}/candidates。
func (s *TaskRunService) candidates(w http.ResponseWriter, r *http.Request) {
	session, ok := s.currentSession(w, r)
	if !ok {
		return
	}
	tenantID, _ := s.tenantInfo(session.Email)
	runID := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/task-runs/"), "/candidates")
	filter := firstNonEmpty(strings.TrimSpace(r.URL.Query().Get("filter")), "greeted")
	if filter != "greeted" && filter != "resume" {
		writeError(w, http.StatusBadRequest, "filter must be greeted or resume")
		return
	}
	items, err := s.runs.TaskRunCandidates(tenantID, strings.TrimSpace(runID), filter)
	if err == ErrNotFound {
		writeError(w, http.StatusNotFound, "task run not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load task run candidates")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "candidates": items})
}

// currentSession 从请求中读取当前登录会话。
func (s *TaskRunService) currentSession(w http.ResponseWriter, r *http.Request) (Session, bool) {
	session, err := s.auth.SessionFromRequest(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "session is invalid or expired")
		return Session{}, false
	}
	return session, true
}

// tenantInfo 返回用户团队标识和管理员身份。
func (s *TaskRunService) tenantInfo(email string) (string, bool) {
	tenant, err := s.tenants.GetOrCreateTenant(email)
	if err != nil {
		return "", false
	}
	isAdmin, _ := s.tenants.IsTenantAdmin(tenant.ID, email)
	return tenant.ID, isAdmin
}
