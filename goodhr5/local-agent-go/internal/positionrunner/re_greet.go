// 本文件编排复打招呼的 AI 决策、发送与云端上报，不包含平台页面差异。
// 复打招呼：对之前打过招呼但未回复的候选人，间隔一段时间后再主动发一次招呼消息。
// 候选人名单从云端拉取，复打内容由 AI 生成，结果上报云端维护 re_greet_count / last_re_greeted_at。
package positionrunner

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"goodhr5/local-agent-go/internal/actiondispatch"
	"goodhr5/local-agent-go/internal/cloudapi"
	"goodhr5/local-agent-go/internal/localai"
	"goodhr5/local-agent-go/internal/localdb"
	"goodhr5/local-agent-go/internal/platformcore"
	"goodhr5/local-agent-go/internal/platforms"
	"goodhr5/local-agent-go/internal/regreetupload"
)

// runReGreetTask 组装复打招呼依赖并执行整轮编排。
// ctx 为运行上下文，position 为岗位运行记录，options 为启动参数，snapshot 为运行时快照（含云端个人配置）。
func (r *Runner) runReGreetTask(ctx context.Context, position localdb.Position, options StartOptions, snapshot PositionRuntimeSnapshot) bool {
	positionID := position.ID
	platformRuntime, err := platforms.RuntimeFor(position.PlatformID)
	if err != nil {
		r.failStart(positionID, err.Error(), options)
		return false
	}
	runtime, ok := platformRuntime.(platformcore.ReGreetRuntime)
	if !ok {
		r.failStart(positionID, "当前平台暂不支持复打招呼", options)
		return false
	}
	pageRuntime, ok := platformRuntime.(platformcore.AutoReplyRuntime)
	if !ok {
		r.failStart(positionID, "当前平台暂不支持复打招呼", options)
		return false
	}
	generator, _, err := replyGeneratorFor(options)
	if err != nil {
		r.failStart(positionID, err.Error(), options)
		return false
	}
	return r.runReGreet(ctx, position, options, snapshot, pageRuntime, runtime, generator)
}

