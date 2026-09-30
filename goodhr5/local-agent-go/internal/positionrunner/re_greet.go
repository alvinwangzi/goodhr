// 本文件编排复打招呼的 AI 决策、发送与云端上报，不包含平台页面差异。
// 复打招呼：对之前打过招呼但未回复的候选人，间隔一段时间后再主动发一次招呼消息。
// 候选人名单从云端拉取，复打内容由 AI 生成，结果上报云端维护 re_greet_count / last_re_greeted_at。
package positionrunner

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"math/big"
	"path/filepath"
	"strings"
	"time"

	"goodhr5/local-agent-go/internal/cloudapi"
	"goodhr5/local-agent-go/internal/localai"
	"goodhr5/local-agent-go/internal/localdb"
	"goodhr5/local-agent-go/internal/platformcore"
	"goodhr5/local-agent-go/internal/platforms"
)

// runReGreetTask 组装复打招呼依赖并执行整轮编排。
// ctx 为运行上下文，position 为岗位运行记录，options 为启动参数，snapshot 为运行时快照（含云端个人配置）。
func (r *Runner) runReGreetTask(ctx context.Context, position localdb.Position, options StartOptions, snapshot PositionRuntimeSnapshot) {
	positionID := position.ID
	platformRuntime, err := platforms.RuntimeFor(position.PlatformID)
	if err != nil {
		r.failStart(positionID, err.Error(), options)
		return
	}
	runtime, ok := platformRuntime.(platformcore.ReGreetRuntime)
	if !ok {
		r.failStart(positionID, "当前平台暂不支持复打招呼", options)
		return
	}
	pageRuntime, ok := platformRuntime.(platformcore.AutoReplyRuntime)
	if !ok {
		r.failStart(positionID, "当前平台暂不支持复打招呼", options)
		return
	}
	generator, _, err := replyGeneratorFor(options)
	if err != nil {
		r.failStart(positionID, err.Error(), options)
		return
	}
	r.runReGreet(ctx, position, options, snapshot, pageRuntime, runtime, generator)
}

