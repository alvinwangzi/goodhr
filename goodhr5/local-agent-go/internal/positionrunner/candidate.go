// Package positionrunner 文件作用：按职责承载本地岗位运行运行流程的拆分实现。
package positionrunner

import (
	"context"
	"errors"
	"fmt"
	"goodhr5/local-agent-go/internal/cloudapi"
	"goodhr5/local-agent-go/internal/localdb"
	"goodhr5/local-agent-go/internal/platformcore"
	"math"
	"math/rand"
	"strings"
	"time"
)

const (
	// restEntryRestoreAttempts 是休息窗口内回复检查后回到岗位列表页的最大重试次数。
	restEntryRestoreAttempts = 3
)

// consumeCandidateForGreet 按顺序消费一个候选人并执行打招呼。
// greetedSoFar 为岗位运行已打招呼数量。
func (r *Runner) consumeCandidateForGreet(ctx context.Context, position localdb.Position, platformRuntime platformcore.Runtime, exec platformExecutor, platformConfig cloudapi.PlatformConfig, candidate map[string]any, greetedSoFar int, options StartOptions) (int, int, int, error) {
	status := stringFromMap(candidate, "status")
	if status != "passed" && status != "ai_passed" && status != "detail_fetched" {
		r.positionLog(position.ID, "info", fmt.Sprintf("打招呼执行：跳过，候选人=%s，状态=%s", candidateLogName(candidate), status))
		return 0, 0, 0, nil
	}
	if position.MatchLimit > 0 && greetedSoFar >= position.MatchLimit {
		candidate["status"] = "skipped"
		candidate["skip_reason"] = "已达到岗位运行打招呼上限"
		return 0, 0, 1, nil
	}
	// 页面和系统的既有沟通事实优先于本轮评分，避免再次首次打招呼。
	if skip, err := r.skipFirstGreetFromPage(ctx, position, platformRuntime, exec, platformConfig, candidate, options); skip {
		if err != nil {
			r.positionLog(position.ID, "warning", "页面事实已确认，跳过首次招呼，但状态同步失败："+err.Error())
		}
		r.positionLog(position.ID, "info", "跳过首次打招呼：页面或系统已有沟通/简历记录，候选人="+candidateLogName(candidate))
		request := candidateInfoRequestFromPosition(position)
		allowed, _, _, hasScore := candidateInfoScoreDecision(position, candidate)
		if stringFromMap(candidate, "resume_status") == "received" {
			request.RequestResume = false
		}
		if allowed && hasScore && (request.RequestPhone || request.RequestWechat || request.RequestResume) {
			if enqueueErr := r.enqueueCandidateInfoRequest(position, candidate, request); enqueueErr != nil {
				r.positionLog(position.ID, "warning", "已有沟通候选人的索要意图未登记："+enqueueErr.Error())
			}
		}
		return 0, 0, 1, nil
	} else if err != nil {
		return 0, 1, 0, &candidateOperationError{Operation: "核对候选人页面状态", Err: err}
	}
	// 打招呼前模拟人工点击延时
	if err := waitBeforeGreet(ctx, r, position.ID, options); err != nil {
		return 0, 0, 0, err
	}
	r.positionLog(position.ID, "info", fmt.Sprintf("打招呼执行：准备执行，候选人=%s，已打招呼=%d", candidateLogName(candidate), greetedSoFar))
	request := candidateInfoRequestFromPosition(position)
	requestConfigured := candidateInfoRequestConfigured(request)
	requestAllowed, requestScore, requestThreshold, hasRequestScore := candidateInfoScoreDecision(position, candidate)
	candidate["_candidate_info_after_greet"] = requestConfigured && requestAllowed
	greetErr := r.tryGreet(ctx, position.ID, platformRuntime, exec, platformConfig, candidate, options)
	delete(candidate, "_candidate_info_after_greet")
	if greetErr != nil {
		var observed *platformcore.CandidateStateObservedError
		if errors.As(greetErr, &observed) {
			candidate["status"], candidate["contact_observed"] = "contacted", true
			candidate["resume_status"] = observed.State.ResumeStatus
			ext := mapValue(candidate["ext"])
			ext["contact_observed"] = true
			if observed.State.ResumeStatus == "received" {
				ext["resume_status"] = "received"
				candidate["status"] = "resume_received"
			}
			candidate["ext"] = ext
			if err := r.reconcileCandidatePageState(ctx, position, options, stringFromMap(candidate, "id"), candidateLogName(candidate), observed.State); err != nil {
				r.positionLog(position.ID, "warning", "页面状态同步失败："+err.Error())
			}
			return 0, 0, 1, nil
		}
		candidate["status"] = "failed"
		if options.LocalRunID != "" {
			candidate["status"] = "unknown"
		}
		candidate["error"] = greetErr.Error()
		r.positionLog(position.ID, "warning", fmt.Sprintf("打招呼执行：失败，候选人=%s，错误=%s", candidateLogName(candidate), greetErr.Error()))
		return 0, 1, 0, &candidateOperationError{Operation: "执行打招呼", Err: greetErr}
	}
	if requestConfigured && !hasRequestScore {
		r.positionLog(position.ID, "info", fmt.Sprintf("索要信息：跳过，候选人=%s，没有最终 AI 评分，索要分数=%.1f", candidateLogName(candidate), requestThreshold))
	} else if requestConfigured && !requestAllowed {
		r.positionLog(position.ID, "info", fmt.Sprintf("索要信息：跳过，候选人=%s，最终 AI 评分=%.1f，索要分数=%.1f，要求评分严格大于索要分数", candidateLogName(candidate), requestScore, requestThreshold))
	} else if requestConfigured {
		r.positionLog(position.ID, "info", fmt.Sprintf("索要信息：评分通过，候选人=%s，最终 AI 评分=%.1f，索要分数=%.1f，准备索要%s", candidateLogName(candidate), requestScore, requestThreshold, candidateInfoRequestLabel(request)))
	}
	var requestErr error
	requestAttempted := false
	if requestConfigured && requestAllowed {
		if hasRequestItems := request.RequestPhone || request.RequestWechat || request.RequestResume; hasRequestItems {
			// Boss 等平台的索要按钮需要候选人先回复才会解锁，打招呼后立即索要必然失败，
			// 因此改为把候选人记入待索要名单，岗位收尾时统一检查回复后再执行索要。
			if enqueueErr := r.enqueueCandidateInfoRequest(position, candidate, request); enqueueErr != nil {
				r.positionLog(position.ID, "warning", fmt.Sprintf("索要信息：写入待索要名单失败，本轮跳过索要，候选人=%s，错误=%s", candidateLogName(candidate), enqueueErr.Error()))
			} else {
				r.positionLog(position.ID, "info", fmt.Sprintf("索要信息：已记入待索要名单，候选人=%s，岗位结束后自动检查回复并索要%s", candidateLogName(candidate), candidateInfoRequestLabel(request)))
			}
		} else {
			requester, ok := platformRuntime.(platformcore.CandidateInfoRequester)
			if !ok {
				r.positionLog(position.ID, "warning", "索要信息：当前平台没有实现索要信息接口")
			} else {
				requestAttempted = true
				requestErr = r.withOperationTimeout(ctx, position.ID, candidateLogName(candidate), "调用索要信息接口", candidateInfoActionTimeout, func(requestCtx context.Context) error {
					return requester.RequestCandidateInfo(requestCtx, exec, platformConfig, platformcore.Candidate(candidate), request)
				})
			}
		}
	}
	if requestErr != nil {
		r.positionLog(position.ID, "warning", fmt.Sprintf("索要信息：执行失败但继续后续候选人，候选人=%s，索要项=%s，错误=%s", candidateLogName(candidate), candidateInfoRequestLabel(request), requestErr.Error()))
	} else if requestAttempted {
		// 索要动作真实执行成功后才写结果字段，云端据此落索要事件和已发送问候语事件。
		if request.RequestPhone {
			candidate["requested_phone"] = true
		}
		if request.RequestWechat {
			candidate["requested_wechat"] = true
		}
		if request.RequestResume {
			candidate["requested_resume"] = true
		}
		if message := strings.TrimSpace(request.GreetMessage); message != "" {
			candidate["greet_message_sent"] = message
		}
		r.positionLog(position.ID, "info", fmt.Sprintf("索要信息：执行完成，候选人=%s，索要项=%s", candidateLogName(candidate), candidateInfoRequestLabel(request)))
	}
	candidate["status"] = "greeted"
	candidate["greeted_at"] = time.Now().UTC().Format(time.RFC3339Nano)
	r.positionLog(position.ID, "info", "打招呼执行：成功，候选人="+candidateLogName(candidate))
	if position.EnableSound {
		r.playSound("success.wav", position.ID)
	}
	return 1, 0, 0, nil
}

