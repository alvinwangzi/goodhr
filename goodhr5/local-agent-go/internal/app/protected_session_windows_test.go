// 本文件使用真实 Windows DPAPI 与隔离 HTTP 云端验证 HRPlus 会话恢复和退出，不操作招聘页面。
package app

import (
	"context"
	"encoding/json"
	"goodhr5/local-agent-go/internal/protectedsession"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// protectedSessionFixture 为当前测试目录绑定一个真实加密文件与明确的云端身份。
func protectedSessionFixture(t *testing.T) (*Server, *httptest.Server, protectedsession.Session, *bool) {
	t.Helper()
	expired := false
	cloud := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if expired {
			w.WriteHeader(401)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": "expired"})
			return
		}
		if r.Header.Get("Authorization") != "Bearer fixture-auth-secret" {
			t.Error("恢复没有使用原凭证")
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "user": map[string]any{"email": "owner@fixture.test", "tenant_id": "fixture-team"}})
	}))
	t.Cleanup(cloud.Close)
	s := newCapabilityTestServer(t)
	s.cfg.CloudAPIBase = cloud.URL
	machine, err := s.ensureMachineID()
	if err != nil {
		t.Fatal(err)
	}
	value := protectedsession.Session{CloudBase: cloud.URL, UserEmail: "owner@fixture.test", TenantID: "fixture-team", MachineID: machine, Token: "fixture-auth-secret"}
	if err = protectedsession.New(s.cfg.DataDir).Save(value); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.runner.ClearReGreetUploadSession)
	return s, cloud, value, &expired
}

// TestRepeatedSessionBindPreservesPlan 验证网页以同一已核对令牌重新绑定，不取消原计划或改写登录代次。
func TestRepeatedSessionBindPreservesPlan(t *testing.T) {
	s, _, value, _ := protectedSessionFixture(t)
	s.restoreProtectedSession(t.Context())
	identity, version := s.currentPlanSession()
	a := s.planAuthority(identity, version)
	parent, err := s.runner.ReservePlanBrowser(t.Context(), "20000000-0000-0000-0000-000000000099")
	if err != nil {
		t.Fatal(err)
	}
	defer parent.Release(true)
	body, _ := json.Marshal(map[string]any{"token": value.Token})
	recorder := httptest.NewRecorder()
	s.handleSessionBind(recorder, httptest.NewRequest("POST", "/api/v1/session/bind", strings.NewReader(string(body))))
	if recorder.Code != http.StatusOK || !a.StillCurrent() || !parent.Valid() {
		t.Fatal("同一登录重新绑定停止了原计划", recorder.Code)
	}
	current, currentVersion := s.currentPlanSession()
	if current != identity || currentVersion != version {
		t.Fatal("重复绑定改变了原授权代次")
	}
}

// TestCancelledPlanIsNotReleased 验证取消上下文不能作为执行权实际释放的证明。
func TestCancelledPlanIsNotReleased(t *testing.T) {
	s, _, _, _ := protectedSessionFixture(t)
	parent, err := s.runner.ReservePlanBrowser(t.Context(), "20000000-0000-0000-0000-000000000099")
	if err != nil {
		t.Fatal(err)
	}
	s.runner.StopAll("测试取消原计划")
	if parent.Valid() || parent.CleanupReleased() {
		t.Fatal("取消被误认为实际释放")
	}
	if err := parent.Release(true); err != nil {
		t.Fatal(err)
	}
	if !parent.CleanupReleased() {
		t.Fatal("实际释放未保存证明")
	}
}

// TestProtectedSessionRestore 验证恢复必须通过云端核对，过期凭证和错误云端地址不能变成已登录。
func TestProtectedSessionRestore(t *testing.T) {
	s, _, value, expired := protectedSessionFixture(t)
	s.restoreProtectedSession(t.Context())
	if s.sessionCurrent == nil || s.sessionCurrent.UserEmail != value.UserEmail {
		t.Fatal("有效会话没有恢复")
	}
	recorder := httptest.NewRecorder()
	s.handleProtectedSessionStatus(recorder, httptest.NewRequest("GET", "/api/v1/session/status", nil))
	if strings.Contains(recorder.Body.String(), value.Token) {
		t.Fatal("状态接口输出凭证")
	}
	s.sessionCurrent = nil
	*expired = true
	s.restoreProtectedSession(t.Context())
	if s.sessionCurrent != nil {
		t.Fatal("过期会话仍被采用")
	}
	*expired = false
	s.cfg.CloudAPIBase = "http://127.0.0.1:1"
	s.restoreProtectedSession(t.Context())
	if s.sessionCurrent != nil {
		t.Fatal("令牌被用于另一个云端地址")
	}
}

// TestLogoutRejectsLateSession 验证退出使旧绑定序号失效，迟到请求不能恢复凭证或上传授权。
func TestLogoutRejectsLateSession(t *testing.T) {
	s, _, value, _ := protectedSessionFixture(t)
	sequence := s.beginProtectedSessionChange()
	recorder := httptest.NewRecorder()
	s.handleSessionUnbind(recorder, httptest.NewRequest("POST", "/api/v1/session/unbind", nil))
	if recorder.Code != 200 || !s.sessionBlocked {
		t.Fatal("退出没有阻止新执行")
	}
	if err := s.commitProtectedSession(context.Background(), sequence, value); err == nil {
		t.Fatal("旧登录请求覆盖退出")
	}
	if _, err := protectedsession.New(s.cfg.DataDir).Load(); err == nil {
		t.Fatal("退出后仍恢复凭证")
	}
	next := s.beginProtectedSessionChange()
	if err := s.commitProtectedSession(t.Context(), next, value); err != nil || s.sessionBlocked {
		t.Fatal("新登录不能重新启用", err)
	}
}

// TestRestoreYieldsToExplicitLogin 验证启动恢复不能覆盖已开始核对的网页新登录。
func TestRestoreYieldsToExplicitLogin(t *testing.T) {
	s, _, _, _ := protectedSessionFixture(t)
	s.beginProtectedSessionChange()
	s.restoreProtectedSession(t.Context())
	if s.sessionCurrent != nil {
		t.Fatal("后台旧文件覆盖显式新登录")
	}
}
