// 本文件执行 HRPlus 一个有限自动回复批次，完整保留身份复核、发送确认和回复后索要安全单元。
package positionrunner

import (
	"context"
	"errors"
	"fmt"
	"time"

	"goodhr5/local-agent-go/internal/actiondispatch"
	"goodhr5/local-agent-go/internal/platformcore"
)

// replyBatchResult 表示本批次真实处理结果，未领取的会话交还调度器。
type replyBatchResult struct {
	Stats     platformcore.ReplyStats
	Remaining []platformcore.ReplyConversation
}

// processReplyBatch 在三个会话或六十秒后让出执行机会，计时只在候选人安全边界判断。
// now 可由测试替换；stopped 在领取前检查；report 只接收实际结果，不将附件计为文字回复。
func processReplyBatch(ctx context.Context, flow *replyFlow, conversations []platformcore.ReplyConversation, now func() time.Time, stopped func() bool, report func(platformcore.ReplyConversation, string)) (replyBatchResult, error) {
	result := replyBatchResult{}
	started := now()
	for index, conversation := range conversations {
		if ctx.Err() != nil || stopped() {
			result.Remaining = conversations[index:]
			return result, context.Canceled
		}
		if actiondispatch.BatchLimit(started, now(), result.Stats.Checked) {
			result.Remaining = conversations[index:]
			return result, nil
		}
		current, err := flow.runtime.ReadReplyContext(ctx, flow.exec, flow.target, conversation)
		if err == nil && (conversation.ID == "" || current.Conversation.ID != conversation.ID) {
			err = platformcore.ErrReplyUnsafe
		}
		outcome := "failed"
		result.Stats.Checked++
		if err == nil {
			outcome, err = flow.process(ctx, current)
			// 回复与索要属于一个安全单元，批次时间超限也不在二者之间切页面。
			if outcome == "sent" {
				if _, resumeErr := flow.resumeAfterReplyIfNeeded(ctx, current.Conversation); resumeErr != nil {
					flow.flowLog("warning", "回复后索要尚未完成，已保存跟踪状态")
				}
			}
		} else if errors.Is(err, platformcore.ErrReplyUnsafe) {
			outcome = "skipped"
		}
		switch outcome {
		case "sent":
			result.Stats.Replied++
			flow.consecutiveFailures = 0
		case "accepted_resume":
			result.Stats.AcceptedResume++
			flow.consecutiveFailures = 0
		case "skipped":
			result.Stats.Skipped++
		case "unknown":
			result.Stats.Unknown++
		default:
			result.Stats.Failed++
			flow.consecutiveFailures++
		}
		if report != nil {
			report(conversation, outcome)
		}
		if errors.Is(err, errReplyStorage) || errors.Is(err, context.Canceled) || isBrowserClosedPositionError(err) {
			result.Remaining = conversations[index+1:]
			return result, err
		}
		if flow.consecutiveFailures >= 3 {
			result.Remaining = conversations[index+1:]
			return result, fmt.Errorf("自动回复连续处理失败，任务已停止")
		}
	}
	return result, nil
}