// candidateInfoRequestFromPosition 从云端岗位快照读取索要信息勾选项和首次打招呼语。
func candidateInfoRequestFromPosition(position localdb.Position) platformcore.CandidateInfoRequest {
	snapshot := position.PositionSnapshot
	commonConfig := mapFromAny(snapshot["common_config"])
	return platformcore.CandidateInfoRequest{
		RequestPhone:  boolFromMap(commonConfig, "request_phone"),
		RequestWechat: boolFromMap(commonConfig, "request_wechat"),
		RequestResume: boolFromMap(commonConfig, "request_resume"),
		GreetMessage:  strings.TrimSpace(stringFromMap(snapshot, "greet_message")),
	}
}

// candidateInfoRequestConfigured 判断岗位是否配置了任一索要动作或追加问候语。
func candidateInfoRequestConfigured(request platformcore.CandidateInfoRequest) bool {
	return request.RequestPhone || request.RequestWechat || request.RequestResume || strings.TrimSpace(request.GreetMessage) != ""
}

// candidateInfoRequestLabel 按岗位勾选项生成索要项展示文案，如“手机号、微信、简历”。
// request 为岗位索要配置，返回值用于日志展示，没有任何勾选时返回“追加问候语”或“无”。
func candidateInfoRequestLabel(request platformcore.CandidateInfoRequest) string {
	items := make([]string, 0, 4)
	if request.RequestPhone {
		items = append(items, "手机号")
	}
	if request.RequestWechat {
		items = append(items, "微信")
	}
	if request.RequestResume {
		items = append(items, "简历")
	}
	if strings.TrimSpace(request.GreetMessage) != "" {
		items = append(items, "追加问候语")
	}
	if len(items) == 0 {
		return "无"
	}
	return strings.Join(items, "、")
}

