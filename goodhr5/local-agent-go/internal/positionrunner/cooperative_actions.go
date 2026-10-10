// 本文件把 HRPlus 单岗位找简历、回复和到期复打接到同一页面通道，只在候选人安全边界切换。
package positionrunner

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"goodhr5/local-agent-go/internal/actiondispatch"
	"goodhr5/local-agent-go/internal/cloudapi"
	"goodhr5/local-agent-go/internal/localdb"
	"goodhr5/local-agent-go/internal/platformcore"
	"goodhr5/local-agent-go/internal/platforms"
)

// dueReGreet 保存已到期的候选人事实，未来工作不延长当前任务生命周期。
type dueReGreet struct {
	candidate cloudapi.ReGreetCandidate
	due       time.Time
	basis     string
	queuedAt  time.Time
}

// actionSession 保存同一个 TaskRun 内的动作队列，不创建计划或多岗位数据。
type actionSession struct {
	runner            *Runner
	position          localdb.Position
	options           StartOptions
	snapshot          PositionRuntimeSnapshot
	runtime           platformcore.Runtime
	resumer           platformcore.RecommendationResumer
	flow              *replyFlow
	scheduler         *actiondispatch.Scheduler
	now               func() time.Time
	replies           []platformcore.ReplyConversation
	reGreets          []dueReGreet
	handledReGreets   map[string]bool
	replyStats        platformcore.ReplyStats
	reGreetStats      reGreetStats
	onRecommendation  bool
	cursor            platformcore.RecommendationCursor
	needsRescan       bool
	lastScanSignature string
	pendingScanRescan bool
	lastChecked       time.Time
	infoQueue         []string
	handledInfo       map[string]bool
	greetingRemaining bool
	lastBatch         actiondispatch.Action
}

// newActionSession 复用原回复准备和岗位快照，三种动作不会再次发起独立岗位启动。
func (r *Runner) newActionSession(ctx context.Context, position localdb.Position, options StartOptions, snapshot PositionRuntimeSnapshot) (*actionSession, error) {
	runtime, err := platforms.RuntimeFor(position.PlatformID)
	if err != nil {
		return nil, err
	}
	messageRuntime, ok := runtime.(platformcore.AutoReplyRuntime)
	if !ok {
		return nil, fmt.Errorf("当前平台不支持单岗位消息调度")
	}
	resumer, canResume := runtime.(platformcore.RecommendationResumer)
	if hasTaskType(parseTaskTypes(options.TaskType), "greeting") && !canResume {
		return nil, fmt.Errorf("当前平台尚未支持安全恢复推荐进度")
	}
	generator, aiClient, err := replyGeneratorFor(options)
	if err != nil {
		return nil, err
	}
	flow, err := r.prepareReplyFlow(ctx, position, options, messageRuntime, generator, aiClient, true)
	if err != nil {
		return nil, err
	}
	now := time.Now
	if options.actionNow != nil {
		now = options.actionNow
	}
	s := &actionSession{runner: r, position: position, options: options, snapshot: snapshot, runtime: runtime, resumer: resumer, flow: flow, scheduler: actiondispatch.New(options.PrioritizeReply), now: now, handledReGreets: map[string]bool{}}
	if hasTaskType(parseTaskTypes(options.TaskType), "auto_reply") {
		r.updateReplyStats(position.ID, s.replyStats)
	}
	return s, nil
}

// enterMessages 保存切换锚点并通过菜单进入沟通，重新核对同一岗位，不能复用上次面板身份。
func (s *actionSession) enterMessages(ctx context.Context) error {
	var prepared *platformcore.ReplyTarget
	if !s.onRecommendation {
		prepared = &s.flow.target
	}
	if s.onRecommendation {
		checkpoint, err := s.runner.db.LoadActionCheckpoint(ctx, s.options.LocalRunID)
		if err != nil {
			return err
		}
		cursor, err := s.resumer.CaptureRecommendationCursor(ctx, s.flow.exec, checkpoint.Anchors)
		if err != nil {
			cursor = platformcore.RecommendationCursor{Reason: "anchor_capture_failed"}
		}
		s.cursor = cursor
	}
	target, err := ensureReplyTarget(ctx, s.flow.runtime, s.flow.exec, positionPositionName(s.position), prepared)
	if err != nil {
		return err
	}
	s.flow.target = target
	s.onRecommendation = false
	return nil
}

