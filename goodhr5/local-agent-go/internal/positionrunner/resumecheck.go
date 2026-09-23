// 本文件作用：岗位运行收尾阶段的"检查候选人回复并索要简历"编排——
// 读取待索要名单，调用平台回复检查能力（Boss 在消息页核对回复后点求简历），
// 按结果更新名单状态，并把索要结果补报到云端简历库。
package positionrunner

import (
	"context"
	"fmt"
	"strings"
	"time"

	"goodhr5/local-agent-go/internal/cloudapi"
	"goodhr5/local-agent-go/internal/localdb"
	"goodhr5/local-agent-go/internal/platformcore"
	"goodhr5/local-agent-go/internal/platforms"
)

const (
	// resumeCheckTimeout 是岗位收尾后回复检查的整体超时时间。
	resumeCheckTimeout = 3 * time.Minute
)

// enqueueResumeRequest 在打招呼成功后把候选人写入待索要名单。
// position 为岗位运行记录，candidate 为候选人字段集合，写入失败时返回错误。
func (r *Runner) enqueueResumeRequest(position localdb.Position, candidate map[string]any) error {
	name := strings.TrimSpace(stringFromMap(candidate, "candidate_name"))
	if name == "" {
		return fmt.Errorf("候选人姓名为空")
	}
	_, err := r.db.EnqueueResumeRequest(position.ID, position.PlatformID, name)
	return err
}

// asyncCheckResumeRequests 在后台协程执行回复检查，避免阻塞岗位停止或完成流程的返回。
// position 为岗位运行记录，platformConfig 为云端平台配置，options 为启动参数。
func (r *Runner) asyncCheckResumeRequests(position localdb.Position, platformConfig cloudapi.PlatformConfig, options StartOptions) {
	release, ready, ok := r.reserveResumeBrowser(position.ID)
	if !ok {
		r.positionLog(position.ID, "info", "回复检查：浏览器繁忙，名单保留待下次检查")
		return
	}
	go func() {
		defer release()
		<-ready
		defer func() {
			if recovered := recover(); recovered != nil {
				r.positionLog(position.ID, "warning", fmt.Sprintf("回复检查：执行过程中出现异常已忽略，细节=%v", recovered))
			}
		}()
		platformRuntime, err := platforms.RuntimeFor(position.PlatformID)
		if err != nil {
			r.positionLog(position.ID, "warning", "回复检查：解析平台能力失败，跳过索要，错误="+err.Error())
			return
		}
		r.checkResumeRequests(position, platformRuntime, platformConfig, options)
	}()
}

// checkResumeRequests 岗位收尾入口：自建整体超时上下文后执行回复检查。
// position 为岗位运行记录，platformRuntime 为平台能力，platformConfig 为云端平台配置，options 为启动参数。
func (r *Runner) checkResumeRequests(position localdb.Position, platformRuntime platformcore.Runtime, platformConfig cloudapi.PlatformConfig, options StartOptions) {
	ctx, cancel := context.WithTimeout(context.Background(), resumeCheckTimeout)
	defer cancel()
	r.performResumeChecks(ctx, position, platformRuntime, platformConfig, options)
}

