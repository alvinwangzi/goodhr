// 本文件用隔离 HTTP、临时 SQLite 和真实后台运行器验证 HRPlus 立即开始入口，不访问真实招聘页面。
package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const fixturePlanStartPath = "/api/v1/local/execution-plans/10000000-0000-0000-0000-000000000001/start"

// planStartCall 通过实际路由调用立即开始，不输出或记录请求令牌。
func planStartCall(t *testing.T, s *Server, ctx context.Context, token, body string) *httptest.ResponseRecorder {
	t.Helper()
	mux := http.NewServeMux()
	s.registerRoutes(mux)
	r := httptest.NewRequest(http.MethodPost, fixturePlanStartPath, strings.NewReader(body)).WithContext(ctx)
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	return w
}

// startResponseStatus 解析真实响应数据用于核对按钮反馈，不用 HTTP 成功替代实际开始。
func startResponseStatus(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	var payload struct {
		Data struct {
			Status string `json:"status"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	return payload.Data.Status
}

// TestImmediatePlanWindowAndIdentity 验证窗口外不领取，其他账号和旧批次不能启动当前计划。
func TestImmediatePlanWindowAndIdentity(t *testing.T) {
	s, identity, claims, _, _, _ := backgroundPlanFixture(t, false)
	if err := s.commitProtectedSession(t.Context(), 0, identity); err != nil {
		t.Fatal(err)
	}
	body := `{"expected_version":1,"activation_id":"30000000-0000-0000-0000-000000000001"}`
	if w := planStartCall(t, s, t.Context(), "other-token", body); w.Code != 403 || claims.Load() != 0 {
		t.Fatal("其他登录启动计划", w.Code)
	}
	s.planNow = func() time.Time { return time.Date(2026, 10, 10, 4, 0, 0, 0, time.UTC) }
	w := planStartCall(t, s, t.Context(), identity.Token, body)
	if w.Code != 200 || startResponseStatus(t, w) != "waiting_time" || claims.Load() != 0 {
		t.Fatal("名义结束后领取任务", w.Code)
	}
	stale := `{"expected_version":2,"activation_id":"30000000-0000-0000-0000-000000000001"}`
	if w := planStartCall(t, s, t.Context(), identity.Token, stale); w.Code != 409 || claims.Load() != 0 {
		t.Fatal("旧版本触发计划", w.Code)
	}
}

// TestImmediatePlanUsesBackgroundContext 验证立即开始复用同一持久触发，网页请求关闭不取消本地长任务。
func TestImmediatePlanUsesBackgroundContext(t *testing.T) {
	s, identity, claims, _, _, preparing := backgroundPlanFixture(t, true)
	if err := s.commitProtectedSession(t.Context(), 0, identity); err != nil {
		t.Fatal(err)
	}
	background, stop := context.WithCancel(t.Context())
	s.planExecutionMu.Lock()
	s.planExecutionContext = background
	s.planExecutionMu.Unlock()
	defer func() { stop(); s.shutdownPlanExecution(background) }()
	web, cancelWeb := context.WithCancel(t.Context())
	body := `{"expected_version":1,"activation_id":"30000000-0000-0000-0000-000000000001"}`
	w := planStartCall(t, s, web, identity.Token, body)
	if w.Code != 200 || startResponseStatus(t, w) != "starting" {
		t.Fatal("准备期间错误显示已经开始", w.Code)
	}
	select {
	case <-preparing:
	case <-time.After(5 * time.Second):
		t.Fatal("没有进入实际准备")
	}
	cancelWeb()
	s.planExecutionMu.Lock()
	active := s.activePlanExecution
	s.planExecutionMu.Unlock()
	if active == nil || active.execution.Held.Reservation.Context().Err() != nil {
		t.Fatal("网页断开取消了本地执行")
	}
	w = planStartCall(t, s, t.Context(), identity.Token, body)
	if w.Code != 200 || claims.Load() != 1 {
		t.Fatal("重复点击再次领取", w.Code, claims.Load())
	}
}

// TestImmediatePlanWaitsForResource 验证当前本地执行权被占用时只排队，不访问云端领取。
func TestImmediatePlanWaitsForResource(t *testing.T) {
	s, identity, claims, _, _, _ := backgroundPlanFixture(t, false)
	if err := s.commitProtectedSession(t.Context(), 0, identity); err != nil {
		t.Fatal(err)
	}
	background, stop := context.WithCancel(t.Context())
	defer stop()
	s.planExecutionMu.Lock()
	s.planExecutionContext = background
	s.planExecutionMu.Unlock()
	other, err := s.runner.ReservePlanBrowser(t.Context(), "20000000-0000-0000-0000-000000000099")
	if err != nil {
		t.Fatal(err)
	}
	defer other.Release(true)
	body := `{"expected_version":1,"activation_id":"30000000-0000-0000-0000-000000000001"}`
	w := planStartCall(t, s, t.Context(), identity.Token, body)
	if w.Code != 200 || startResponseStatus(t, w) != "waiting_device" || claims.Load() != 0 {
		t.Fatal("冲突仍领取或错误反馈", w.Code)
	}
}