// refreshWork 在安全边界检查当前消息和个人复打到期，不让计时器自行操作页面。
func (s *actionSession) refreshWork(ctx context.Context) error {
	if err := s.enterMessages(ctx); err != nil {
		return err
	}
	types := parseTaskTypes(s.options.TaskType)
	if s.handledInfo == nil || s.greetingRemaining {
		s.handledInfo = map[string]bool{}
	}
	if candidateInfoRequestConfigured(candidateInfoRequestFromPosition(s.position)) {
		items, err := s.runner.db.ListCandidateInfoRequests(s.position.ID)
		if err != nil {
			return err
		}
		known := map[string]bool{}
		for _, id := range s.infoQueue {
			known[id] = true
		}
		selected := candidateInfoRequestFromPosition(s.position)
		wanted := map[string]bool{"phone": selected.RequestPhone, "wechat": selected.RequestWechat, "resume": selected.RequestResume}
		for _, item := range items {
			if known[item.ID] || s.handledInfo[item.ID] {
				continue
			}
			active := false
			for action, enabled := range item.Actions {
				if enabled && wanted[action] && item.Results[action] != "requested" && item.Results[action] != "satisfied" {
					active = true
				}
			}
			if active {
				s.infoQueue = append(s.infoQueue, item.ID)
				known[item.ID] = true
			}
		}
	}
	signatureParts := []string{}
	if hasTaskType(types, "auto_reply") {
		conversations, err := s.flow.runtime.ScanUnreadReplies(ctx, s.flow.exec, s.flow.target, 100)
		if err != nil {
			return err
		}
		known := map[string]bool{}
		for _, conversation := range s.replies {
			known[conversation.ID] = true
		}
		for _, conversation := range conversations {
			signatureParts = append(signatureParts, "reply:"+conversation.ID)
			if conversation.ID != "" && !known[conversation.ID] {
				s.replies = append(s.replies, conversation)
				known[conversation.ID] = true
			}
		}
	}
	if hasTaskType(types, "re_greet") {
		prefs := extractReGreetPrefs(s.options)
		candidates, err := s.flow.cloudClient.FetchReGreetCandidates(ctx, s.options.Token, s.position.ID, s.flow.platform, prefs.timeRangeDays, prefs.intervalMinMinutes, prefs.maxCount, true)
		if err != nil {
			return err
		}
		known := map[string]bool{}
		for _, item := range s.reGreets {
			known[item.basis] = true
		}
		initialDue, waiting, invalidContact, handled := len(s.reGreets), 0, 0, 0
		for _, candidate := range candidates {
			contact, err := time.Parse(time.RFC3339Nano, firstNonEmptyString(candidate.LastReGreetedAt, candidate.GreetedAt))
			if err != nil {
				invalidContact++
				continue
			}
			basis := fmt.Sprintf("%s:%d:%s", candidate.PlatformCandidateID, candidate.ReGreetCount, contact.UTC().Format(time.RFC3339Nano))
			if known[basis] || s.handledReGreets[basis] {
				handled++
				continue
			}
			due, err := s.runner.db.EnsureReGreetDue(ctx, s.flow.scope, s.flow.platform, candidate.PlatformCandidateID, fmt.Sprintf("%d:%s", candidate.ReGreetCount, contact.UTC().Format(time.RFC3339Nano)), contact.Add(randomInterval(prefs.intervalMinMinutes, prefs.intervalMaxMinutes)))
			if err != nil {
				return err
			}
			if !s.now().Before(due) {
				signatureParts = append(signatureParts, "regreet:"+basis)
				s.reGreets = append(s.reGreets, dueReGreet{candidate: candidate, due: due, basis: basis, queuedAt: s.now()})
				s.reGreetStats.total++
				known[basis] = true
			} else {
				waiting++
			}
		}
		s.runner.positionLog(s.position.ID, "info", fmt.Sprintf("复打名单检查：返回%d人，新增到期%d人，当前待处理%d人，未到时间%d人，已处理或已排队%d人，联系时间缺失%d人", len(candidates), len(s.reGreets)-initialDue, len(s.reGreets), waiting, handled, invalidContact))
		sort.SliceStable(s.reGreets, func(i, j int) bool { return s.reGreets[i].due.Before(s.reGreets[j].due) })
	}
	s.scheduler.Checked(s.now())
	s.lastChecked = s.now()
	sort.Strings(signatureParts)
	s.lastScanSignature = platformcore.ReplyHash(strings.Join(signatureParts, "\n"))
	return s.saveDispatchCheckpoint(ctx, actiondispatch.CheckMessages)
}