// performResumeChecks 回复检查内核：读取待索要名单并逐人检查回复、执行索要、更新状态和云端补报。
// 检查耗时受传入上下文约束，岗位收尾与休息窗口两个入口共用。
// ctx 为检查上下文，position 为岗位运行记录，platformRuntime 为平台能力，platformConfig 为云端平台配置，options 为启动参数。
func (r *Runner) performResumeChecks(ctx context.Context, position localdb.Position, platformRuntime platformcore.Runtime, platformConfig cloudapi.PlatformConfig, options StartOptions) {
	positionID := position.ID
	items, err := r.db.ListResumeRequests(positionID, localdb.ResumeRequestStatusPending)
	if err != nil {
		r.positionLog(positionID, "warning", "回复检查：读取待索要名单失败，错误="+err.Error())
		return
	}
	if len(items) == 0 {
		return
	}
	checker, ok := platformRuntime.(platformcore.ResumeRequestChecker)
	if !ok {
		r.positionLog(positionID, "info", "回复检查：当前平台不支持消息页回复检查，跳过索要")
		return
	}
	r.positionLog(positionID, "info", fmt.Sprintf("回复检查：开始检查 %d 位候选人是否已回复并索要简历", len(items)))
	exec := platformExecutor{runner: r, positionID: positionID}
	names := make([]string, 0, len(items))
	for _, item := range items {
		names = append(names, item.CandidateName)
	}
	outcomes, err := checker.CheckResumeRequests(ctx, exec, platformConfig, names)
	if err != nil {
		// 检查整体失败时名单保持待检查，等下一次岗位收尾再查。
		r.positionLog(positionID, "warning", "回复检查：执行失败，名单保留待下次检查，错误="+err.Error())
		return
	}
	requested, pendingCount, failedCount := 0, 0, 0
	requestedNames := make([]string, 0, len(items))
	for _, item := range items {
		outcome, exist := outcomes[item.CandidateName]
		if !exist || outcome.Status == "pending" {
			pendingCount++
			continue
		}
		switch outcome.Status {
		case "requested":
			if err := r.db.MarkResumeRequested(item.ID); err != nil {
				r.positionLog(positionID, "warning", "回复检查：标记索要完成失败，候选人="+item.CandidateName+"，错误="+err.Error())
				failedCount++
				continue
			}
			requested++
			requestedNames = append(requestedNames, item.CandidateName)
		case "not_found":
			if err := r.db.MarkResumeFailed(item.ID, "会话列表中未找到该候选人的会话"); err != nil {
				r.positionLog(positionID, "warning", "回复检查：标记名单失败失败，候选人="+item.CandidateName+"，错误="+err.Error())
				continue
			}
			failedCount++
		default:
			reason := strings.TrimSpace(outcome.Reason)
			if reason == "" {
				reason = "回复检查未完成索要"
			}
			if err := r.db.MarkResumeFailed(item.ID, reason); err != nil {
				r.positionLog(positionID, "warning", "回复检查：标记名单失败失败，候选人="+item.CandidateName+"，错误="+err.Error())
				continue
			}
			failedCount++
		}
	}
	r.positionLog(positionID, "info", fmt.Sprintf(
		"回复检查：本轮检查=%d，已回复并完成索要=%d，未回复继续等待=%d，未完成=%d",
		len(items), requested, pendingCount, failedCount,
	))
	if requested > 0 {
		r.notifyCloudResumeRequested(ctx, position, options, requestedNames)
	}
}

// notifyCloudResumeRequested 把本轮新完成的索要结果补报到云端，供简历库时间线和执行任务名单展示。
// ctx 为检查上下文，position 为岗位运行记录，options 为启动参数，names 为本轮完成索要的候选人姓名。
func (r *Runner) notifyCloudResumeRequested(ctx context.Context, position localdb.Position, options StartOptions, names []string) {
	if len(names) == 0 {
		return
	}
	token := strings.TrimSpace(options.Token)
	if token == "" {
		r.positionLog(position.ID, "warning", "回复检查：缺少云端登录凭证，跳过索要结果补报")
		return
	}
	baseURL := strings.TrimSpace(options.CloudAPIBase)
	if baseURL == "" {
		baseURL = strings.TrimSpace(r.cloudAPIBase)
	}
	if baseURL == "" {
		baseURL = "https://goodhr5.58it.cn"
	}
	cloudCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	err := cloudapi.New(baseURL).NotifyResumeRequested(cloudCtx, token, position.ID, options.CloudRunID, names)
	if err != nil {
		r.positionLog(position.ID, "warning", "回复检查：索要结果补报云端失败，候选人="+strings.Join(names, "、")+"，错误="+err.Error())
		return
	}
	r.positionLog(position.ID, "info", "回复检查：索要结果已补报云端，候选人="+strings.Join(names, "、"))
}