// runReGreet 执行复打招呼任务：启动浏览器 → 拉云端名单 → 逐个 AI 生成并发送 → 上报结果。
// pageRuntime 承担消息页准备与岗位核对；runtime 承担复打定位、输入、发送与确认。
func (r *Runner) runReGreet(ctx context.Context, position localdb.Position, options StartOptions, snapshot PositionRuntimeSnapshot, pageRuntime platformcore.AutoReplyRuntime, runtime platformcore.ReGreetRuntime, generator replyGenerator) {
	positionID := position.ID
	totalRounds := scanRounds(options)
	prefs := extractReGreetPrefs(options)
	stats := reGreetStats{}
	r.updateReGreetStats(positionID, stats)

	stopped := func(message string) {
		r.updateProgress(positionID, Progress{Stage: "stopped", Message: message, TotalRounds: totalRounds})
		_, _ = r.db.UpdatePositionStatus(positionID, "stopped")
		r.positionLog(positionID, "info", "复打招呼停止："+message)
		r.notifyCloudReGreetStatus(positionID, options, "stopped", stats)
	}
	_ = func(err error) bool {
		if err == nil {
			return false
		}
		if isBrowserClosedPositionError(err) {
			stopped("浏览器已关闭，复打招呼已结束")
			return true
		}
		if errors.Is(err, context.Canceled) {
			stopped("复打招呼已按停止请求结束")
			return true
		}
		return false
	}

	r.positionLog(positionID, "info", "复打招呼启动：正在启动浏览器")
	if _, err := r.worker.Start(ctx); err != nil {
		r.failStart(positionID, "浏览器启动失败："+err.Error(), options)
		return
	}
	exec := platformExecutor{runner: r, positionID: positionID, once: true}
	profileName := positionProfileName(position)
	if _, err := r.worker.CallOnce(ctx, "/api/v1/browser/start", map[string]any{
		"humanize":       true,
		"user_data_dir":  filepath.Join(r.profilesDir, profileName),
		"downloads_path": r.browserDownloadDir(),
		"no_script":      true,
	}); err != nil {
		r.failStart(positionID, "浏览器启动或显示校准失败："+err.Error(), options)
		return
	}

	r.positionLog(positionID, "info", "复打招呼启动：正在打开消息页并核对岗位")
	if err := pageRuntime.PrepareReplyPage(ctx, exec); err != nil {
		r.failStart(positionID, "消息页准备失败："+err.Error(), options)
		return
	}
	name := positionPositionName(position)
	r.positionLog(positionID, "info", "复打招呼核对岗位：岗位名="+name)
	target, err := pageRuntime.ResolveReplyTarget(ctx, exec, name)
	if err != nil {
		r.positionLog(positionID, "error", "复打招呼岗位核对失败：岗位名="+name+"，错误="+err.Error())
		r.failStart(positionID, "页面岗位核对失败："+err.Error(), options)
		return
	}
	r.positionLog(positionID, "info", "复打招呼岗位核对成功：positionID="+target.PositionID)

	cloudBase := strings.TrimSpace(options.CloudAPIBase)
	if cloudBase == "" {
		cloudBase = strings.TrimSpace(r.cloudAPIBase)
	}
	if cloudBase == "" {
		cloudBase = "https://www.xx.com"
	}
	cloudClient := cloudapi.New(cloudBase)
	platform := strings.ToLower(strings.TrimSpace(position.PlatformID))
	if platform == "" {
		platform = "boss"
	}

	r.updateProgress(positionID, Progress{Stage: "fetching", Message: "正在从云端拉取复打名单", TotalRounds: totalRounds})
	candidates, err := cloudClient.FetchReGreetCandidates(ctx, options.Token, position.ID, platform, prefs.timeRangeDays, prefs.intervalMinMinutes, prefs.maxCount)
	if err != nil {
		r.positionLog(positionID, "error", "复打招呼拉取名单失败："+err.Error())
		r.failStart(positionID, "拉取复打名单失败："+err.Error(), options)
		return
	}
	r.positionLog(positionID, "info", fmt.Sprintf("复打招呼名单拉取完成：共 %d 人", len(candidates)))
	stats.total = len(candidates)
	r.updateReGreetStats(positionID, stats)
	if len(candidates) == 0 {
		r.positionLog(positionID, "info", "复打招呼：当前没有符合条件的候选人，任务结束")
		stopped("没有需要复打的候选人")
		return
	}

	r.updateProgress(positionID, Progress{Stage: "running", Message: fmt.Sprintf("正在对 %d 位候选人执行复打招呼", len(candidates)), TotalRounds: totalRounds})

	reGreetPrompt := positionReGreetPrompt(position, positionReplyPrompt(position))
	for i, candidate := range candidates {
		if r.isUserStopped(positionID) || ctx.Err() != nil {
			stopped("复打招呼已停止")
			return
		}
		candidateName := candidate.CandidateName
		r.positionLog(positionID, "info", fmt.Sprintf("复打招呼进度 [%d/%d]：候选人=%s（%s）", i+1, len(candidates), candidateName, candidate.PlatformCandidateID))

		// 通过搜索框定位候选人会话
		conversation, locateErr := runtime.LocateReplyConversation(ctx, exec, candidateName)
		if locateErr != nil {
			r.positionLog(positionID, "warning", fmt.Sprintf("复打招呼定位候选人失败（%s）：%v", candidateName, locateErr))
			stats.failed++
			_ = cloudClient.ReportReGreetResult(ctx, options.Token, position.ID, platform, candidate.PlatformCandidateID, candidateName, false, "locate_failed")
			r.updateReGreetStats(positionID, stats)
			continue
		}

		// 读取当前面板上下文（聊天历史），同时作为发送前基线用于成功确认。
		beforeCtx, readErr := runtime.ReadOpenedReplyContext(ctx, exec, target, conversation)
		if readErr != nil {
			r.positionLog(positionID, "warning", fmt.Sprintf("复打招呼读取会话上下文失败（%s）：%v", candidateName, readErr))
			stats.failed++
			_ = cloudClient.ReportReGreetResult(ctx, options.Token, position.ID, platform, candidate.PlatformCandidateID, candidateName, false, "read_context_failed")
			r.updateReGreetStats(positionID, stats)
			continue
		}

		// 组装聊天历史文本
		conversationHistory := buildConversationHistory(beforeCtx.Messages)

		// AI 生成复打消息
		decision, genErr := generator.GenerateReGreet(ctx, localai.ReGreetRequest{
			ReGreetPrompt:       reGreetPrompt,
			CandidateName:       candidateName,
			PositionRequirement: positionRequirement(position),
			GreetMessage:        "", // TODO: 从云端获取首次打招呼消息
			ConversationHistory: conversationHistory,
			SkipRefusedCheck:    false,
		})
		if genErr != nil {
			r.positionLog(positionID, "warning", fmt.Sprintf("复打招呼 AI 生成失败（%s）：%v", candidateName, genErr))
			stats.failed++
			_ = cloudClient.ReportReGreetResult(ctx, options.Token, position.ID, platform, candidate.PlatformCandidateID, candidateName, false, "ai_generate_failed")
			r.updateReGreetStats(positionID, stats)
			continue
		}
		if decision.IsRefused {
			r.positionLog(positionID, "info", fmt.Sprintf("复打招呼候选人已拒绝（%s）：%s", candidateName, decision.RefuseReason))
			stats.skipped++
			_ = cloudClient.ReportReGreetResult(ctx, options.Token, position.ID, platform, candidate.PlatformCandidateID, candidateName, false, "skipped_refused")
			r.updateReGreetStats(positionID, stats)
			continue
		}
		text := strings.TrimSpace(decision.Message)
		if !decision.ShouldSend || text == "" {
			r.positionLog(positionID, "info", fmt.Sprintf("复打招呼 AI 决定跳过（%s）", candidateName))
			stats.skipped++
			_ = cloudClient.ReportReGreetResult(ctx, options.Token, position.ID, platform, candidate.PlatformCandidateID, candidateName, false, "ai_skip")
			r.updateReGreetStats(positionID, stats)
			continue
		}

		// 发送复打消息：输入 → 发送 → 面板新增出站文本确认
		if stageErr := runtime.StageReGreet(ctx, exec, target, conversation, text); stageErr != nil {
			r.positionLog(positionID, "warning", fmt.Sprintf("复打招呼输入消息失败（%s）：%v", candidateName, stageErr))
			stats.failed++
			_ = cloudClient.ReportReGreetResult(ctx, options.Token, position.ID, platform, candidate.PlatformCandidateID, candidateName, false, "stage_failed")
			r.updateReGreetStats(positionID, stats)
			continue
		}
		if sendErr := runtime.SendReGreet(ctx, exec, target, conversation, text); sendErr != nil {
			r.positionLog(positionID, "warning", fmt.Sprintf("复打招呼发送失败（%s）：%v", candidateName, sendErr))
			stats.failed++
			_ = cloudClient.ReportReGreetResult(ctx, options.Token, position.ID, platform, candidate.PlatformCandidateID, candidateName, false, "send_failed")
			r.updateReGreetStats(positionID, stats)
			continue
		}
		confirmed, confirmErr := runtime.ConfirmReGreet(ctx, exec, target, conversation, beforeCtx, text)
		if confirmErr != nil || !confirmed {
			r.positionLog(positionID, "warning", fmt.Sprintf("复打招呼发送未确认（%s）：err=%v", candidateName, confirmErr))
			stats.failed++
			_ = cloudClient.ReportReGreetResult(ctx, options.Token, position.ID, platform, candidate.PlatformCandidateID, candidateName, false, "send_unconfirmed")
			r.updateReGreetStats(positionID, stats)
			continue
		}

		r.positionLog(positionID, "info", fmt.Sprintf("复打招呼已发送：%s → %s", candidateName, text))
		stats.sent++
		reportErr := cloudClient.ReportReGreetResult(ctx, options.Token, position.ID, platform, candidate.PlatformCandidateID, candidateName, true, "")
		if reportErr != nil {
			r.positionLog(positionID, "warning", fmt.Sprintf("复打招呼上报失败（%s）：%v", candidateName, reportErr))
		}
		r.updateReGreetStats(positionID, stats)

		if i < len(candidates)-1 {
			delay := randomInterval(prefs.intervalMinMinutes, prefs.intervalMaxMinutes)
			r.positionLog(positionID, "info", fmt.Sprintf("复打招呼：等待 %v 后继续下一位", delay))
			select {
			case <-ctx.Done():
				stopped("复打招呼已停止")
				return
			case <-time.After(delay):
			}
		}
	}

	r.positionLog(positionID, "info", fmt.Sprintf("复打招呼完成：共 %d 人，发送 %d，跳过 %d，失败 %d", stats.total, stats.sent, stats.skipped, stats.failed))
	stopped("复打招呼已完成")
}

