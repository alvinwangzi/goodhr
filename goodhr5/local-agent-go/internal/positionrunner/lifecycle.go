// Package positionrunner 文件作用：按职责承载本地岗位运行运行流程的拆分实现。
package positionrunner

import (
	"context"
	"errors"
	"fmt"
	"goodhr5/local-agent-go/internal/cloudapi"
	"goodhr5/local-agent-go/internal/localdb"
	"goodhr5/local-agent-go/internal/platformcore"
	"goodhr5/local-agent-go/internal/platforms"
	"goodhr5/local-agent-go/internal/power"
	"strings"
	"time"
)

// Start 启动本地岗位运行运行器。
// ctx 为请求上下文，positionID 为岗位运行 ID，options 为启动参数。
func (r *Runner) Start(ctx context.Context, positionID string, options StartOptions) (map[string]any, error) {
	positionID = strings.TrimSpace(positionID)
	if positionID == "" {
		return nil, fmt.Errorf("岗位运行 ID 不能为空")
	}
	options.Token = strings.TrimSpace(options.Token)
	if options.Token == "" {
		return nil, fmt.Errorf("请先登录后再校验会员")
	}
	taskType, err := normalizeTaskType(options.TaskType)
	if err != nil {
		return nil, err
	}
	// 保留原始多选值供 runPosition 解析，只用 normalized 做启动阶段的即时判断。
	_ = taskType
	taskTypes := parseTaskTypes(options.TaskType)
	onlyAutoReply := hasTaskType(taskTypes, "auto_reply") && !hasTaskType(taskTypes, "greeting") && !hasTaskType(taskTypes, "re_greet")
	onlyReGreet := hasTaskType(taskTypes, "re_greet") && !hasTaskType(taskTypes, "greeting") && !hasTaskType(taskTypes, "auto_reply")
	client := cloudapi.New(options.CloudAPIBase)
	cloudPosition, err := client.FetchPosition(ctx, options.Token, positionID)
	if err != nil {
		return nil, err
	}
	position, err := r.db.UpsertPositionSnapshot(localPositionSnapshotFromCloud(cloudPosition))
	if err != nil {
		return nil, err
	}
	if onlyAutoReply {
		// 自动回复在取得任何浏览器动作前检查平台能力与本地内嵌配置。
		platformRuntime, err := platforms.RuntimeFor(position.PlatformID)
		if err != nil {
			return nil, err
		}
		runtime, ok := platformRuntime.(platformcore.AutoReplyRuntime)
		if !ok {
			return nil, fmt.Errorf("当前平台暂不支持 AI 自动回复")
		}
		if err := runtime.AutoReplyAvailable(); err != nil {
			return nil, fmt.Errorf("AI 自动回复暂未开放：%w", err)
		}
	}
	if onlyReGreet {
		// 复打招呼同样需要平台支持会话操作（Boss 直聘复用消息会话能力）。
		platformRuntime, err := platforms.RuntimeFor(position.PlatformID)
		if err != nil {
			return nil, err
		}
		runtime, ok := platformRuntime.(platformcore.AutoReplyRuntime)
		if !ok {
			return nil, fmt.Errorf("当前平台暂不支持复打招呼")
		}
		if err := runtime.AutoReplyAvailable(); err != nil {
			return nil, fmt.Errorf("复打招呼暂未开放：%w", err)
		}
	}
	r.positionLog(positionID, "info", "岗位运行启动：正在准备本地运行环境")
	r.positionLog(positionID, "info", fmt.Sprintf("岗位运行启动：岗位运行配置读取完成，平台=%s，岗位=%s，模式=%s，轮次=%d", position.PlatformID, positionPositionName(position), position.Mode, scanRounds(options)))
	runCtx, cancel := context.WithCancel(context.Background())
	if !r.setRunning(positionID, cancel, options) {
		cancel()
		return nil, fmt.Errorf("本地程序有任务正在运行，一次只能跑一个岗位。请先停止当前任务再开始")
	}
	r.positionLog(positionID, "info", "岗位运行启动：本地运行锁已创建")
	if err := r.ensurePowerProtection(positionID); err != nil {
		r.positionLog(positionID, "warning", "岗位运行启动：防睡眠保护启动失败，错误="+err.Error())
	} else {
		r.positionLog(positionID, "info", "岗位运行启动：防睡眠保护已开启")
	}
	totalRounds := scanRounds(options)
	r.updateProgress(positionID, Progress{Stage: "starting", Message: "岗位运行准备启动", TotalRounds: totalRounds})
	// 保存通知邮箱到运行状态
	r.mu.Lock()
	if state, ok := r.running[positionID]; ok {
		state.emailForNotify = options.EmailForNotify
	}
	r.mu.Unlock()
	snapshot, err := r.buildPositionRuntimeSnapshot(ctx, client, position, options, totalRounds)
	if err != nil {
		cancel()
		r.failStart(positionID, err.Error(), options)
		r.clear(positionID)
		return map[string]any{"position": positionStatusAfterStartFailure(r.db, positionID, position), "running": false}, err
	}
	position = snapshot.Position
	options = snapshot.Options
	options.EnableSound = position.EnableSound
	updated, err := r.db.UpdatePositionStatus(positionID, "running")
	if err != nil {
		r.clear(positionID)
		cancel()
		return nil, err
	}
	syncCtx, syncCancel := context.WithTimeout(context.Background(), 5*time.Second)
	if onlyAutoReply || onlyReGreet {
		// 纯自动回复 / 纯复打招呼必须得到云端明确许可和非空执行任务记录 ID，失败不沿用只记警告继续运行的行为。
		taskTypeForSync := "auto_reply"
		taskLabel := "自动回复"
		if onlyReGreet {
			taskTypeForSync = "re_greet"
			taskLabel = "复打招呼"
		}
		syncResult, syncErr := client.SyncTaskStatus(syncCtx, options.Token, positionID, cloudapi.TaskStatusRequest{Status: "running", TaskType: taskTypeForSync, MachineID: options.MachineID})
		if syncErr != nil {
			syncCancel()
			r.positionLog(positionID, "error", "岗位运行启动：云端未允许"+taskLabel+"，错误="+syncErr.Error())
			_, _ = r.db.UpdatePositionStatus(positionID, "stopped")
			r.clear(positionID)
			return nil, fmt.Errorf("云端未允许%s，任务未开始：%w", taskLabel, syncErr)
		}
		snapshot.Options.CloudRunID = syncResult.RunID
		options.CloudRunID = syncResult.RunID
		r.positionLog(positionID, "info", "岗位运行启动：云端已许可"+taskLabel+"，本次执行任务记录 ID="+syncResult.RunID)
	} else if syncResult, syncErr := client.SyncPositionStatus(syncCtx, options.Token, positionID, "running", options.MachineID); syncErr != nil {
		r.positionLog(positionID, "warning", "岗位运行启动：云端运行状态同步失败，错误="+syncErr.Error())
	} else if strings.TrimSpace(syncResult.RunID) != "" {
		// 云端本次运行对应的执行任务记录 ID 必须写回 snapshot.Options：
		// runPosition 会用 snapshot.Options 覆盖启动参数，漏写会导致候选人结果丢失归组 ID。
		snapshot.Options.CloudRunID = syncResult.RunID
		options.CloudRunID = syncResult.RunID
		r.positionLog(positionID, "info", "岗位运行启动：本次执行任务记录 ID="+syncResult.RunID)
	}
	syncCancel()
	r.positionLog(positionID, "info", "岗位运行启动：已进入后台运行")
	go r.runPosition(runCtx, position, options, snapshot)
	return map[string]any{"position": updated, "running": true}, nil
}