// runReGreet 执行复打招呼任务：启动浏览器 → 拉云端名单 → 逐个 AI 生成并发送 → 上报结果。
// pageRuntime 承担消息页准备与岗位核对；runtime 承担复打定位、输入、发送与确认。
func (r *Runner) runReGreet(ctx context.Context, position localdb.Position, options StartOptions, snapshot PositionRuntimeSnapshot, pageRuntime platformcore.AutoReplyRuntime, runtime platformcore.ReGreetRuntime, generator replyGenerator) bool {
	positionID := position.ID
	totalRounds := scanRounds(options)
	prefs := extractReGreetPrefs(options)
	stats := reGreetStats{}
	if options.reGreetTotals != nil {
		stats = *options.reGreetTotals
	}
	defer func() { r.updateReGreetStats(positionID, stats) }()
	defer func() {
		if options.reGreetTotals != nil {
			*options.reGreetTotals = stats
		}
	}()
	r.updateReGreetStats(positionID, stats)

	stopped := func(message string) {
		r.updateProgress(positionID, Progress{Stage: "stopped", Message: message, TotalRounds: totalRounds})
		_, _ = r.db.UpdatePositionStatus(positionID, "stopped")
		r.positionLog(positionID, "info", "复打招呼停止："+message)
		r.notifyCloudReGreetStatus(positionID, options, "stopped", stats)
	}
	fatal := func(err error) bool {
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
		return false
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
		return false
	}

	r.positionLog(positionID, "info", "复打招呼启动：正在打开消息页并核对岗位")
	if err := pageRuntime.PrepareReplyPage(ctx, exec); err != nil {
		r.failStart(positionID, "消息页准备失败："+err.Error(), options)
		return false
	}
	name := positionPositionName(position)
	r.positionLog(positionID, "info", "复打招呼核对岗位：岗位名="+name)
	target, err := pageRuntime.ResolveReplyTarget(ctx, exec, name)
	if err != nil {
		r.positionLog(positionID, "error", "复打招呼岗位核对失败：岗位名="+name+"，错误="+err.Error())
		r.failStart(positionID, "页面岗位核对失败："+err.Error(), options)
		return false
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

	// 跳过和失败也记录本次运行归属，上报失败必须在本地日志中可见。
	reportSkip := func(candidate cloudapi.ReGreetCandidate, reason string) {
		if err := cloudClient.ReportReGreetResult(ctx, options.Token, position.ID, platform, candidate.PlatformCandidateID, candidate.CandidateName, false, reason, cloudapi.ReGreetReportDetails{RunID: options.CloudRunID}); err != nil {
			r.positionLog(positionID, "warning", "复打结果上报失败：候选人="+candidate.CandidateName+"，原因="+reason+"，错误="+err.Error())
		}
	}

	r.updateProgress(positionID, Progress{Stage: "fetching", Message: "正在从云端拉取复打名单", TotalRounds: totalRounds})
	candidates, err := cloudClient.FetchReGreetCandidates(ctx, options.Token, position.ID, platform, prefs.timeRangeDays, prefs.intervalMinMinutes, prefs.maxCount, true)
	if err != nil {
		r.positionLog(positionID, "error", "复打招呼拉取名单失败："+err.Error())
		r.failStart(positionID, "拉取复打名单失败："+err.Error(), options)
		return false
	}
	r.positionLog(positionID, "info", fmt.Sprintf("复打招呼名单拉取完成：共 %d 人", len(candidates)))
	if options.reGreetBatch != nil {
		latest := map[string]cloudapi.ReGreetCandidate{}
		for _, candidate := range candidates {
			latest[candidate.PlatformCandidateID] = candidate
		}
		filtered := []cloudapi.ReGreetCandidate{}
		for _, expected := range options.reGreetBatch {
			if candidate, ok := latest[expected.PlatformCandidateID]; ok && expected.ReGreetCount == candidate.ReGreetCount && expected.GreetedAt == candidate.GreetedAt && expected.LastReGreetedAt == candidate.LastReGreetedAt {
				filtered = append(filtered, candidate)
			}
		}
		candidates = filtered
	}
	if options.reGreetTotals == nil {
		stats.total = len(candidates)
	}
	if options.reGreetRemaining != nil {
		*options.reGreetRemaining = candidates
	}
	r.updateReGreetStats(positionID, stats)
	if len(candidates) == 0 {
		r.positionLog(positionID, "info", "复打招呼：当前没有符合条件的候选人，任务结束")
		return true
	}

	r.updateProgress(positionID, Progress{Stage: "running", Message: fmt.Sprintf("正在对 %d 位候选人执行复打招呼", len(candidates)), TotalRounds: totalRounds})

	reGreetPrompt := positionReGreetPrompt(position, "")
	now := time.Now
	if options.actionNow != nil {
		now = options.actionNow
	}
	batchStarted := now()
	for i, candidate := range candidates {
		if options.reGreetRemaining != nil && actiondispatch.BatchLimit(batchStarted, now(), i) {
			break
		}
		if r.isUserStopped(positionID) || ctx.Err() != nil {
			stopped("复打招呼已停止")
			return false
		}
		candidateName := candidate.CandidateName
		if options.reGreetRemaining != nil {
			*options.reGreetRemaining = candidates[i+1:]
		}
		r.positionLog(positionID, "info", fmt.Sprintf("复打招呼进度 [%d/%d]：候选人=%s（%s）", i+1, len(candidates), candidateName, candidate.PlatformCandidateID))

		// 联系基准来自云端事实，到期时间独立保存，不等待上一位候选人的间隔。
		basis, basisErr := time.Parse(time.RFC3339Nano, firstNonEmptyString(candidate.LastReGreetedAt, candidate.GreetedAt))
		if basisErr != nil {
			stats.skipped++
			reportSkip(candidate, "identity_basis_unresolved")
			continue
		}
		due, dueErr := r.db.EnsureReGreetDue(ctx, platformcore.ReplyHash("profile:"+profileName), platform, candidate.PlatformCandidateID, fmt.Sprintf("%d:%s", candidate.ReGreetCount, basis.UTC().Format(time.RFC3339Nano)), basis.Add(randomInterval(prefs.intervalMinMinutes, prefs.intervalMaxMinutes)))
		if dueErr != nil {
			r.failStart(positionID, "复打到期安排保存失败", options)
			return false
		}
		if now().Before(due) {
			continue
		}
		pending, pendingErr := r.db.PendingReGreetForCandidate(ctx, platformcore.ReplyHash(profileName), platform, candidate.PlatformCandidateID)
		if pendingErr != nil {
			r.failStart(positionID, "复打补传状态读取失败", options)
			return false
		}
		if pending {
			stats.skipped++
			continue
		}
		identity, identityErr := r.verifiedCandidateIdentity(ctx, exec, runtime, platformcore.ReplyHash("profile:"+profileName), platform, candidate.PlatformCandidateID, candidateName)
		if identityErr != nil && !errors.Is(identityErr, sql.ErrNoRows) && !errors.Is(identityErr, platformcore.ErrReplyUnsafe) && !errors.Is(identityErr, localdb.ErrIdentityConflict) {
			r.failStart(positionID, "候选人身份记录读取失败", options)
			return false
		}
		locator, identitySupported := runtime.(platformcore.IdentityConversationLocator)
		if identityErr != nil || identity.Status != "verified" || !identitySupported {
			stats.skipped++
			reportSkip(candidate, "identity_unresolved")
			r.positionLog(positionID, "warning", "复打跳过：推荐与会话身份尚未直接核对，候选人="+candidateName)
			continue
		}
		conversation, locateErr := locator.LocateReplyConversationByID(ctx, exec, candidateName, identity.ConversationID)
		if locateErr != nil {
			if fatal(locateErr) {
				return false
			}
			r.positionLog(positionID, "warning", fmt.Sprintf("复打招呼定位候选人失败（%s）：%v", candidateName, locateErr))
			stats.failed++
			reportSkip(candidate, "locate_failed")
			r.updateReGreetStats(positionID, stats)
			continue
		}

		// 读取当前面板上下文（聊天历史），同时作为发送前基线用于成功确认。
		beforeCtx, readErr := runtime.ReadOpenedReplyContext(ctx, exec, target, conversation)
		if readErr != nil {
			if fatal(readErr) {
				return false
			}
			r.positionLog(positionID, "warning", fmt.Sprintf("复打招呼读取会话上下文失败（%s）：%v", candidateName, readErr))
			stats.failed++
			reportSkip(candidate, "read_context_failed")
			r.updateReGreetStats(positionID, stats)
			continue
		}

		// 页面事实优先于 AI：已回复、已收简历或上下文不明确时不生成消息。
		// 即使要跳过也先回写人工操作事实，下一次名单查询才不会再次包含陈旧记录。
		if err := r.reconcileCandidatePageState(ctx, position, options, candidate.PlatformCandidateID, candidateName, candidateStateFromReply(beforeCtx)); err != nil {
			r.positionLog(positionID, "warning", "平台页面状态同步失败，候选人="+candidateName+"，错误="+err.Error())
			stats.failed++
			reportSkip(candidate, "state_sync_failed")
			r.updateReGreetStats(positionID, stats)
			continue
		}
		if reason := reGreetSkipReason(beforeCtx); reason != "" {
			stats.skipped++
			if reason == "skipped_replied" {
				stats.skippedReplied++
			}
			if reason == "skipped_resume_received" {
				stats.skippedResume++
			}
			r.positionLog(positionID, "info", "复打招呼跳过："+candidateName+"，原因="+reason)
			reportSkip(candidate, reason)
			r.updateReGreetStats(positionID, stats)
			continue
		}
		// 复用本地发送意图存储；名单次数相同的候选人不能因上报失败而再次发送。
		key := localdb.AutoReplyRecord{ProfileScope: platformcore.ReplyHash(profileName), Platform: platform,
			ConversationID: "re_greet:" + candidate.PlatformCandidateID, InboundFingerprint: platformcore.ReplyHash(fmt.Sprintf("%s:%d", positionID, candidate.ReGreetCount)),
			PositionID: positionID, RunID: options.CloudRunID, ContextFingerprint: reGreetContextFingerprint(beforeCtx)}
		if existing, err := r.db.FindAutoReply(ctx, key); err == nil && (existing.Status == "sent" || existing.Status == "sending" || existing.Status == "unknown") {
			stats.skipped++
			r.positionLog(positionID, "warning", "复打招呼跳过：已有发送记录，请先核对云端次数，候选人="+candidateName)
			r.updateReGreetStats(positionID, stats)
			continue
		} else if err != nil && !errors.Is(err, sql.ErrNoRows) {
			r.failStart(positionID, "无法读取复打发送记录，任务已停止", options)
			return false
		}

		// 组装聊天历史文本
		conversationHistory := buildConversationHistory(beforeCtx.Messages)

		// AI 生成复打消息
		decision, genErr := generator.GenerateReGreet(ctx, localai.ReGreetRequest{
			ReGreetPrompt:       reGreetPrompt,
			CandidateName:       candidateName,
			PositionRequirement: positionRequirement(position),
			GreetMessage:        firstReGreetMessage(beforeCtx.Messages),
			ConversationHistory: conversationHistory,
			SkipRefusedCheck:    !positionReGreetSkipRefused(position),
		})
		if genErr != nil {
			if fatal(genErr) {
				return false
			}
			r.positionLog(positionID, "warning", fmt.Sprintf("复打招呼 AI 生成失败（%s）：%v", candidateName, genErr))
			stats.failed++
			reportSkip(candidate, "ai_generate_failed")
			r.updateReGreetStats(positionID, stats)
			continue
		}
		if decision.IsRefused && positionReGreetSkipRefused(position) {
			r.positionLog(positionID, "info", fmt.Sprintf("复打招呼候选人已拒绝（%s）：%s", candidateName, decision.RefuseReason))
			stats.skipped++
			stats.skippedRefused++
			reportSkip(candidate, "skipped_refused")
			r.updateReGreetStats(positionID, stats)
			continue
		}
		text := strings.TrimSpace(decision.Message)
		if utf8.RuneCountInString(text) > 200 {
			stats.skipped++
			reportSkip(candidate, "message_too_long")
			continue
		}
		if !decision.ShouldSend || text == "" {
			r.positionLog(positionID, "info", fmt.Sprintf("复打招呼 AI 决定跳过（%s）", candidateName))
			stats.skipped++
			reportSkip(candidate, "ai_skip")
			r.updateReGreetStats(positionID, stats)
			continue
		}

		// AI 生成后重新读取页面，防止发送期间出现新消息、人工草稿或账号切换。
		current, err := runtime.ReadOpenedReplyContext(ctx, exec, target, conversation)
		if fatal(err) {
			return false
		}
		if err != nil || !reGreetContextMatches(beforeCtx, current) || r.isUserStopped(positionID) {
			if err == nil && !r.isUserStopped(positionID) {
				if syncErr := r.reconcileCandidatePageState(ctx, position, options, candidate.PlatformCandidateID, candidateName, candidateStateFromReply(current)); syncErr != nil {
					r.positionLog(positionID, "warning", "变化后的页面状态同步失败："+syncErr.Error())
				}
			}
			stats.skipped++
			reportSkip(candidate, "context_changed")
			r.updateReGreetStats(positionID, stats)
			continue
		}
		key.ReplyFingerprint = platformcore.ReplyHash(text)
		record, err := r.db.PrepareAutoReply(ctx, key)
		if err != nil {
			r.failStart(positionID, "无法保存复打发送意图，任务已停止", options)
			return false
		}
		// 发送复打消息：输入 → 发送 → 面板新增出站文本确认。
		if stageErr := runtime.StageReGreet(ctx, exec, target, conversation, beforeCtx, text); stageErr != nil {
			if fatal(stageErr) {
				return false
			}
			r.positionLog(positionID, "warning", fmt.Sprintf("复打招呼输入消息失败（%s）：%v", candidateName, stageErr))
			stats.failed++
			reportSkip(candidate, "stage_failed")
			r.updateReGreetStats(positionID, stats)
			continue
		}
		if ctx.Err() != nil || r.isUserStopped(positionID) {
			stopped("复打招呼已停止")
			return false
		}
		if err := r.db.TransitionAutoReply(ctx, record.ID, "prepared", "sending", ""); err != nil {
			r.failStart(positionID, "无法保存复打发送状态，任务已停止", options)
			return false
		}
		if sendErr := runtime.SendReGreet(ctx, exec, target, conversation, beforeCtx, text); sendErr != nil {
			_ = r.db.TransitionAutoReply(context.WithoutCancel(ctx), record.ID, "sending", "unknown", "send_failed")
			stats.unknown++
			if fatal(sendErr) {
				return false
			}
			r.positionLog(positionID, "warning", fmt.Sprintf("复打招呼发送失败（%s）：%v", candidateName, sendErr))
			reportSkip(candidate, "send_failed")
			r.updateReGreetStats(positionID, stats)
			continue
		}
		confirmed, confirmErr := runtime.ConfirmReGreet(ctx, exec, target, conversation, beforeCtx, text)
		if confirmErr != nil || !confirmed {
			_ = r.db.TransitionAutoReply(context.WithoutCancel(ctx), record.ID, "sending", "unknown", "send_unconfirmed")
			stats.unknown++
			if fatal(confirmErr) {
				return false
			}
			r.positionLog(positionID, "warning", fmt.Sprintf("复打招呼发送未确认（%s）：err=%v", candidateName, confirmErr))
			reportSkip(candidate, "send_unconfirmed")
			r.updateReGreetStats(positionID, stats)
			continue
		}
		receiptRequest := cloudapi.ReGreetReceiptRequest{OperationID: record.ID, MachineID: options.MachineID, Platform: platform, PlatformCandidateID: candidate.PlatformCandidateID, CandidateName: candidateName, Success: true, RunID: options.CloudRunID, BaseCount: candidate.ReGreetCount, BaseContactAt: basis, SentAt: time.Now().UTC().Truncate(time.Microsecond), MessageText: text}
		receiptPayload, marshalErr := json.Marshal(struct {
			PositionID string `json:"position_id"`
			cloudapi.ReGreetReceiptRequest
		}{positionID, receiptRequest})
		if marshalErr != nil {
			r.failStart(positionID, "发送已确认，但收据无法保存", options)
			return false
		}
		if err := r.db.ConfirmReGreetAndQueue(context.WithoutCancel(ctx), localdb.ReGreetOutbox{OperationID: record.ID, ProfileScope: key.ProfileScope, Platform: platform, CandidateID: candidate.PlatformCandidateID, APIBase: cloudBase, Payload: receiptPayload}); err != nil {
			r.failStart(positionID, "发送已确认，但本地记录保存失败，请核对后再运行", options)
			return false
		}

		r.positionLog(positionID, "info", fmt.Sprintf("复打招呼已发送：%s → %s", candidateName, text))
		stats.sent++
		reportCtx, reportCancel := context.WithTimeout(context.WithoutCancel(ctx), cloudStatsSyncTimeout)
		acknowledged, reportErr := regreetupload.New(r.db).Flush(reportCtx, key.ProfileScope, options.Token)
		reportCancel()
		if reportErr != nil {
			r.positionLog(positionID, "warning", fmt.Sprintf("复打招呼上报失败（%s）：%v", candidateName, reportErr))
		} else if acknowledged == 0 {
			r.positionLog(positionID, "info", "复打已确认发送，结果已保存并等待云端收据：候选人="+candidateName)
		}
		r.ensureReGreetUploader()
		r.updateReGreetStats(positionID, stats)

	}

	r.positionLog(positionID, "info", fmt.Sprintf("复打招呼完成：共 %d 人，发送 %d，跳过 %d，失败 %d", stats.total, stats.sent, stats.skipped, stats.failed))
	return true
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
	unknown        int // 已进入发送但结果尚未确认，不能算成功或自动重发。
	total          int
	sent           int
	skipped        int
	failed         int
	skippedReplied int
	skippedRefused int
	skippedResume  int
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
	ctx, cancel := context.WithTimeout(context.Background(), cloudStatsSyncTimeout)
	defer cancel()
	taskType := options.TaskType
	if taskType == "" {
		taskType = "re_greet"
	}
	greeted := 0
	if hasTaskType(parseTaskTypes(taskType), "greeting") {
		greeted = r.currentRunGreeted(positionID)
	}
	_, err := client.SyncTaskStatus(ctx, options.Token, positionID, cloudapi.TaskStatusRequest{
		Status:    status,
		TaskType:  taskType,
		RunID:     options.CloudRunID,
		MachineID: options.MachineID,
		Greeted:   greeted,
		Skipped:   stats.skipped + stats.failed,
	})
	if err != nil {
		r.positionLog(positionID, "warning", "复打招呼云端状态同步失败："+err.Error())
	}
}

// positionReGreetPrompt 读取 ai_config 中的复打提示词，兼容旧快照，缺失时由 AI 层使用复打默认规则。
func positionReGreetPrompt(position localdb.Position, fallback string) string {
	if v := strings.TrimSpace(stringFromMap(mapValue(position.PositionSnapshot["ai_config"]), "re_greet_prompt")); v != "" {
		return v
	}
	if snapshot := position.PositionSnapshot; len(snapshot) > 0 {
		if v, ok := snapshot["re_greet_prompt"].(string); ok && strings.TrimSpace(v) != "" {
			return v
		}
	}
	return fallback
}

// reGreetRunning 判断任务是否包含复打，停止时立即取消尚未发送的消息。
func (r *Runner) reGreetRunning(positionID string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	state := r.running[positionID]
	return state != nil && hasTaskType(parseTaskTypes(state.options.TaskType), "re_greet")
}

// currentReGreetStats 返回复打统计摘要，消息正文不进入状态接口。
func (r *Runner) currentReGreetStats(positionID string) (map[string]int, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	state := r.running[positionID]
	if state == nil || !hasTaskType(parseTaskTypes(state.options.TaskType), "re_greet") {
		return nil, false
	}
	s := state.reGreetStats
	return map[string]int{"total": s.total, "sent": s.sent, "skipped": s.skipped, "failed": s.failed, "unknown": s.unknown, "skipped_replied": s.skippedReplied, "skipped_refused": s.skippedRefused, "skipped_resume_received": s.skippedResume}, true
}

// positionReGreetSkipRefused 读取岗位级拒绝检测开关，缺失时按前端默认值关闭。
func positionReGreetSkipRefused(position localdb.Position) bool {
	return boolFromMap(mapValue(position.PositionSnapshot["ai_config"]), "re_greet_skip_refused")
}

// reGreetSkipReason 根据页面事实判断是否可以复打，忽略末尾系统提示。
func reGreetSkipReason(current platformcore.ReplyContext) string {
	if current.ResumeStatus == "received" {
		return "skipped_resume_received"
	}
	if strings.TrimSpace(current.Draft) != "" {
		return "context_unsafe"
	}
	for _, message := range current.Messages {
		if message.Direction != "inbound" && message.Direction != "outbound" && message.Direction != "system" {
			return "context_unsafe"
		}
	}
	for i := len(current.Messages) - 1; i >= 0; i-- {
		switch current.Messages[i].Direction {
		case "system":
			continue
		case "inbound":
			return "skipped_replied"
		case "outbound":
			if current.ResumeStatus != "none" && current.ResumeStatus != "requested" {
				return "context_unsafe"
			}
			return ""
		}
	}
	return "context_unsafe"
}

// firstReGreetMessage 提取当前已加载聊天历史中最早的我方文字，缺失时留空而不编造。
func firstReGreetMessage(messages []platformcore.ReplyMessage) string {
	for _, m := range messages {
		if m.Direction == "outbound" && m.Kind == "text" && strings.TrimSpace(m.Text) != "" {
			return strings.TrimSpace(m.Text)
		}
	}
	return ""
}

// reGreetContextFingerprint 为复打上下文生成摘要，草稿和简历状态均参与复核。
func reGreetContextFingerprint(value platformcore.ReplyContext) string {
	raw, _ := json.Marshal(value)
	return platformcore.ReplyHash(string(raw))
}

// reGreetContextMatches 只允许同一会话、消息和草稿保持不变时发送生成结果。
func reGreetContextMatches(before, current platformcore.ReplyContext) bool {
	return reGreetSkipReason(current) == "" && reGreetContextFingerprint(before) == reGreetContextFingerprint(current)
}

// buildConversationHistory 将消息列表组装为聊天历史文本，供 AI 生成复打消息时参考。
func buildConversationHistory(messages []platformcore.ReplyMessage) string {
	if len(messages) == 0 {
		return ""
	}
	var sb strings.Builder
	for _, msg := range messages {
		role := "候选人"
		if msg.Direction == "system" {
			continue
		}
		if msg.Direction == "outbound" {
			role = "我"
		}
		if msg.Text != "" {
			sb.WriteString(fmt.Sprintf("%s: %s\n", role, msg.Text))
		}
	}
	return strings.TrimSpace(sb.String())
}