// candidateInfoScoreDecision 判断候选人最终 AI 评分是否严格大于岗位索要分数。
func candidateInfoScoreDecision(position localdb.Position, candidate map[string]any) (allowed bool, score float64, threshold float64, hasScore bool) {
	aiConfig := mapFromAny(position.PositionSnapshot["ai_config"])
	greetThreshold := floatFromMapOr(aiConfig, "greet_score_threshold", 70)
	threshold = floatFromMapOr(aiConfig, "request_score_threshold", greetThreshold)
	value, exists := candidate["ai_greet_score"]
	if !exists || value == nil {
		return false, 0, threshold, false
	}
	score = float64Value(value, math.NaN())
	if math.IsNaN(score) {
		return false, 0, threshold, false
	}
	return score > threshold, score, threshold, true
}

// tryGreet 带重试地执行单个候选人打招呼。
// ctx 为请求上下文，platformConfig 为平台配置，candidate 为候选人。
func (r *Runner) tryGreet(ctx context.Context, positionID string, platformRuntime platformcore.Runtime, exec platformExecutor, platformConfig cloudapi.PlatformConfig, candidate map[string]any, options StartOptions) error {
	retries := maxInt(0, options.GreetRetries)
	// 新检查点路径不重试结果不明的发送，Worker 传输层也不能自动重复同一页面点击。
	if options.LocalRunID != "" {
		retries = 0
		exec.once = true
	}
	var lastErr error
	for attempt := 0; attempt <= retries; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		r.positionLog(positionID, "info", fmt.Sprintf("打招呼执行：准备调用平台接口，第%d次", attempt+1))
		err := r.withOperationTimeout(ctx, positionID, candidateLogName(candidate), fmt.Sprintf("调用打招呼接口第%d次", attempt+1), greetActionTimeout, func(greetCtx context.Context) error {
			return platformRuntime.GreetCandidate(greetCtx, exec, platformConfig, platformcore.Candidate(candidate))
		})
		if err == nil {
			return nil
		}
		var observed *platformcore.CandidateStateObservedError
		if errors.As(err, &observed) {
			return err
		}
		lastErr = err
		if attempt < retries {
			if err := sleepWithContext(ctx, 300*time.Millisecond); err != nil {
				return err
			}
		}
	}
	return lastErr
}