// runPosition 在后台执行本地岗位运行主流程。
// ctx 为运行上下文，position 为岗位运行记录，options 为启动参数。
func (r *Runner) runPosition(ctx context.Context, position localdb.Position, options StartOptions, snapshot PositionRuntimeSnapshot) {
	positionID := position.ID
	defer r.clear(positionID)
	defer r.closePendingCandidateDetail(positionID)
	totalRounds := scanRounds(options)
	position = snapshot.Position
	options = snapshot.Options
	options.EnableSound = position.EnableSound
	r.updateRunOptions(positionID, options)
	taskTypes := parseTaskTypes(options.TaskType)
	if hasTaskType(taskTypes, "auto_reply") && !hasTaskType(taskTypes, "greeting") && !hasTaskType(taskTypes, "re_greet") {
		// 纯自动回复独立编排：不进入候选人扫描、休息和收尾求简历流程。
		r.updateProgress(positionID, Progress{Stage: "running", Message: "自动回复已开始执行", TotalRounds: scanRounds(options)})
		r.runAutoReplyTask(ctx, position, options)
		return
	}
	if hasTaskType(taskTypes, "re_greet") && !hasTaskType(taskTypes, "greeting") && !hasTaskType(taskTypes, "auto_reply") {
		// 纯复打招呼独立编排：从云端拉复打名单，逐个发送后上报结果。
		r.updateProgress(positionID, Progress{Stage: "running", Message: "复打招呼已开始执行", TotalRounds: scanRounds(options)})
		r.runReGreetTask(ctx, position, options, snapshot)
		return
	}
	r.initRestState(positionID, options)
	r.updateProgress(positionID, Progress{Stage: "running", Message: "岗位运行已开始执行", TotalRounds: totalRounds})
	r.positionLog(positionID, "info", "岗位运行启动：本地岗位运行运行器已启动，准备进入扫描流程")
	scanResult, err := r.scanOnce(ctx, position, snapshot.PlatformConfig, options)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			if reason := r.cancelReason(positionID); reason != "" {
				r.updateProgress(positionID, Progress{Stage: "failed", Message: reason, TotalRounds: totalRounds})
				r.failStart(positionID, reason, options)
				return
			}
			r.updateProgress(positionID, Progress{Stage: "stopped", Message: "岗位运行已停止", TotalRounds: totalRounds})
			_, _ = r.db.UpdatePositionStatus(positionID, "stopped")
			r.positionLog(positionID, "info", "岗位运行停止：收到停止信号，正在同步云端停止状态")
			r.notifyCloudPositionStopped(positionID, options)
			// 用户停止岗位后浏览器保持打开，后台继续检查待索要名单中候选人是否已回复。
			r.asyncCheckResumeRequests(position, snapshot.PlatformConfig, options)
			return
		}
		if isBrowserClosedPositionError(err) {
			r.updateProgress(positionID, Progress{Stage: "stopped", Message: "浏览器已关闭，岗位运行已自动结束", TotalRounds: totalRounds})
			_, _ = r.db.UpdatePositionStatus(positionID, "stopped")
			message := "浏览器已关闭，岗位运行已自动结束：" + err.Error()
			r.positionLog(positionID, "error", "岗位运行失败：环节=浏览器运行，错误="+message)
			r.sendPositionFailNotification(context.Background(), positionID, message, options)
			return
		}
		var authErr cloudapi.AuthExpiredError
		if errors.As(err, &authErr) {
			r.notifyCloudPositionStopped(positionID, options)
			return
		}
		r.failStart(positionID, "本地岗位运行扫描失败："+err.Error(), options)
		return
	}
	if r.isUserStopped(positionID) {
		r.positionLog(positionID, "info", "岗位运行停止：岗位运行已被用户停止，忽略扫描完成结果")
		// 用户停止岗位后浏览器保持打开，后台继续检查待索要名单中候选人是否已回复。
		r.asyncCheckResumeRequests(position, snapshot.PlatformConfig, options)
		return
	}
	// 多选场景：打招呼完成后，如果同时选择了自动回复，则接着执行自动回复。
	if hasTaskType(taskTypes, "auto_reply") {
		r.positionLog(positionID, "info", "岗位运行：打招呼已完成，开始执行自动回复")
		r.updateProgress(positionID, Progress{Stage: "running", Message: "打招呼已完成，自动回复已开始执行", TotalRounds: scanRounds(options)})
		r.runAutoReplyTask(ctx, position, options)
		return
	}
	r.updateProgress(positionID, Progress{Stage: "completed", Message: "岗位运行已完成", Round: totalRounds, TotalRounds: totalRounds})
	_, _ = r.db.UpdatePositionStatus(positionID, "completed")
	r.positionLog(positionID, "info", fmt.Sprintf(
		"岗位运行完成：本次运行结束，扫描=%d，打招呼=%d，跳过=%d，失败=%d",
		intFromMap(scanResult, "candidates_count"),
		intFromMap(scanResult, "greeted_count"),
		intFromMap(scanResult, "skipped_count"),
		intFromMap(scanResult, "failed_count"),
	))
	r.notifyCloudPositionCompleted(positionID, options)
	// 岗位完成后后台检查待索要名单中候选人是否已回复，已回复则自动索要简历。
	r.asyncCheckResumeRequests(position, snapshot.PlatformConfig, options)
}

