// 本文件验证本地程序打开云端控制台时携带实际监听端口的 URL 处理规则。
package app

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"

	"goodhr5/local-agent-go/internal/config"
)

// TestResolveConsoleURLUsesEnvironmentConfig 验证启动地址来自所选环境，远程返回值不能覆盖它。
func TestResolveConsoleURLUsesEnvironmentConfig(t *testing.T) {
	for _, test := range []struct {
		environment string
		consoleURL  string
	}{
		{environment: "dev", consoleURL: "http://localhost:5173/admin"},
		{environment: "prod", consoleURL: "https://console.example.com/admin?from=agent#home"},
	} {
		t.Run(test.environment, func(t *testing.T) {
			var requests atomic.Int32
			remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				_, _ = fmt.Fprint(w, `{"url":"https://wrong.example.com/admin"}`)
			}))
			defer remote.Close()
			t.Setenv("GOODHR_APP_ENV", test.environment)
			t.Setenv("GOODHR_CLOUD_API_BASE", remote.URL)
			t.Setenv("GOODHR_CONSOLE_URL", test.consoleURL)
			cfg, err := config.NewWithDataDir("127.0.0.1", 55271, t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			s := &Server{cfg: cfg}
			got := s.resolveConsoleURL("http://127.0.0.1:55271/admin/")
			if got != test.consoleURL {
				t.Fatalf("打开地址 = %q，期望环境配置地址 %q", got, test.consoleURL)
			}
			if requests.Load() != 0 {
				t.Fatal("解析控制台地址不应再请求远程跳转配置")
			}
		})
	}
}

// TestConsoleDevURLUsesSelectedConfig 验证本地代理也使用配置地址，生产环境不探测开发服务。
func TestConsoleDevURLUsesSelectedConfig(t *testing.T) {
	t.Setenv("GOODHR_CONSOLE_DEV_URL", "http://wrong.example.com")
	for _, test := range []struct{ environment, consoleURL, want string }{
		{"dev", "http://127.0.0.1:46123/admin?from=agent#home", "http://127.0.0.1:46123"},
		{"prod", "https://console.example.com/admin", ""},
	} {
		s := &Server{cfg: &config.Config{Environment: test.environment, ConsoleURL: test.consoleURL}}
		if got := s.consoleDevURL(); got != test.want {
			t.Fatalf("%s 代理地址 = %q，期望 %q", test.environment, got, test.want)
		}
	}
}

// TestWithLocalAgentPort 验证新增端口参数时会保留原有查询参数和锚点。
func TestWithLocalAgentPort(t *testing.T) {
	target := withLocalAgentPort("https://goodhr5.58it.cn/admin?next=%2Fadmin%2Fpositions#section", 55279)
	parsed, err := url.Parse(target)
	if err != nil {
		t.Fatalf("解析控制台地址失败: %v", err)
	}
	if got := parsed.Query().Get("local_port"); got != "55279" {
		t.Fatalf("local_port = %q, want 55279", got)
	}
	if got := parsed.Query().Get("next"); got != "/admin/positions" {
		t.Fatalf("next = %q, want /admin/positions", got)
	}
	if parsed.Fragment != "section" {
		t.Fatalf("fragment = %q, want section", parsed.Fragment)
	}
}

// TestWithLocalAgentPortReplacesExistingValue 验证当前实际端口会覆盖地址中的旧端口。
func TestWithLocalAgentPortReplacesExistingValue(t *testing.T) {
	target := withLocalAgentPort("https://goodhr5.58it.cn/admin?local_port=55271", 55273)
	parsed, err := url.Parse(target)
	if err != nil {
		t.Fatalf("解析控制台地址失败: %v", err)
	}
	if got := parsed.Query().Get("local_port"); got != "55273" {
		t.Fatalf("local_port = %q, want 55273", got)
	}
}

// TestWithLocalAgentPortIgnoresInvalidInput 验证非法端口或非法地址不会破坏原始地址。
func TestWithLocalAgentPortIgnoresInvalidInput(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		port int
	}{
		{name: "zero port", raw: "https://goodhr5.58it.cn/admin", port: 0},
		{name: "too large port", raw: "https://goodhr5.58it.cn/admin", port: 65536},
		{name: "relative url", raw: "/admin", port: 55271},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := withLocalAgentPort(test.raw, test.port); got != test.raw {
				t.Fatalf("withLocalAgentPort() = %q, want %q", got, test.raw)
			}
		})
	}
}