// waitBeforeGreet 在打招呼前随机等待。
// ctx 为请求上下文，options 为岗位运行启动参数。
// r 为 Runner 实例，用于写岗位运行日志。
func waitBeforeGreet(ctx context.Context, r *Runner, positionID string, options StartOptions) error {
	minDelay := options.GreetBeforeDelayMin
	maxDelay := options.GreetBeforeDelayMax
	if minDelay <= 0 && maxDelay <= 0 {
		return nil
	}
	if maxDelay < minDelay {
		maxDelay = minDelay
	}
	delay := minDelay
	if maxDelay > minDelay {
		delay += rand.Float64() * (maxDelay - minDelay)
	}
	if r != nil && positionID != "" {
		r.positionLog(positionID, "info", fmt.Sprintf("模拟人工操作：打招呼前，等待 %.1f 秒", delay))
	}
	return sleepWithContext(ctx, time.Duration(delay*float64(time.Second)))
}

// initRestState 初始化本次岗位运行的模拟休息计划。
// positionID 为岗位运行 ID，options 为岗位运行启动参数。
func (r *Runner) initRestState(positionID string, options StartOptions) {
	maxTimes := randomIntRange(options.RestTimesMin, options.RestTimesMax)
	nextAfter := randomIntRange(options.RestAfterCandidatesMin, options.RestAfterCandidatesMax)
	if maxTimes <= 0 || nextAfter <= 0 || options.RestDurationMax <= 0 {
		return
	}
	r.mu.Lock()
	state := r.running[positionID]
	if state != nil {
		state.restMaxTimes = maxTimes
		state.restUsed = 0
		state.restNextAfter = nextAfter
		state.restSinceLast = 0
	}
	r.mu.Unlock()
	r.positionLog(positionID, "info", fmt.Sprintf("模拟休息已启用：最多休息 %d 次，首次约处理 %d 人后休息", maxTimes, nextAfter))
}