// reGreetPrefs 聚合启动参数中与复打招呼相关的 4 个字段，避免在循环里反复读 options。
type reGreetPrefs struct {
	intervalMinMinutes int
	intervalMaxMinutes int
	timeRangeDays      int
	maxCount           int
}

// extractReGreetPrefs 从启动参数中抽取复打个人配置；缺失时回退到清单默认值。
// 启动参数里的 4 个字段由 applyCloudPreferences 在启动阶段从云端个人配置写入。
func extractReGreetPrefs(options StartOptions) reGreetPrefs {
	prefs := reGreetPrefs{
		intervalMinMinutes: 30,
		intervalMaxMinutes: 50,
		timeRangeDays:      7,
		maxCount:           1,
	}
	if options.ReGreetIntervalMin > 0 {
		prefs.intervalMinMinutes = options.ReGreetIntervalMin
	}
	if options.ReGreetIntervalMax > 0 {
		prefs.intervalMaxMinutes = options.ReGreetIntervalMax
	}
	if options.ReGreetTimeRange > 0 {
		prefs.timeRangeDays = options.ReGreetTimeRange
	}
	if options.ReGreetMaxCount > 0 {
		prefs.maxCount = options.ReGreetMaxCount
	}
	if prefs.intervalMaxMinutes < prefs.intervalMinMinutes {
		prefs.intervalMaxMinutes = prefs.intervalMinMinutes
	}
	return prefs
}

