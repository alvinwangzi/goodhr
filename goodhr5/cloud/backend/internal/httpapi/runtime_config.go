// 本文件负责向已登录用户提供本地程序和必要运行组件配置。
// 开发环境下，组件下载地址自动覆盖为本地后端 /uploads/ 路径。
package httpapi

import (
	"encoding/json"
	"net/http"
)

// RuntimeConfigService 处理本地程序与运行组件配置读取。
type RuntimeConfigService struct {
	auth          *AuthService
	systemConfigs SystemConfigStore
	config        Config
}

// NewRuntimeConfigService 创建运行组件配置服务。
func NewRuntimeConfigService(auth *AuthService, systemConfigs SystemConfigStore, cfg Config) *RuntimeConfigService {
	return &RuntimeConfigService{auth: auth, systemConfigs: systemConfigs, config: cfg}
}

// Current 返回当前可用的本地程序和运行组件配置。
// 开发环境下，runtime_components 的下载地址自动覆盖为本地后端 /uploads/ 路径。
func (s *RuntimeConfigService) Current(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if _, err := s.auth.SessionFromRequest(r); err != nil {
		writeError(w, http.StatusUnauthorized, "session is invalid or expired")
		return
	}
	config := map[string]any{
		"local_agent":        []any{},
		"runtime_components": map[string]any{},
	}
	if cfg, err := s.systemConfigs.Get("system.onboarding_config"); err == nil {
		_ = json.Unmarshal([]byte(cfg.ConfigValue), &config)
	}
	// 开发环境：把组件下载地址覆盖为本地后端 /uploads/ 路径，避免依赖外部 OSS。
	if s.config.IsDev() {
		applyDevComponentURLs(config)
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "config": config})
}

// applyDevComponentURLs 在开发环境下把 runtime_components 的下载地址改为本地后端。
func applyDevComponentURLs(config map[string]any) {
	components, ok := config["runtime_components"].(map[string]any)
	if !ok {
		return
	}
	for _, platform := range []string{"win", "mac"} {
		for _, comp := range []string{"cloakbrowser", "node_runtime", "ocr"} {
			if compMap, ok := components[comp].(map[string]any); ok {
				if platMap, ok := compMap[platform].(map[string]any); ok {
					if url, ok := platMap["url"].(string); ok && url != "" {
						// 提取文件名，拼成本地地址
						if idx := lastIndexByte(url, '/'); idx >= 0 {
							platMap["url"] = "http://localhost:8084/uploads/" + url[idx+1:]
						}
					}
				}
			}
		}
	}
}

func lastIndexByte(s string, c byte) int {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == c {
			return i
		}
	}
	return -1
}