// saveDispatchCheckpoint 保存动作与消息检查时间，成功数量继续来自同一单岗位运行。
func (s *actionSession) saveDispatchCheckpoint(ctx context.Context, action actiondispatch.Action) error {
	if s.options.LocalRunID == "" {
		return nil
	}
	checkpoint, err := s.runner.db.LoadActionCheckpoint(ctx, s.options.LocalRunID)
	if err != nil {
		return err
	}
	checkpoint.CurrentAction = string(action)
	checkpoint.NextMessageCheck = s.scheduler.NextCheck
	checkpoint.Replied = s.replyStats.Replied
	checkpoint.ReGreeted = s.reGreetStats.sent
	checkpoint.LastMessageCheck = s.lastChecked
	checkpoint.ReplyStats = map[string]int{"checked": s.replyStats.Checked, "replied": s.replyStats.Replied, "accepted_resume": s.replyStats.AcceptedResume, "skipped": s.replyStats.Skipped, "failed": s.replyStats.Failed, "unknown": s.replyStats.Unknown}
	checkpoint.ReGreetStats = map[string]int{"total": s.reGreetStats.total, "sent": s.reGreetStats.sent, "skipped": s.reGreetStats.skipped, "failed": s.reGreetStats.failed, "unknown": s.reGreetStats.unknown}
	if action == actiondispatch.Done {
		checkpoint.NextMessageCheck = time.Time{}
	}
	checkpoint.MessageState, err = s.messageState(checkpoint)
	if err != nil {
		return err
	}
	return s.runner.db.SaveActionCheckpoint(ctx, checkpoint)
}

// returnRecommendation 优先核对三个局部锚点；变化时才真实滚回起点，由扫描器按运行状态去重。
func (s *actionSession) returnRecommendation(ctx context.Context) error {
	if s.onRecommendation {
		return nil
	}
	matched, err := restoreRecommendationCursor(ctx, s.runner, s.position, s.snapshot.PlatformConfig, s.runtime, s.resumer, s.flow.exec, s.cursor)
	if err != nil {
		return err
	}
	if !matched {
		s.needsRescan = true
	}
	s.onRecommendation = true
	return s.saveDispatchCheckpoint(ctx, actiondispatch.Greeting)
}

// restoreRecommendationCursor 复用 M1 菜单、岗位和三个局部锚点核对，原位置变化时才真实回滚。
func restoreRecommendationCursor(ctx context.Context, runner *Runner, position localdb.Position, config cloudapi.PlatformConfig, runtime platformcore.Runtime, resumer platformcore.RecommendationResumer, exec platformcore.Executor, cursor platformcore.RecommendationCursor) (bool, error) {
	if err := resumer.ReturnToRecommendation(ctx, exec); err != nil {
		return false, err
	}
	current, err := runtime.CurrentPositionName(ctx, exec, config)
	if err != nil {
		return false, err
	}
	if !strings.Contains(normalizePositionName(current), normalizePositionName(positionPositionName(position))) {
		if err := runtime.SelectPosition(ctx, exec, config, positionPositionName(position)); err != nil {
			return false, err
		}
		cursor.Valid = false
		cursor.Reason = "position_changed"
	}
	matched, reason, err := resumer.CheckRecommendationCursor(ctx, exec, cursor)
	if err != nil {
		matched = false
		reason = "anchor_read_failed"
	}
	if matched {
		runner.positionLog(position.ID, "info", "resume_anchor_match：局部锚点匹配，保留队列和本次数量继续")
	} else {
		if err := resumer.RewindRecommendation(ctx, exec); err != nil {
			return false, err
		}
		runner.positionLog(position.ID, "info", "resume_rescan：按本次记录恢复，原因="+reason)
	}
	return matched, nil
}

// service 在当前工作间交替执行有限批次，停止后不再领取新会话，无工作时立即结束。
func (s *actionSession) service(ctx context.Context, greetingRemaining bool, forceCheck bool) (bool, error) {
	return s.serviceBounded(ctx, greetingRemaining, forceCheck, false)
}