// maybeRestAfterCandidate 在候选人处理后按计划模拟休息；休息窗口内优先检查候选人回复，剩余时间继续等待。
// ctx 为岗位运行上下文，position 为岗位运行记录，platformRuntime 为平台能力，exec 为浏览器执行器，platformConfig 为云端平台配置，options 为岗位运行启动参数。
func (r *Runner) maybeRestAfterCandidate(ctx context.Context, position localdb.Position, platformRuntime platformcore.Runtime, exec platformExecutor, platformConfig cloudapi.PlatformConfig, options StartOptions) error {
	positionID := position.ID
	r.mu.Lock()
	state := r.running[positionID]
	if state == nil || state.restMaxTimes <= 0 || state.restUsed >= state.restMaxTimes || state.restNextAfter <= 0 {
		r.mu.Unlock()
		return nil
	}
	state.restSinceLast++
	if state.restSinceLast < state.restNextAfter {
		r.mu.Unlock()
		return nil
	}
	processed := state.restSinceLast
	state.restUsed++
	restIndex := state.restUsed
	state.restSinceLast = 0
	state.restNextAfter = randomIntRange(options.RestAfterCandidatesMin, options.RestAfterCandidatesMax)
	r.mu.Unlock()

	maxDuration := options.RestDurationMax
	if maxDuration < options.RestDurationMin {
		maxDuration = options.RestDurationMin
	}
	durationMinutes := randomFloatRange(options.RestDurationMin, maxDuration)
	if durationMinutes <= 0 {
		return nil
	}
	duration := time.Duration(durationMinutes * float64(time.Minute))
	endsAt := time.Now().Add(duration)
	r.positionLog(positionID, "info", fmt.Sprintf("模拟休息：开始，已连续处理 %d 人，第 %d 次休息，预计休息 %s，结束时间=%s", processed, restIndex, formatRestDuration(duration), endsAt.Format("15:04:05")))
	if err := r.waitForSimulatedRest(ctx, position, platformRuntime, exec, platformConfig, options, restIndex, duration, endsAt); err != nil {
		return err
	}
	r.updateProgress(positionID, Progress{Stage: "running", Message: "模拟休息结束，继续处理候选人"})
	r.positionLog(positionID, "info", "模拟休息：结束，继续处理候选人")
	return nil
}

// waitForSimulatedRest 等待模拟休息结束；窗口内优先检查候选人回复并回到岗位列表页，剩余时间继续等待。
// 检查耗时计入休息时长，总休息节奏保持不变；浮层调用始终异步且忽略错误，页面展示异常不会影响岗位运行主流程。
func (r *Runner) waitForSimulatedRest(ctx context.Context, position localdb.Position, platformRuntime platformcore.Runtime, exec platformExecutor, platformConfig cloudapi.PlatformConfig, options StartOptions, restIndex int, duration time.Duration, endsAt time.Time) error {
	positionID := position.ID
	r.updateRestDisplay(positionID, exec, restIndex, duration, endsAt)
	if r.checkResumeRequestsDuringRest(ctx, position, platformRuntime, exec, platformConfig, options, endsAt) {
		// 检查会切到消息页，回到列表页后重新显示休息进度，避免用户误以为休息被打断。
		if err := ctx.Err(); err != nil {
			return err
		}
		r.updateRestDisplay(positionID, exec, restIndex, duration, endsAt)
	}
	remaining := time.Until(endsAt)
	if remaining <= 0 {
		return nil
	}
	return sleepWithContext(ctx, remaining)
}

// checkResumeRequestsDuringRest 在模拟休息窗口内检查候选人回复，检查耗时计入休息时长。
// 待索要名单为空、平台不支持回复检查或数据库不可用时返回 false，休息行为与原来保持一致。
func (r *Runner) checkResumeRequestsDuringRest(ctx context.Context, position localdb.Position, platformRuntime platformcore.Runtime, exec platformExecutor, platformConfig cloudapi.PlatformConfig, options StartOptions, endsAt time.Time) bool {
	if r.db == nil || ctx.Err() != nil {
		return false
	}
	if _, ok := platformRuntime.(platformcore.ResumeRequestChecker); !ok {
		return false
	}
	items, err := r.db.ListResumeRequests(position.ID, localdb.ResumeRequestStatusPending)
	if err != nil || len(items) == 0 {
		return false
	}
	windowCtx, cancel := context.WithDeadline(ctx, endsAt)
	defer cancel()
	r.positionLog(position.ID, "info", fmt.Sprintf("模拟休息：窗口内检查候选人回复，名单=%d 人，需在 %s 前完成", len(items), endsAt.Format("15:04:05")))
	r.performResumeChecks(windowCtx, position, platformRuntime, platformConfig, options)
	r.restoreEntryPageDuringRest(windowCtx, position, platformRuntime, exec, platformConfig)
	return true
}

