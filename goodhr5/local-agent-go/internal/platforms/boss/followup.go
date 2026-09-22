// Package boss 文件作用：提供 Boss 基础筛选的平台扩展入口。
// Boss 打招呼使用平台默认沟通按钮，未实现打招呼后索要信息接口，
// 因此不实现 platformcore.CandidateInfoRequester，主流程会明确提示该平台不支持索要。
package boss

import (
	"context"

	"goodhr5/local-agent-go/internal/cloudapi"
	"goodhr5/local-agent-go/internal/platformcore"
)

// ApplyBasicFilters 保留 Boss 基础筛选入口，当前不执行页面操作。
func (r *Runtime) ApplyBasicFilters(context.Context, platformcore.Executor, cloudapi.PlatformConfig, map[string]any) error {
	return nil
}
