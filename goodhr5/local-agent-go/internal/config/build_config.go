// 本文件统一校验构建与启动环境配置；构建时只将公开地址写入 EXE，不包含任何密钥。
package config

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"
	"unicode"
)

// EmbeddedBuildConfig 由构建工具注入 Base64 编码的 JSON 配置；为空表示直接运行源码。
var EmbeddedBuildConfig string

// BuildConfig 保存所选环境的公开地址，开发与生产使用相同的配置结构。
type BuildConfig struct {
	Description        string `json:"_comment,omitempty"`
	Environment        string `json:"environment"`
	ConsoleURL         string `json:"console_url"`
	CloudAPIBase       string `json:"cloud_api_base"`
	ConsoleManifestURL string `json:"console_manifest_url,omitempty"`
}

// LoadBuildConfig 读取指定配置文件并验证它与打包时选择的环境一致。
func LoadBuildConfig(path, environment string) (BuildConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return BuildConfig{}, fmt.Errorf("读取打包配置失败：%w", err)
	}
	cfg, err := parseBuildConfig(data)
	if err != nil {
		return BuildConfig{}, err
	}
	if cfg.Environment != environment {
		return BuildConfig{}, fmt.Errorf("所选环境 %q 与配置环境 %q 不一致", environment, cfg.Environment)
	}
	return cfg, nil
}

// RuntimeBuildConfig 返回包内配置；直接运行源码时才从进程环境变量读取地址。
func RuntimeBuildConfig() (BuildConfig, error) {
	if EmbeddedBuildConfig != "" {
		data, err := base64.StdEncoding.DecodeString(EmbeddedBuildConfig)
		if err != nil {
			return BuildConfig{}, fmt.Errorf("包内环境配置损坏，请重新打包：%w", err)
		}
		return parseBuildConfig(data)
	}
	cfg := BuildConfig{
		Environment:        os.Getenv("GOODHR_APP_ENV"),
		ConsoleURL:         os.Getenv("GOODHR_CONSOLE_URL"),
		CloudAPIBase:       os.Getenv("GOODHR_CLOUD_API_BASE"),
		ConsoleManifestURL: os.Getenv("GOODHR_CONSOLE_MANIFEST_URL"),
	}
	return cfg, cfg.validate()
}

// parseBuildConfig 严格解析单个 JSON 配置，拒绝拼错的字段与多余对象。
func parseBuildConfig(data []byte) (BuildConfig, error) {
	var cfg BuildConfig
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&cfg); err != nil {
		return cfg, fmt.Errorf("解析环境配置失败：%w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return cfg, fmt.Errorf("环境配置只能包含一个 JSON 对象")
	}
	return cfg, cfg.validate()
}

// validate 校验环境名称、必填地址和可选下载地址，不允许缺省回退到其他环境。
func (c *BuildConfig) validate() error {
	c.Environment = strings.TrimSpace(c.Environment)
	c.ConsoleURL = strings.TrimSpace(c.ConsoleURL)
	c.CloudAPIBase = strings.TrimRight(strings.TrimSpace(c.CloudAPIBase), "/")
	c.ConsoleManifestURL = strings.TrimSpace(c.ConsoleManifestURL)
	if c.Environment != "dev" && c.Environment != "prod" {
		return fmt.Errorf("请明确配置 dev 或 prod 环境")
	}
	for _, item := range []struct{ name, value string }{
		{"console_url", c.ConsoleURL},
		{"cloud_api_base", c.CloudAPIBase},
		{"console_manifest_url", c.ConsoleManifestURL},
	} {
		if item.name == "console_manifest_url" && item.value == "" {
			continue
		}
		u, err := url.Parse(item.value)
		if err != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || strings.IndexFunc(item.value, unicode.IsSpace) >= 0 {
			return fmt.Errorf("%s 必须配置为不含账号密码的完整 http/https 地址", item.name)
		}
		if item.name == "cloud_api_base" && (u.RawQuery != "" || u.Fragment != "") {
			return fmt.Errorf("cloud_api_base 不能包含查询参数或锚点")
		}
	}
	return nil
}