// restoreEntryPageDuringRest 回复检查结束后把页面带回岗位列表页，失败时重试，不影响岗位继续运行。
func (r *Runner) restoreEntryPageDuringRest(ctx context.Context, position localdb.Position, platformRuntime platformcore.Runtime, exec platformExecutor, platformConfig cloudapi.PlatformConfig) {
	entryURL := platformEntryURL(platformConfig)
	if strings.TrimSpace(entryURL) == "" {
		r.positionLog(position.ID, "warning", "模拟休息：回复检查后云端平台配置缺少入口页面地址，无法回到列表页")
		return
	}
	for attempt := 1; attempt <= restEntryRestoreAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return
		}
		onEntry, err := platformRuntime.IsPositionEntryPage(ctx, exec, platformConfig)
		if err == nil && onEntry {
			r.positionLog(position.ID, "info", "模拟休息：已回到岗位列表页")
			return
		}
		if err := platformRuntime.OpenEntryPage(ctx, exec, platformConfig, entryURL); err != nil {
			r.positionLog(position.ID, "warning", fmt.Sprintf("模拟休息：回到岗位列表页失败，第 %d/%d 次，错误=%s", attempt, restEntryRestoreAttempts, err.Error()))
			continue
		}
		if err := r.waitPositionEntryPage(ctx, position.ID, platformRuntime, exec, platformConfig); err != nil {
			r.positionLog(position.ID, "warning", fmt.Sprintf("模拟休息：确认岗位列表页加载失败，第 %d/%d 次，错误=%s", attempt, restEntryRestoreAttempts, err.Error()))
			continue
		}
		r.prepareEntryPage(ctx, position.ID, platformRuntime, exec, platformConfig)
		r.positionLog(position.ID, "info", "模拟休息：已回到岗位列表页")
		return
	}
	r.positionLog(position.ID, "warning", "模拟休息：多次尝试后仍未回到岗位列表页，继续休息流程")
}

// updateRestDisplay 更新模拟休息进度，并以非阻塞方式显示浏览器页面浮层。
func (r *Runner) updateRestDisplay(positionID string, exec platformExecutor, restIndex int, duration time.Duration, endsAt time.Time) {
	message := fmt.Sprintf("本次休息 %s，预计 %s 继续处理", formatRestDuration(duration), endsAt.Format("15:04:05"))
	r.updateProgress(positionID, Progress{Stage: "resting", Message: message})
	r.showRestOverlayAsync(exec, restIndex, message, duration+time.Minute)
}

// showRestOverlayAsync 异步显示模拟休息浮层，任何 Worker 或页面异常都不会阻塞岗位运行。
func (r *Runner) showRestOverlayAsync(exec platformExecutor, restIndex int, message string, maxAge time.Duration) {
	if exec.runner == nil || exec.runner.worker == nil {
		return
	}
	go func() {
		defer func() { _ = recover() }()
		overlayCtx, overlayCancel := context.WithTimeout(context.Background(), overlayActionTimeout)
		defer overlayCancel()
		_, _ = exec.Post(overlayCtx, "/api/v1/page/ai-overlay", map[string]any{
			"action":     "show",
			"title":      "模拟休息中",
			"subtitle":   fmt.Sprintf("第 %d 次休息", restIndex),
			"message":    message,
			"max_age_ms": maxAge.Milliseconds(),
		})
	}()
}