// Stop 停止本地岗位运行运行器。
// positionID 为岗位运行 ID。
func (r *Runner) Stop(positionID string) (map[string]any, error) {
	positionID = strings.TrimSpace(positionID)
	if positionID == "" {
		return nil, fmt.Errorf("岗位运行 ID 不能为空")
	}
	r.positionLog(positionID, "info", "岗位运行停止：收到用户停止请求")
	r.markUserStopped(positionID)
	position, err := r.db.UpdatePositionStatus(positionID, "stopped")
	if err != nil {
		return nil, err
	}
	if r.autoReplyRunning(positionID) {
		// 自动回复收到停止信号立即取消尚未发送的 AI 请求；已开始发送只做必要结果确认。
		r.markUserStoppedAndCancel(positionID)
		r.updateProgress(positionID, Progress{Stage: "stopped", Message: "自动回复已停止"})
		r.positionLog(positionID, "info", "岗位运行停止：自动回复已立即停止，不再生成新回复")
	} else if r.hasRunningLock(positionID) {
		r.updateProgress(positionID, Progress{Stage: "running", Message: "正在处理当前候选人，处理完会停止"})
		r.positionLog(positionID, "info", "岗位运行停止：正在等待当前候选人处理完成")
		if !r.waitUntilStopped(positionID, stopGracefulTimeout) {
			r.positionLog(positionID, "warning", fmt.Sprintf("岗位运行停止：等待超时，已强制停止，超过=%s", stopGracefulTimeout.Round(time.Second)))
			r.markUserStoppedAndCancel(positionID)
			r.updateProgress(positionID, Progress{Stage: "stopped", Message: "停止等待超时，已强制停止"})
			_, _ = r.db.UpdatePositionStatus(positionID, "stopped")
		}
	}
	r.positionLog(positionID, "info", "岗位运行停止：当前候选人处理完成，岗位运行停止，浏览器保持打开")
	if latest, getErr := r.db.GetPosition(positionID); getErr == nil {
		position = latest
	}
	return map[string]any{"position": localPositionStatusMap(position), "running": r.hasRunningLock(positionID)}, nil
}

