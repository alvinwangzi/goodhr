// 本文件复用 HRPlus 单岗位消息页准备和 AI 回复依赖，批次切换不重新启动岗位或重置计数。
package positionrunner

import (
	"context"
	"fmt"
	"goodhr5/local-agent-go/internal/cloudapi"
	"goodhr5/local-agent-go/internal/localai"
	"goodhr5/local-agent-go/internal/localdb"
	"goodhr5/local-agent-go/internal/platformcore"
	"path/filepath"
	"strings"
)

// prepareReplyFlow 组装同一岗位快照的消息流程；后续切换可复用浏览器并重新核对岗位。
func (r *Runner) prepareReplyFlow(ctx context.Context, position localdb.Position, options StartOptions, runtime platformcore.AutoReplyRuntime, generator replyGenerator, aiClient *localai.Client, startBrowser bool) (*replyFlow, error) {
	if startBrowser {
		if _, err := r.worker.Start(ctx); err != nil {
			return nil, fmt.Errorf("浏览器启动失败：%w", err)
		}
		if _, err := r.worker.CallOnce(ctx, "/api/v1/browser/start", map[string]any{"humanize": true, "user_data_dir": filepath.Join(r.profilesDir, positionProfileName(position)), "downloads_path": r.browserDownloadDir(), "no_script": true}); err != nil {
			return nil, fmt.Errorf("浏览器准备失败：%w", err)
		}
	}
	exec := platformExecutor{runner: r, positionID: position.ID, once: true}
	if err := runtime.PrepareReplyPage(ctx, exec); err != nil {
		return nil, fmt.Errorf("消息页准备失败：%w", err)
	}
	name := positionPositionName(position)
	scope, accountBound, scopeErr := r.bindPlatformAccountScope(ctx, exec, runtime, position, options)
	if scopeErr != nil {
		return nil, fmt.Errorf("登录账号核对失败：%w", scopeErr)
	}
	target, err := runtime.ResolveReplyTarget(ctx, exec, name)
	if err != nil {
		return nil, fmt.Errorf("页面岗位核对失败：%w", err)
	}
	base := firstNonEmptyString(options.CloudAPIBase, r.cloudAPIBase, "https://www.xx.com")
	legacyScope := ""
	if accountBound {
		legacyScope = platformcore.ReplyHash("profile:" + safePathName(positionProfileName(position)))
	}
	return &replyFlow{db: r.db, runtime: runtime, exec: exec, generator: generator, aiClient: aiClient, target: target,
		scope: scope, platform: strings.ToLower(strings.TrimSpace(position.PlatformID)), positionID: position.ID, runID: options.CloudRunID,
		legacyScope:    legacyScope,
		rejectTemplate: positionRejectTemplate(position), cloudClient: cloudapi.New(base), token: options.Token, positionSnapshot: position.PositionSnapshot, screenshotsDir: r.screenshotsDir,
		request: localai.ReplyRequest{PositionName: name, PositionRequirement: positionRequirement(position), ReplyPrompt: positionReplyPrompt(position), ReplySystemPrompt: options.AIConfig.ReplySystemPrompt, FAQ: positionFAQ(position)}}, nil
}