// formatRestDuration 将休息时长格式化为便于用户阅读的分钟和秒数。
func formatRestDuration(duration time.Duration) string {
	if duration < 0 {
		duration = 0
	}
	duration = duration.Round(time.Second)
	minutes := int(duration / time.Minute)
	seconds := int((duration % time.Minute) / time.Second)
	if minutes <= 0 {
		return fmt.Sprintf("%d 秒", seconds)
	}
	if seconds <= 0 {
		return fmt.Sprintf("%d 分钟", minutes)
	}
	return fmt.Sprintf("%d 分 %d 秒", minutes, seconds)
}

// freshCandidates 过滤已见过的候选人。
// candidates 为候选人列表，seen 为已见候选人 ID 集合，返回新增候选人和重复数量。
func freshCandidates(candidates []map[string]any, seen map[string]struct{}) ([]map[string]any, int) {
	result := []map[string]any{}
	duplicateCount := 0
	for _, candidate := range candidates {
		id := stringFromMap(candidate, "id")
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			duplicateCount++
			continue
		}
		seen[id] = struct{}{}
		result = append(result, candidate)
	}
	return result, duplicateCount
}

// candidateMaps 将平台候选人转换成主流程保存用 map。
// candidates 为平台 runtime 返回的候选人列表。
func candidateMaps(candidates []platformcore.Candidate) []map[string]any {
	result := make([]map[string]any, 0, len(candidates))
	for _, candidate := range candidates {
		result = append(result, map[string]any(candidate))
	}
	return result
}

// prepareCandidatesForFirstStage 处理第一次基础分析前的候选人队列。
// position 为岗位运行记录，candidates 为候选人列表；有详情阶段时不在列表阶段做关键词终判。
func (r *Runner) prepareCandidatesForFirstStage(position localdb.Position, candidates []map[string]any) ([]map[string]any, int) {
	if positionMode(position) == "keyword" && !shouldFetchDetail(position) {
		return applyKeywordFilterWithDecision(position, candidates, func(message string) {
			r.positionLog(position.ID, "info", message)
		}, func(candidate map[string]any, state keywordMatchState) {
			r.updateKeywordAnalysis(position.ID, state, candidate, keywordAnalysisReason(candidate, state), true)
		})
	}
	return prepareCandidatesForFirstStage(position, candidates)
}

// prepareCandidatesForFirstStage 处理第一次基础分析前的候选人队列。
// position 为岗位运行记录，candidates 为候选人列表；有详情阶段时不在列表阶段做关键词终判。
func prepareCandidatesForFirstStage(position localdb.Position, candidates []map[string]any) ([]map[string]any, int) {
	if positionMode(position) == "keyword" && !shouldFetchDetail(position) {
		return applyKeywordFilter(position, candidates, nil)
	}
	for _, candidate := range candidates {
		if strings.TrimSpace(stringFromMap(candidate, "status")) == "" {
			candidate["status"] = "passed"
		}
	}
	return candidates, 0
}

// candidateLogName 返回候选人日志展示名称。
// candidate 为候选人字段集合。
func candidateLogName(candidate map[string]any) string {
	return firstNonEmptyString(
		stringFromMap(candidate, "candidate_name"),
		stringFromMap(candidate, "name"),
		stringFromMap(candidate, "id"),
		"候选人",
	)
}

// canContinueCandidate 判断候选人是否可以继续进入详情或 AI 阶段。
// status 为候选人当前状态。
func canContinueCandidate(status string) bool {
	status = strings.TrimSpace(status)
	return status == "" || status == "scanned" || status == "passed" || status == "detail_fetched" || status == "ai_passed"
}

// shouldSaveCandidateResult 判断候选人结果是否需要入库。
// status 为候选人当前状态，返回 true 表示该候选人是有效扫描结果。
func shouldSaveCandidateResult(status string) bool {
	status = strings.TrimSpace(status)
	return status == "scanned" || status == "passed" || status == "detail_fetched" || status == "ai_passed" || status == "greeted"
}