// StopAll 停止所有正在运行的本地岗位运行。
// reason 为停止原因，返回停止的岗位运行数量。
func (r *Runner) StopAll(reason string) int {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "岗位运行已停止"
	}
	r.mu.Lock()
	ids := make([]string, 0, len(r.running))
	for positionID, state := range r.running {
		ids = append(ids, positionID)
		if state != nil && state.cancel != nil {
			state.cancel()
		}
	}
	r.mu.Unlock()
	for _, positionID := range ids {
		r.updateProgress(positionID, Progress{Stage: "stopped", Message: reason, TotalRounds: defaultScanRounds})
		_, _ = r.db.UpdatePositionStatus(positionID, "stopped")
		r.positionLog(positionID, "warning", reason)
	}
	return len(ids)
}

// Status 返回本地岗位运行运行状态。
// positionID 为岗位运行 ID。
func (r *Runner) Status(positionID string) (map[string]any, error) {
	positionID = strings.TrimSpace(positionID)
	if positionID == "" {
		return nil, fmt.Errorf("岗位运行 ID 不能为空")
	}
	position, err := r.db.GetPosition(positionID)
	if err != nil {
		if isLocalPositionMissing(err) {
			return map[string]any{
				"position": map[string]any{
					"id":     positionID,
					"status": "pending",
				},
				"running": false,
				"progress": Progress{
					Stage:       "pending",
					Message:     "本地岗位运行尚未启动",
					TotalRounds: defaultScanRounds,
					UpdatedAt:   time.Now().Format(time.RFC3339),
				},
				"logs": []localdb.Log{},
			}, nil
		}
		return nil, err
	}
	running := r.IsRunning(positionID)
	progress := r.Progress(positionID, position)
	analysis := r.analysisSnapshot(positionID)
	logs, _ := r.db.ListPositionLogs(positionID, 20)
	positionMap := localPositionStatusMap(position)
	positionMap["current_run_greeted_count"] = r.currentRunGreeted(positionID)
	if stats, ok := r.currentReplyStats(positionID); ok {
		positionMap["task_type"] = "auto_reply"
		positionMap["reply_stats"] = stats
	}
	return map[string]any{
		"position":      positionMap,
		"running":       running,
		"progress":      progress,
		"logs":          logs,
		"status":        position.Status,
		"current_step":  progress.Message,
		"scanned_count": position.ScannedCount,
		"greeted_count": position.GreetedCount,
		"skipped_count": position.SkippedCount,
		"failed_count":  position.FailedCount,
		"analysis":      analysis,
	}, nil
}

