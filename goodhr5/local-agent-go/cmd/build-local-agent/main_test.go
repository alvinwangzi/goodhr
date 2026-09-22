// 本文件通过真实构建和 EXE 自检验证环境配置确实写入产物，而不是只检查脚本文本。
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"goodhr5/local-agent-go/internal/config"
)

// TestBuildRejectsInvalidSelection 验证构建前拦截缺失环境、错配环境与缺失配置。
func TestBuildRejectsInvalidSelection(t *testing.T) {
	t.Setenv("GOODHR_APP_ENV", "")
	path := filepath.Join(t.TempDir(), "dev.json")
	if err := os.WriteFile(path, []byte(`{"environment":"dev","console_url":"http://localhost:5173","cloud_api_base":"http://localhost:8084"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{},
		{"-env", "other"},
		{"-env", "prod", "-config", path},
		{"-env", "dev", "-config", path + ".missing"},
		{"-env", "dev", "-config", path, "-version", "bad version"},
	} {
		var output bytes.Buffer
		if err := runBuild(args, &output, &output); err == nil {
			t.Fatalf("错误配置应拒绝构建：%v", args)
		}
	}
}

// TestBuildEmbedsSelectedEnvironment 构建两种环境并执行自检，验证地址、版本隔离和无启动副作用。
func TestBuildEmbedsSelectedEnvironment(t *testing.T) {
	if os.Getenv("GOODHR_BUILD_INTEGRATION") != "1" {
		t.Skip("设置 GOODHR_BUILD_INTEGRATION=1 执行真实构建验收")
	}
	t.Chdir(filepath.Join("..", ".."))
	for _, test := range []struct{ environment, consoleURL, apiURL string }{
		{"dev", "http://localhost:45173/admin?from=dev#home", "http://localhost:48084"},
		{"prod", "https://console.example.com/admin", "https://api.example.com"},
	} {
		t.Run(test.environment, func(t *testing.T) {
			root := t.TempDir()
			configPath := filepath.Join(root, "input.json")
			data := fmt.Sprintf(`{"environment":%q,"console_url":%q,"cloud_api_base":%q}`, test.environment, test.consoleURL, test.apiURL)
			if err := os.WriteFile(configPath, []byte(data), 0o600); err != nil {
				t.Fatal(err)
			}
			var output bytes.Buffer
			args := []string{"-env", test.environment, "-config", configPath, "-output-dir", root, "-version", "0.1.0-test"}
			if err := runBuild(args, &output, &output); err != nil {
				t.Fatalf("真实构建失败：%v\n%s", err, output.String())
			}
			ext := ""
			if runtime.GOOS == "windows" {
				ext = ".exe"
			}
			exe := filepath.Join(root, fmt.Sprintf("hrplus-agent-%s-%s-%s%s", test.environment, runtime.GOOS, runtime.GOARCH, ext))
			dataDir := filepath.Join(root, "must-not-be-created")
			t.Setenv("GOODHR_APP_ENV", "wrong")
			t.Setenv("GOODHR_CONSOLE_URL", "https://wrong.example.com")
			t.Setenv("GOODHR_CLOUD_API_BASE", "https://wrong.example.com")
			result, err := exec.Command(exe, "--print-config", "--data-dir", dataDir, "--restart").CombinedOutput()
			if err != nil {
				t.Fatalf("产物自检失败：%v\n%s", err, result)
			}
			var got config.BuildConfig
			if err := json.Unmarshal(result, &got); err != nil {
				t.Fatalf("产物配置不是 JSON：%v\n%s", err, result)
			}
			if got.Environment != test.environment || got.ConsoleURL != test.consoleURL || got.CloudAPIBase != test.apiURL {
				t.Fatalf("产物未使用所选配置：%+v", got)
			}
			if _, err := os.Stat(dataDir); !os.IsNotExist(err) {
				t.Fatalf("自检不应创建数据目录：%v", err)
			}
		})
	}
}
