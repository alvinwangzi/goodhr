// 本文件验证 HRPlus 网页明确退出后旧登录无法恢复后台授权，不使用真实邮件或本地凭证。
package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestLogoutRevokesSession 验证旧令牌失效、重复退出幂等以及其他账号保持有效。
func TestLogoutRevokesSession(t *testing.T) {
	s := mustNewServer(t)
	routes := s.Routes()
	token := loginForTest(t, routes, "logout@example.com")
	other := loginForTest(t, routes, "other@example.com")
	for i := 0; i < 2; i++ {
		req := httptest.NewRequest(http.MethodPost, "/api/auth/logout", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		response := httptest.NewRecorder()
		routes.ServeHTTP(response, req)
		if response.Code != 200 {
			t.Fatal("重复退出失败")
		}
	}
	if _, err := s.auth.SessionFromToken(token); err == nil {
		t.Fatal("退出令牌仍然有效")
	}
	if _, err := s.auth.SessionFromToken(other); err != nil {
		t.Fatal("退出影响其他登录", err)
	}
}
