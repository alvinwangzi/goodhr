// Package liepin 文件作用：提供猎聘企业端基础筛选的平台扩展入口。
// 猎聘企业端未实现打招呼后索要信息接口，因此不实现 platformcore.CandidateInfoRequester，
// 主流程会明确提示该平台不支持索要，避免候选人结果里出现未实际执行的索要标志。
package liepin

import (
	"context"

	"goodhr5/local-agent-go/internal/cloudapi"
	"goodhr5/local-agent-go/internal/platformcore"
)

// ApplyBasicFilters 保留猎聘企业端基础筛选入口，当前不执行页面操作。
func (r *Runtime) ApplyBasicFilters(context.Context, platformcore.Executor, cloudapi.PlatformConfig, map[string]any) error {
	return nil
}