// isLocalPositionMissing 判断错误是否表示本地岗位运行尚未创建。
// err 为数据库返回的错误。
func isLocalPositionMissing(err error) bool {
	return err != nil && strings.Contains(err.Error(), "本地岗位运行不存在")
}

// Progress 返回岗位运行当前进度。
// positionID 为岗位运行 ID，position 为岗位运行记录。
func (r *Runner) Progress(positionID string, position localdb.Position) Progress {
	r.mu.Lock()
	state := r.running[strings.TrimSpace(positionID)]
	r.mu.Unlock()
	if state != nil {
		return state.progress
	}
	stage := position.Status
	if stage == "" {
		stage = "unknown"
	}
	return Progress{
		Stage:       stage,
		Message:     statusMessage(stage),
		TotalRounds: defaultScanRounds,
		UpdatedAt:   position.UpdatedAt,
	}
}

// IsRunning 判断岗位运行是否正在运行。
// positionID 为岗位运行 ID。
func (r *Runner) IsRunning(positionID string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	positionID = strings.TrimSpace(positionID)
	if r.userStopped[positionID] {
		return false
	}
	state := r.running[positionID]
	if state != nil && isTerminalStage(state.progress.Stage) {
		return false
	}
	ok := state != nil
	return ok
}

// hasRunningLock 判断岗位运行运行锁是否还存在，不受用户停止标记影响。
func (r *Runner) hasRunningLock(positionID string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, ok := r.running[strings.TrimSpace(positionID)]
	return ok
}

// waitUntilStopped 等待岗位运行运行锁释放，超时返回 false。
func (r *Runner) waitUntilStopped(positionID string, timeout time.Duration) bool {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(stopPollInterval)
	defer ticker.Stop()
	for {
		if !r.hasRunningLock(positionID) {
			return true
		}
		select {
		case <-deadline.C:
			return !r.hasRunningLock(positionID)
		case <-ticker.C:
		}
	}
}

// failStart 记录启动失败日志并清理运行锁，自动播放失败提示音和发送邮件通知。
// positionID 为岗位运行 ID，msg 为失败原因，options 为本次岗位运行启动参数。
func (r *Runner) failStart(positionID string, msg string, options StartOptions) {
	r.positionLog(positionID, "error", "岗位运行失败：环节=岗位运行运行，错误="+msg)
	_, _ = r.db.UpdatePositionStatus(positionID, "failed")
	// 自动播放失败提示音（如果岗位运行开启了提示音）
	if position, err := r.db.GetPosition(positionID); err == nil && position.EnableSound {
		r.playSound("failed.wav", positionID)
	}
	r.sendPositionFailNotification(context.Background(), positionID, msg, options)
}