// serviceBounded 复用 M1 动作实现，计划调用只执行一批消息后交回主调度，独立岗位保持原循环。
func (s *actionSession) serviceBounded(ctx context.Context, greetingRemaining bool, forceCheck bool, singleBatch bool) (bool, error) {
	s.lastBatch = ""
	s.greetingRemaining = greetingRemaining
	if forceCheck {
		s.scheduler.NextCheck = time.Time{}
	}
	for {
		if ctx.Err() != nil || s.runner.isUserStopped(s.position.ID) {
			s.scheduler.Stop()
			return false, context.Canceled
		}
		types := parseTaskTypes(s.options.TaskType)
		work := actiondispatch.Work{Greeting: greetingRemaining, Reply: len(s.replies) > 0 && hasTaskType(types, "auto_reply"), ReGreet: len(s.reGreets) > 0 && hasTaskType(types, "re_greet"), CandidateInfo: len(s.infoQueue) > 0}
		if len(s.reGreets) > 0 {
			work.ReGreetDue = s.reGreets[0].due
			work.ReGreetWaitingSince = s.reGreets[0].queuedAt
		}
		action := s.scheduler.Next(s.now(), work)
		s.runner.updateProgress(s.position.ID, Progress{Stage: "running", Message: "单岗位当前动作：" + string(action)})
		switch action {
		case actiondispatch.CheckMessages:
			if err := s.refreshWork(ctx); err != nil {
				return false, err
			}
		case actiondispatch.Reply:
			s.lastBatch = action
			if err := s.enterMessages(ctx); err != nil {
				return false, err
			}
			batch, err := processReplyBatch(ctx, s.flow, s.replies, s.now, func() bool { return s.runner.isUserStopped(s.position.ID) }, func(conversation platformcore.ReplyConversation, outcome string) {
				s.runner.reportAutoReplyScreening(ctx, s.position, s.options, conversation, outcome)
			})
			s.replies = batch.Remaining
			s.replyStats.Checked += batch.Stats.Checked
			s.replyStats.Replied += batch.Stats.Replied
			s.replyStats.AcceptedResume += batch.Stats.AcceptedResume
			s.replyStats.Skipped += batch.Stats.Skipped
			s.replyStats.Failed += batch.Stats.Failed
			s.replyStats.Unknown += batch.Stats.Unknown
			s.runner.updateReplyStats(s.position.ID, s.replyStats)
			s.scheduler.Completed(action)
			if saveErr := s.saveDispatchCheckpoint(context.WithoutCancel(ctx), action); saveErr != nil {
				return false, saveErr
			}
			if err != nil {
				return false, err
			}
		case actiondispatch.ReGreet:
			s.lastBatch = action
			if err := s.enterMessages(ctx); err != nil {
				return false, err
			}
			count := minInt(3, len(s.reGreets))
			selected := s.reGreets[:count]
			tail := append([]dueReGreet{}, s.reGreets[count:]...)
			candidates := []cloudapi.ReGreetCandidate{}
			for _, item := range selected {
				candidates = append(candidates, item.candidate)
			}
			remaining := []cloudapi.ReGreetCandidate{}
			options := s.options
			options.reGreetBatch = candidates
			options.reGreetRemaining = &remaining
			options.reGreetTotals = &s.reGreetStats
			options.preparedReplyTarget = &s.flow.target
			options.actionNow = s.now
			runtime, ok := s.runtime.(platformcore.ReGreetRuntime)
			if !ok {
				return false, fmt.Errorf("平台未提供复打能力")
			}
			completed := s.runner.runReGreet(ctx, s.position, options, s.snapshot, s.flow.runtime, runtime, s.flow.generator)
			if saveErr := s.saveDispatchCheckpoint(context.WithoutCancel(ctx), action); saveErr != nil {
				return false, saveErr
			}
			if !completed {
				if ctx.Err() != nil {
					return false, ctx.Err()
				}
				return false, fmt.Errorf("复打批次未安全完成")
			}
			remainingIDs := map[string]bool{}
			for _, candidate := range remaining {
				remainingIDs[candidate.PlatformCandidateID] = true
			}
			s.reGreets = nil
			for _, item := range selected {
				if remainingIDs[item.candidate.PlatformCandidateID] {
					s.reGreets = append(s.reGreets, item)
				} else {
					s.handledReGreets[item.basis] = true
				}
			}
			s.reGreets = append(s.reGreets, tail...)
			s.scheduler.Completed(action)
			if err := s.saveDispatchCheckpoint(ctx, action); err != nil {
				return false, err
			}
		case actiondispatch.CandidateInfo:
			s.lastBatch = action
			if err := s.enterMessages(ctx); err != nil {
				return false, err
			}
			count := minInt(3, len(s.infoQueue))
			selected := append([]string{}, s.infoQueue[:count]...)
			options := s.options
			options.candidateInfoBatchIDs = selected
			options.preparedReplyTarget = &s.flow.target
			remaining := []string{}
			options.candidateInfoRemaining = &remaining
			options.actionNow = s.now
			s.runner.performCandidateInfoChecks(ctx, s.position, s.runtime, options)
			pending := map[string]bool{}
			for _, id := range remaining {
				pending[id] = true
			}
			queue := []string{}
			for _, id := range selected {
				if pending[id] {
					queue = append(queue, id)
				} else {
					s.handledInfo[id] = true
				}
			}
			s.infoQueue = append(queue, s.infoQueue[count:]...)
			s.scheduler.Completed(action)
			if err := s.saveDispatchCheckpoint(context.WithoutCancel(ctx), action); err != nil {
				return false, err
			}
			if ctx.Err() != nil {
				return false, ctx.Err()
			}
		case actiondispatch.Greeting:
			if err := s.returnRecommendation(ctx); err != nil {
				return false, err
			}
			rescan := s.needsRescan
			s.needsRescan = false
			return rescan, nil
		case actiondispatch.Done:
			s.lastBatch = action
			return false, s.saveDispatchCheckpoint(ctx, action)
		}
		if singleBatch && (action == actiondispatch.Reply || action == actiondispatch.ReGreet || action == actiondispatch.CandidateInfo) {
			return false, nil
		}
	}
}

