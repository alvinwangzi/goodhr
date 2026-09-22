// 本文件验证构建配置校验及包内配置优先级，防止开发包与生产包混用地址。
package config

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"
)

// TestLoadBuildConfig 验证构建时读取所选环境并拒绝缺失、拼错或无效的地址。
func TestLoadBuildConfig(t *testing.T) {
	for _, test := range []struct {
		name, environment, input string
		wantError                bool
	}{
		{"开发配置", "dev", `{"environment":"dev","console_url":"http://localhost:5173/admin","cloud_api_base":"http://localhost:8084"}`, false},
		{"生产配置", "prod", `{"environment":"prod","console_url":"https://console.example.com/admin","cloud_api_base":"https://api.example.com"}`, false},
		{"环境不匹配", "prod", `{"environment":"dev","console_url":"http://localhost:5173","cloud_api_base":"http://localhost:8084"}`, true},
		{"缺少前端", "dev", `{"environment":"dev","cloud_api_base":"http://localhost:8084"}`, true},
		{"缺少后端", "dev", `{"environment":"dev","console_url":"http://localhost:5173"}`, true},
		{"缺少环境", "", `{"console_url":"http://localhost:5173","cloud_api_base":"http://localhost:8084"}`, true},
		{"不支持的环境", "test", `{"environment":"test","console_url":"http://localhost:5173","cloud_api_base":"http://localhost:8084"}`, true},
		{"错误协议", "dev", `{"environment":"dev","console_url":"file:///test","cloud_api_base":"http://localhost:8084"}`, true},
		{"禁止内嵌凭据", "dev", `{"environment":"dev","console_url":"https://user:pass@example.com","cloud_api_base":"http://localhost:8084"}`, true},
		{"字段拼错", "dev", `{"environment":"dev","console_url":"http://localhost:5173","cloud_api_base":"http://localhost:8084","console_urll":"x"}`, true},
		{"错误清单地址", "dev", `{"environment":"dev","console_url":"http://localhost:5173","cloud_api_base":"http://localhost:8084","console_manifest_url":"relative.json"}`, true},
		{"后端不支持查询参数", "dev", `{"environment":"dev","console_url":"http://localhost:5173","cloud_api_base":"http://localhost:8084?x=1"}`, true},
		{"多个配置对象", "dev", `{"environment":"dev","console_url":"http://localhost:5173","cloud_api_base":"http://localhost:8084"}{}`, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.json")
			if err := os.WriteFile(path, []byte(test.input), 0o600); err != nil {
				t.Fatal(err)
			}
			cfg, err := LoadBuildConfig(path, test.environment)
			if (err != nil) != test.wantError {
				t.Fatalf("LoadBuildConfig() error = %v，期望失败 = %v", err, test.wantError)
			}
			if err == nil && cfg.Environment != test.environment {
				t.Fatalf("读取了错误环境：%s", cfg.Environment)
			}
		})
	}
	if _, err := LoadBuildConfig(filepath.Join(t.TempDir(), "missing.json"), "dev"); err == nil {
		t.Fatal("配置文件缺失时应拒绝构建")
	}
}

// TestBuildConfigNormalizesAddresses 验证配置去除首尾空格与后端尾斜线，避免请求拼接错误。
func TestBuildConfigNormalizesAddresses(t *testing.T) {
	cfg, err := parseBuildConfig([]byte(`{"environment":" dev ","console_url":" http://localhost:5173/admin ","cloud_api_base":" http://localhost:8084/ "}`))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Environment != "dev" || cfg.ConsoleURL != "http://localhost:5173/admin" || cfg.CloudAPIBase != "http://localhost:8084" {
		t.Fatalf("配置未完成规范化：%+v", cfg)
	}
}

// TestPackagedConfigCannotBeOverridden 验证已打包的地址不会被用户电脑中旧环境变量覆盖。
func TestPackagedConfigCannotBeOverridden(t *testing.T) {
	original := EmbeddedBuildConfig
	t.Cleanup(func() { EmbeddedBuildConfig = original })
	EmbeddedBuildConfig = base64.StdEncoding.EncodeToString([]byte(`{"environment":"dev","console_url":"http://localhost:5173/admin","cloud_api_base":"http://localhost:8084"}`))
	t.Setenv("GOODHR_APP_ENV", "prod")
	t.Setenv("GOODHR_CONSOLE_URL", "https://wrong.example.com")
	t.Setenv("GOODHR_CLOUD_API_BASE", "https://wrong.example.com")
	cfg, err := NewWithDataDir("127.0.0.1", 55271, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Environment != "dev" || cfg.ConsoleURL != "http://localhost:5173/admin" || cfg.CloudAPIBase != "http://localhost:8084" {
		t.Fatalf("包内配置被覆盖：%+v", cfg)
	}
}

// TestInvalidEmbeddedBuildConfigDoesNotFallback 验证包内配置损坏时不能悄悄改用其他环境。
func TestInvalidEmbeddedBuildConfigDoesNotFallback(t *testing.T) {
	original := EmbeddedBuildConfig
	t.Cleanup(func() { EmbeddedBuildConfig = original })
	t.Setenv("GOODHR_APP_ENV", "dev")
	t.Setenv("GOODHR_CONSOLE_URL", "http://localhost:5173")
	t.Setenv("GOODHR_CLOUD_API_BASE", "http://localhost:8084")
	for _, invalid := range []string{"not-base64", base64.StdEncoding.EncodeToString([]byte(`{}`))} {
		EmbeddedBuildConfig = invalid
		if _, err := RuntimeBuildConfig(); err == nil {
			t.Fatal("包内配置损坏时应拒绝启动")
		}
	}
}
