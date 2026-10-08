// 本文件验证 HRPlus 云端所有者只来自已认证的既有登录接口，错误或缺失证明时不恢复补传。
package cloudapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestSessionOwner 验证现有两种响应封装、认证失败和缺失所有者。
func TestSessionOwner(t *testing.T) {
	for _, tc := range []struct {
		name, body, wanted string
		status             int
		failed             bool
	}{
		{"flat", `{"ok":true,"user":{"email":" OWNER@example.com "}}`, "owner@example.com", 200, false},
		{"wrapped", `{"ok":true,"data":{"user":{"email":"owner@example.com"}}}`, "owner@example.com", 200, false},
		{"missing", `{"ok":true}`, "", 200, true},
		{"expired", `{"msg":"请重新登录"}`, "", 401, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api/auth/me" || r.Header.Get("Authorization") != "Bearer fixture-token" {
					t.Errorf("未复用已认证登录接口")
				}
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()
			owner, err := New(server.URL).SessionOwner(context.Background(), "fixture-token")
			if (err != nil) != tc.failed || owner != tc.wanted {
				t.Fatalf("owner=%q err=%v", owner, err)
			}
		})
	}
}