// randomInterval 在 [minMinutes, maxMinutes] 之间取随机分钟数，模拟人工节奏。
// 使用 crypto/rand 而非 math/rand，避免安全扫描告警；调用频次低（每位候选人一次），性能可忽略。
func randomInterval(minMinutes, maxMinutes int) time.Duration {
	if maxMinutes <= minMinutes {
		return time.Duration(minMinutes) * time.Minute
	}
	delta := maxMinutes - minMinutes
	n, err := rand.Int(rand.Reader, big.NewInt(int64(delta+1)))
	if err != nil {
		return time.Duration(minMinutes) * time.Minute
	}
	return time.Duration(minMinutes+int(n.Int64())) * time.Minute
}

// reGreetStats 记录复打招呼本轮的累计计数，用于前端进度面板与云端状态同步。
type reGreetStats struct {
	total   int
	sent    int
	skipped int
	failed  int
}

// updateReGreetStats 把复打统计写入本地运行状态，供前端 /stats 接口读取。
func (r *Runner) updateReGreetStats(positionID string, stats reGreetStats) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if state, ok := r.running[positionID]; ok {
		state.reGreetStats = stats
	}
}

// notifyCloudReGreetStatus 把复打招呼的阶段性状态同步到云端，便于跨客户端查看。
func (r *Runner) notifyCloudReGreetStatus(positionID string, options StartOptions, status string, stats reGreetStats) {
	if options.Token == "" || options.CloudAPIBase == "" {
		return
	}
	client := cloudapi.New(options.CloudAPIBase)
	_, err := client.SyncTaskStatus(context.Background(), options.Token, positionID, cloudapi.TaskStatusRequest{
		Status:    status,
		TaskType:  "re_greet",
		RunID:     options.CloudRunID,
		MachineID: options.MachineID,
		Greeted:   stats.sent,
		Skipped:   stats.skipped + stats.failed,
	})
	if err != nil {
		r.positionLog(positionID, "warning", "复打招呼云端状态同步失败："+err.Error())
	}
}

// positionReGreetPrompt 返回复打招呼专用的 AI 提示词；岗位未配置时回退到普通打招呼提示词。
func positionReGreetPrompt(position localdb.Position, fallback string) string {
	if snapshot := position.PositionSnapshot; len(snapshot) > 0 {
		if v, ok := snapshot["re_greet_prompt"].(string); ok && strings.TrimSpace(v) != "" {
			return v
		}
	}
	return fallback
}

// buildConversationHistory 将消息列表组装为聊天历史文本，供 AI 生成复打消息时参考。
func buildConversationHistory(messages []platformcore.ReplyMessage) string {
	if len(messages) == 0 {
		return ""
	}
	var sb strings.Builder
	for _, msg := range messages {
		role := "候选人"
		if msg.Direction == "outbound" {
			role = "我"
		}
		if msg.Text != "" {
			sb.WriteString(fmt.Sprintf("%s: %s\n", role, msg.Text))
		}
	}
	return strings.TrimSpace(sb.String())
}
