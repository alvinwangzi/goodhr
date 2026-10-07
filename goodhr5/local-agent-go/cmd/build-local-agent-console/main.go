// 本文件构建带终端窗口的开发环境 EXE，方便查看日志。
package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"goodhr5/local-agent-go/internal/config"
)

func main() {
	cfgPath := filepath.Join("packaging", "environments", "dev.json")
	cfg, err := config.LoadBuildConfig(cfgPath, "dev")
	if err != nil {
		fmt.Fprintln(os.Stderr, "读取配置失败：", err)
		os.Exit(1)
	}
	cfg.Description = ""
	data, err := json.Marshal(cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, "编码配置失败：", err)
		os.Exit(1)
	}
	encoded := base64.StdEncoding.EncodeToString(data)
	// 注意：不加 -H windowsgui，保留终端窗口；版本号用 0.1.2 通过前端版本检查
	ldflags := "-X goodhr5/local-agent-go/internal/config.EmbeddedBuildConfig=" + encoded +
		" -X goodhr5/local-agent-go/internal/version.Value=0.1.2"
	output := filepath.Join("dist", "bin", "dev-console", "hrplus-agent-dev-console.exe")
	os.MkdirAll(filepath.Dir(output), 0o755)
	cmd := exec.Command("go", "build", "-trimpath", "-ldflags="+ldflags, "-o", output, "./cmd/goodhr-local-agent")
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "编译失败：", err)
		os.Exit(1)
	}
	fmt.Println("[HRPlus] 构建完成（带终端）：", output)
}