// scanBoundary 只由扫描协程在安全结束后调用，三种页面动作不会同时运行。
func (s *actionSession) scanBoundary(ctx context.Context) (bool, error) {
	s.scheduler.Completed(actiondispatch.Greeting)
	rescan, err := s.service(ctx, true, false)
	s.pendingScanRescan = s.pendingScanRescan || rescan
	return s.pendingScanRescan, err
}

// runCooperativePosition 从现有单岗位入口执行交替调度，完成后不挂着等待未来消息。
func (r *Runner) runCooperativePosition(ctx context.Context, position localdb.Position, options StartOptions, snapshot PositionRuntimeSnapshot) {
	session, err := r.newActionSession(ctx, position, options, snapshot)
	if err == nil {
		greeting := hasTaskType(parseTaskTypes(options.TaskType), "greeting")
		if _, err = session.service(ctx, greeting, true); err == nil && greeting {
			r.initRestState(position.ID, options)
			options.scanBoundary = session.scanBoundary
			options.requestScanRescan = func() { session.pendingScanRescan = true }
			options.acknowledgeRescan = func() { session.pendingScanRescan = false }
			_, err = r.scanOnce(ctx, position, snapshot.PlatformConfig, options)
		}
		if err == nil {
			previous := ""
			for {
				beforeChecked := session.replyStats.Checked
				beforeSent := session.replyStats.Replied + session.replyStats.AcceptedResume + session.reGreetStats.sent
				beforeReGreet := session.reGreetStats.sent + session.reGreetStats.skipped + session.reGreetStats.failed + session.reGreetStats.unknown
				_, err = session.service(ctx, false, true)
				if err != nil {
					break
				}
				processed := session.replyStats.Checked - beforeChecked + session.reGreetStats.sent + session.reGreetStats.skipped + session.reGreetStats.failed + session.reGreetStats.unknown - beforeReGreet
				if processed == 0 {
					break
				}
				sent := session.replyStats.Replied + session.replyStats.AcceptedResume + session.reGreetStats.sent - beforeSent
				if sent == 0 && previous == session.lastScanSignature {
					break
				}
				previous = session.lastScanSignature
			}
		}
		if session.flow != nil {
			session.flow.flushResumeTracking()
		}
	}
	if err != nil {
		if errors.Is(err, context.Canceled) || r.isUserStopped(position.ID) {
			r.updateProgress(position.ID, Progress{Stage: "stopped", Message: "单岗位任务已停止"})
			_, _ = r.db.UpdatePositionStatus(position.ID, "stopped")
			if session != nil && hasTaskType(parseTaskTypes(options.TaskType), "auto_reply") {
				r.notifyCloudAutoReplyStatus(position.ID, options, "stopped", session.replyStats)
			} else if session != nil {
				r.notifyCloudReGreetStatus(position.ID, options, "stopped", session.reGreetStats)
			} else {
				r.notifyCloudAutoReplyStatus(position.ID, options, "stopped", platformcore.ReplyStats{})
			}
			return
		}
		r.failStart(position.ID, "单岗位动作调度停止："+err.Error(), options)
		return
	}
	r.updateProgress(position.ID, Progress{Stage: "completed", Message: "单岗位当前工作已完成"})
	_, _ = r.db.UpdatePositionStatus(position.ID, "completed")
	if hasTaskType(parseTaskTypes(options.TaskType), "auto_reply") {
		r.notifyCloudAutoReplyStatus(position.ID, options, "completed", session.replyStats)
	} else {
		r.notifyCloudReGreetStatus(position.ID, options, "completed", session.reGreetStats)
	}
}