// positionStatusAfterStartFailure 返回启动失败后的最新岗位运行状态。
// db 为本地数据库，positionID 为岗位运行 ID，fallback 为读取失败时的兜底岗位运行。
func positionStatusAfterStartFailure(db *localdb.DB, positionID string, fallback localdb.Position) localdb.Position {
	if db != nil {
		if position, err := db.GetPosition(positionID); err == nil {
			return position
		}
	}
	fallback.Status = "failed"
	return fallback
}

// isBrowserClosedPositionError 判断错误是否来自用户关闭浏览器。
// err 为岗位运行执行中的错误。
func isBrowserClosedPositionError(err error) bool {
	if err == nil {
		return false
	}
	text := strings.ToLower(err.Error())
	keywords := []string{
		"浏览器已关闭",
		"浏览器未启动",
		"target page, context or browser has been closed",
		"browser has been closed",
		"context closed",
		"target closed",
	}
	for _, keyword := range keywords {
		if strings.Contains(text, strings.ToLower(keyword)) {
			return true
		}
	}
	return false
}

// isFatalCandidateDetailError 判断错误是否表示候选人详情容器在规定时间内未出现。
// err 为详情读取过程中返回的错误。
func isFatalCandidateDetailError(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "候选人详情没找到")
}

// ensurePowerProtection 确保运行岗位运行期间系统不会自动睡眠。
// positionID 为当前岗位运行 ID，失败时返回错误但不阻断岗位运行。
func (r *Runner) ensurePowerProtection(positionID string) error {
	r.mu.Lock()
	if r.powerGuard != nil {
		r.mu.Unlock()
		return nil
	}
	r.mu.Unlock()
	guard, err := power.PreventSleep("HRPlus 岗位运行运行中")
	if err != nil {
		return err
	}
	r.mu.Lock()
	if r.powerGuard != nil {
		r.mu.Unlock()
		_ = guard.Stop()
		return nil
	}
	r.powerGuard = guard
	if r.sleepCancel == nil {
		ctx, cancel := context.WithCancel(context.Background())
		r.sleepCancel = cancel
		go r.monitorSleepResume(ctx)
	}
	r.mu.Unlock()
	return nil
}

// releasePowerProtectionIfIdle 在没有运行岗位运行时释放防睡眠保护。
func (r *Runner) releasePowerProtectionIfIdle() {
	r.mu.Lock()
	if len(r.running) > 0 || r.browserLease != nil {
		r.mu.Unlock()
		return
	}
	guard := r.powerGuard
	cancel := r.sleepCancel
	r.powerGuard = nil
	r.sleepCancel = nil
	r.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if guard != nil {
		_ = guard.Stop()
	}
}

// monitorSleepResume 检测电脑是否发生过睡眠/休眠恢复。
// ctx 结束时检测停止；发现时间断层后会取消正在运行的岗位运行并让岗位运行失败邮件接管通知。
func (r *Runner) monitorSleepResume(ctx context.Context) {
	ticker := time.NewTicker(sleepMonitorInterval)
	defer ticker.Stop()
	last := time.Now()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			gap := now.Sub(last)
			last = now
			if gap > sleepResumeThreshold {
				r.cancelRunningPositionsAfterSleep(gap)
			}
		}
	}
}

// cancelRunningPositionsAfterSleep 在检测到疑似睡眠恢复后取消所有运行岗位运行。
// gap 为检测到的时间断层，用于日志和邮件说明。
func (r *Runner) cancelRunningPositionsAfterSleep(gap time.Duration) {
	reason := fmt.Sprintf("检测到电脑可能已休眠或息屏，岗位运行已停止；心跳中断=%s", gap.Round(time.Second))
	r.mu.Lock()
	items := make(map[string]context.CancelFunc, len(r.running))
	for positionID, state := range r.running {
		if state == nil {
			continue
		}
		state.cancelReason = reason
		items[positionID] = state.cancel
	}
	r.mu.Unlock()
	for positionID, cancel := range items {
		r.positionLog(positionID, "error", "岗位运行失败：环节=电脑休眠检测，错误="+reason)
		if cancel != nil {
			cancel()
		}
	}
}

