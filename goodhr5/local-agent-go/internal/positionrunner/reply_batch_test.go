// 本文件验证 HRPlus 回复批次的三人上限、时间边界、错误身份与停止后不领取会话。
package positionrunner

import (
	"context"
	"fmt"
	"goodhr5/local-agent-go/internal/platformcore"
	"testing"
	"time"
)

// cancelConversationGapExecutor 模拟等待途中停止，不让测试真的等待或操作页面。
type cancelConversationGapExecutor struct {
	platformcore.Executor
	cancel context.CancelFunc
}

// Delay 验证传入的随机范围并触发停止。
func (e *cancelConversationGapExecutor) Delay(ctx context.Context, _ string, seconds float64) error {
	if seconds < 1 || seconds > 2 {
		return fmt.Errorf("候选人切换等待越界：%f", seconds)
	}
	e.cancel()
	return ctx.Err()
}

// TestReplyBatchStopDuringGap 验证等待时停止不会打开下一会话，未领取候选人完整交还。
func TestReplyBatchStopDuringGap(t *testing.T) {
	flow, fixture, conversations := batchReplyFixture(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	flow.exec = &cancelConversationGapExecutor{cancel: cancel}
	result, err := processReplyBatch(ctx, flow, conversations, time.Now, func() bool { return false }, nil)
	if err != context.Canceled || fixture.readCalls != 0 || fixture.sends != 0 || len(result.Remaining) != len(conversations) {
		t.Fatalf("等待时停止仍操作了下一候选人 %+v %v", result, err)
	}
}

// batchReplyFixture 创建四个独立会话，实际回复去重使用真实 SQLite。
func batchReplyFixture(t *testing.T) (*replyFlow, *replyFixture, []platformcore.ReplyConversation) {
	t.Helper()
	flow, fixture, template := newReplyFlowFixture(t)
	conversations := []platformcore.ReplyConversation{}
	for i := 0; i < 4; i++ {
		current := template
		current.Conversation.ID = fmt.Sprintf("candidate-%d", i)
		conversations = append(conversations, current.Conversation)
		fixture.reads = append(fixture.reads, current)
	}
	return flow, fixture, conversations
}

// TestReplyBatchCountBoundary 验证三个人后交还第四人，统计只累加实际成功回复。
func TestReplyBatchCountBoundary(t *testing.T) {
	flow, fixture, conversations := batchReplyFixture(t)
	result, err := processReplyBatch(t.Context(), flow, conversations, time.Now, func() bool { return false }, nil)
	if err != nil || result.Stats.Checked != 3 || result.Stats.Replied != 3 || fixture.sends != 3 || len(result.Remaining) != 1 {
		t.Fatalf("批次越界 %+v %v sends=%d", result, err, fixture.sends)
	}
}

// TestReplyBatchTimeBoundary 验证单人耗时超过上限仍完成发送确认，再停止领取下一人。
func TestReplyBatchTimeBoundary(t *testing.T) {
	flow, fixture, conversations := batchReplyFixture(t)
	now := time.Now()
	fixture.generate = func(context.Context) (string, error) { now = now.Add(61 * time.Second); return "测试回复", nil }
	result, err := processReplyBatch(t.Context(), flow, conversations, func() time.Time { return now }, func() bool { return false }, nil)
	if err != nil || result.Stats.Replied != 1 || fixture.confirmCalls != 1 || len(result.Remaining) != 3 {
		t.Fatalf("安全单元被切断 %+v %v", result, err)
	}
}

// TestReplyBatchStopAndIdentity 验证停止不读新会话，读取另一个同名会话也不能发送。
func TestReplyBatchStopAndIdentity(t *testing.T) {
	flow, fixture, conversations := batchReplyFixture(t)
	result, err := processReplyBatch(t.Context(), flow, conversations, time.Now, func() bool { return true }, nil)
	if err == nil || fixture.readCalls != 0 || len(result.Remaining) != 4 {
		t.Fatal("停止仍领取会话")
	}
	fixture.reads[0].Conversation.ID = "another-same-name"
	result, err = processReplyBatch(t.Context(), flow, conversations[:1], time.Now, func() bool { return false }, nil)
	if err != nil || result.Stats.Skipped != 1 || fixture.sends != 0 || fixture.generations != 0 {
		t.Fatalf("错误身份进入生成或发送 %+v %v", result, err)
	}
}
