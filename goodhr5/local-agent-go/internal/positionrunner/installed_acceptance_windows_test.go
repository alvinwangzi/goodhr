// 本文件为 HRPlus 程序验收选择已安装 EXE 和固定隔离服务地址，禁止以源码构建冒充实装证据。
package positionrunner

import (
	"crypto/sha256"
	"encoding/hex"
	"net"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// startNativeAcceptanceCloud 在实装模式严格绑定包内地址，端口占用则失败，不回退其他环境。
func startNativeAcceptanceCloud(t *testing.T, cloud *httptest.Server) {
	t.Helper()
	if os.Getenv("HRPLUS_M2_INSTALLED_AGENT_EXE") != "" {
		base := os.Getenv("HRPLUS_M2_INSTALLED_CLOUD_URL")
		parsed, err := url.Parse(base)
		if err != nil || parsed.Scheme != "http" || (parsed.Hostname() != "localhost" && parsed.Hostname() != "127.0.0.1") || parsed.Port() == "" {
			t.Fatal("实装验收必须明确回环夹具地址")
		}
		listener, err := net.Listen("tcp", parsed.Host)
		if err != nil {
			t.Fatal("实装固定地址仍被占用，不启动测试", err)
		}
		cloud.Listener.Close()
		cloud.Listener = listener
		cloud.Start()
		cloud.URL = strings.TrimRight(base, "/")
		return
	}
	cloud.Start()
}

// nativeAcceptanceExecutable 核对实装摘要，或明确使用源码构建；两者证据分别记录。
func nativeAcceptanceExecutable(t *testing.T, root, directory string) string {
	t.Helper()
	if installed := os.Getenv("HRPLUS_M2_INSTALLED_AGENT_EXE"); installed != "" {
		expected := os.Getenv("HRPLUS_M2_INSTALLED_AGENT_SHA256")
		data, err := os.ReadFile(installed)
		if err != nil {
			t.Fatal(err)
		}
		digest := sha256.Sum256(data)
		if expected == "" || !strings.EqualFold(hex.EncodeToString(digest[:]), expected) {
			t.Fatal("实装程序摘要与候选不符")
		}
		if _, err = os.Stat(filepath.Join(filepath.Dir(installed), "worker-node", "src", "index.js")); err != nil {
			t.Fatal("实装 Worker 不存在", err)
		}
		t.Logf("实际安装程序：%s SHA256=%s", installed, expected)
		return installed
	}
	executable := filepath.Join(directory, "hrplus-m1-restart.exe")
	build := exec.Command("go", "build", "-o", executable, "./cmd/goodhr-local-agent")
	build.Dir = root
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("构建源码测试程序失败: %v %s", err, output)
	}
	return executable
}