// setRunning 标记岗位运行正在运行。
// positionID 为岗位运行 ID，cancel 为停止回调。
func (r *Runner) setRunning(positionID string, cancel context.CancelFunc, options StartOptions) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.running) > 0 || r.browserLease != nil {
		return false
	}
	delete(r.userStopped, positionID)
	r.browserLease = &browserLease{refs: 1}
	r.running[positionID] = &runState{lease: r.browserLease, done: make(chan struct{}), cancel: cancel, options: options, progress: Progress{Stage: "starting", Message: "岗位运行准备启动", TotalRounds: defaultScanRounds, UpdatedAt: time.Now().UTC().Format(time.RFC3339Nano)}}
	return true
}

// updateRunOptions 更新运行岗位运行使用的启动参数。
// positionID 为岗位运行 ID，options 为最新启动参数。
func (r *Runner) updateRunOptions(positionID string, options StartOptions) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if state := r.running[strings.TrimSpace(positionID)]; state != nil {
		state.options = options
	}
}

// cancelReason 返回岗位运行被系统取消的原因。
// positionID 为岗位运行 ID，返回空字符串表示不是系统原因取消。
func (r *Runner) cancelReason(positionID string) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if state := r.running[strings.TrimSpace(positionID)]; state != nil {
		return strings.TrimSpace(state.cancelReason)
	}
	return ""
}

// updateProgress 更新岗位运行运行进度。
// positionID 为岗位运行 ID，progress 为新进度。
func (r *Runner) updateProgress(positionID string, progress Progress) {
	r.mu.Lock()
	defer r.mu.Unlock()
	state := r.running[positionID]
	if state == nil {
		return
	}
	if progress.TotalRounds <= 0 {
		progress.TotalRounds = defaultScanRounds
	}
	if progress.UpdatedAt == "" {
		progress.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	}
	state.progress = progress
}

// updateAnalysis 更新岗位运行给控制台置顶小窗展示的结构化分析结果。
func (r *Runner) updateAnalysis(positionID string, analysis positionAnalysisStatus) {
	positionID = strings.TrimSpace(positionID)
	if positionID == "" {
		return
	}
	if analysis.UpdatedAt == "" {
		analysis.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	}
	analysis.Keywords = append([]string{}, analysis.Keywords...)
	analysis.MatchedKeywords = append([]string{}, analysis.MatchedKeywords...)
	analysis.ExcludeKeywords = append([]string{}, analysis.ExcludeKeywords...)
	analysis.MatchedExcludes = append([]string{}, analysis.MatchedExcludes...)
	r.mu.Lock()
	defer r.mu.Unlock()
	if state := r.running[positionID]; state != nil {
		state.analysis = &analysis
	}
}

// analysisSnapshot 返回当前岗位运行最近一次结构化分析结果的安全副本。
func (r *Runner) analysisSnapshot(positionID string) *positionAnalysisStatus {
	r.mu.Lock()
	defer r.mu.Unlock()
	state := r.running[strings.TrimSpace(positionID)]
	if state == nil || state.analysis == nil {
		return nil
	}
	analysis := *state.analysis
	analysis.Keywords = append([]string{}, state.analysis.Keywords...)
	analysis.MatchedKeywords = append([]string{}, state.analysis.MatchedKeywords...)
	analysis.ExcludeKeywords = append([]string{}, state.analysis.ExcludeKeywords...)
	analysis.MatchedExcludes = append([]string{}, state.analysis.MatchedExcludes...)
	return &analysis
}

