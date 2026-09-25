// 本文件提供跨平台本地程序构建入口，统一读取环境配置、校验地址并注入 EXE。
package main

import (
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"

	"goodhr5/local-agent-go/internal/config"
	"goodhr5/local-agent-go/internal/version"
)

// main 执行环境构建；任何配置或编译错误都会以非零退出码结束。
func main() {
	if err := runBuild(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// runBuild 校验所选配置后调用 Go 编译器，产物路径和名称均包含环境标识。
func runBuild(args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("build-local-agent", flag.ContinueOnError)
	flags.SetOutput(stderr)
	environment := flags.String("env", os.Getenv("GOODHR_APP_ENV"), "打包环境：dev 或 prod，必须明确指定")
	configPath := flags.String("config", "", "环境配置 JSON 路径，默认 packaging/environments/<环境>.json")
	targetOS := flags.String("os", runtime.GOOS, "目标操作系统")
	targetArch := flags.String("arch", runtime.GOARCH, "目标架构")
	buildVersion := flags.String("version", version.Value, "程序版本号")
	outputDir := flags.String("output-dir", "", "输出目录，默认 dist/bin/<环境>")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("不支持额外参数：%v", flags.Args())
	}
	if *environment != "dev" && *environment != "prod" {
		return fmt.Errorf("请使用 -env dev 或 -env prod 明确选择打包环境")
	}
	if !regexp.MustCompile(`^[0-9A-Za-z._-]+$`).MatchString(*buildVersion) {
		return fmt.Errorf("版本号只能包含字母、数字、点、下划线和短横线")
	}
	for _, value := range []string{*targetOS, *targetArch} {
		if !regexp.MustCompile(`^[a-z0-9]+$`).MatchString(value) {
			return fmt.Errorf("操作系统与架构名称只能包含小写字母和数字")
		}
	}
	if *configPath == "" {
		*configPath = filepath.Join("packaging", "environments", *environment+".json")
	}
	cfg, err := config.LoadBuildConfig(*configPath, *environment)
	if err != nil {
		return err
	}
	cfg.Description = ""
	data, err := json.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("编码构建配置失败：%w", err)
	}
	encoded := base64.StdEncoding.EncodeToString(data)
	ldflags := "-X goodhr5/local-agent-go/internal/config.EmbeddedBuildConfig=" + encoded +
		" -X goodhr5/local-agent-go/internal/version.Value=" + *buildVersion
	ext := ""
	if *targetOS == "windows" {
		ext = ".exe"
		ldflags += " -H windowsgui"
	}
	if *outputDir == "" {
		*outputDir = filepath.Join("dist", "bin", *environment)
	}
	if err := os.MkdirAll(*outputDir, 0o755); err != nil {
		return fmt.Errorf("创建构建目录失败：%w", err)
	}
	output := filepath.Join(*outputDir, fmt.Sprintf("hrplus-agent-%s-%s-%s%s", *environment, *targetOS, *targetArch, ext))
	fmt.Fprintf(stdout, "[HRPlus] 环境=%s 前端=%s 后端=%s\n", cfg.Environment, cfg.ConsoleURL, cfg.CloudAPIBase)
	cmd := exec.Command("go", "build", "-trimpath", "-ldflags="+ldflags, "-o", output, "./cmd/goodhr-local-agent")
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if !strings.EqualFold(key, "CGO_ENABLED") && !strings.EqualFold(key, "GOOS") && !strings.EqualFold(key, "GOARCH") {
			cmd.Env = append(cmd.Env, entry)
		}
	}
	cmd.Env = append(cmd.Env, "CGO_ENABLED=0", "GOOS="+*targetOS, "GOARCH="+*targetArch)
	cmd.Stdout, cmd.Stderr = stdout, stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("Go 编译失败：%w", err)
	}
	fmt.Fprintf(stdout, "[HRPlus] 构建完成：%s\n", output)
	return nil
}