// incrementRunGreeted 增加当前岗位运行本次运行已打招呼数量。
// positionID 为岗位运行 ID，count 为本次新增打招呼数量。
func (r *Runner) incrementRunGreeted(positionID string, count int) {
	if count <= 0 {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if state := r.running[strings.TrimSpace(positionID)]; state != nil {
		state.runGreeted += count
	}
}

// currentRunGreeted 返回当前岗位运行本次运行已打招呼数量。
// positionID 为岗位运行 ID。
func (r *Runner) currentRunGreeted(positionID string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	if state := r.running[strings.TrimSpace(positionID)]; state != nil {
		return state.runGreeted
	}
	return 0
}

// autoReplyRunning 判断岗位当前是否正在运行自动回复任务。
// positionID 为岗位运行 ID。
func (r *Runner) autoReplyRunning(positionID string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	state := r.running[strings.TrimSpace(positionID)]
	return state != nil && hasTaskType(parseTaskTypes(state.options.TaskType), "auto_reply")
}

// updateReplyStats 更新自动回复统计，供状态接口展示。
// positionID 为岗位运行 ID，stats 为最新统计。
func (r *Runner) updateReplyStats(positionID string, stats platformcore.ReplyStats) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if state := r.running[strings.TrimSpace(positionID)]; state != nil {
		value := stats
		state.replyStats = &value
	}
}

// currentReplyStats 返回当前自动回复统计；无运行任务或非自动回复时返回 false。
// positionID 为岗位运行 ID。
func (r *Runner) currentReplyStats(positionID string) (platformcore.ReplyStats, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	state := r.running[strings.TrimSpace(positionID)]
	if state == nil || state.replyStats == nil {
		return platformcore.ReplyStats{}, false
	}
	return *state.replyStats, true
}

// cancel 取消正在运行的岗位运行。
// positionID 为岗位运行 ID。
func (r *Runner) cancel(positionID string) {
	r.mu.Lock()
	state := r.running[positionID]
	r.mu.Unlock()
	if state != nil && state.cancel != nil {
		state.cancel()
	}
}

// markUserStopped 标记岗位运行收到停止请求，但不打断当前候选人处理。
// positionID 为岗位运行 ID。
func (r *Runner) markUserStopped(positionID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.userStopped[positionID] = true
}

// markUserStoppedAndCancel 标记用户主动停止并取消运行岗位运行。
// positionID 为岗位运行 ID，标记会保留到岗位运行协程清理，供收尾动作判断是否应跳过页面操作。
func (r *Runner) markUserStoppedAndCancel(positionID string) {
	r.mu.Lock()
	state := r.running[positionID]
	r.userStopped[positionID] = true
	r.mu.Unlock()
	if state != nil && state.cancel != nil {
		state.cancel()
	}
	r.releasePowerProtectionIfIdle()
}

// isUserStopped 判断岗位运行是否由用户主动停止。
// positionID 为岗位运行 ID，返回 true 时后续收尾逻辑不应再操作浏览器页面。
func (r *Runner) isUserStopped(positionID string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.userStopped[strings.TrimSpace(positionID)]
}

// clear 清理岗位运行运行锁。
// positionID 为岗位运行 ID。
func (r *Runner) clear(positionID string) {
	r.mu.Lock()
	if state := r.running[positionID]; state != nil {
		if state.cancel != nil { state.cancel() }
		if state.done != nil { close(state.done) }
		r.releaseBrowserLocked(state.lease)
		delete(r.running, positionID)
		delete(r.userStopped, positionID)
	}
	r.mu.Unlock()
	r.releasePowerProtectionIfIdle()
}

// releaseBrowserLocked 在持有状态锁时减少当前浏览器占用引用。
func (r *Runner) releaseBrowserLocked(lease *browserLease) {
	if lease == nil || r.browserLease != lease { return }
	lease.refs--
	if lease.refs == 0 { r.browserLease = nil }
}

// reserveResumeBrowser 在启动收尾协程前取得占用；继承时等待主任务全部页面清理结束。
func (r *Runner) reserveResumeBrowser(positionID string) (func(), <-chan struct{}, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	ready := make(chan struct{})
	lease := r.browserLease
	if state := r.running[positionID]; state != nil && state.lease == lease && lease != nil {
		lease.refs++
		ready = state.done
	} else if lease == nil && len(r.running) == 0 {
		lease = &browserLease{refs: 1}
		r.browserLease = lease
		close(ready)
	} else {
		return nil, nil, false
	}
	return func() {
		r.mu.Lock()
		r.releaseBrowserLocked(lease)
		r.mu.Unlock()
		r.releasePowerProtectionIfIdle()
	}, ready, true
}
