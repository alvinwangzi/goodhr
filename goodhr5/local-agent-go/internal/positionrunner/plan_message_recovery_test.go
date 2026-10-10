// 本文件验证 HRPlus M2 消息适配器丢失内存后恢复原剩余名单及统计，不重发已处理候选人。
package positionrunner

import (
	"goodhr5/local-agent-go/internal/actiondispatch"
	"testing"
)

// TestPlanMessageSessionRecovery 使用实际有限批次和原检查点重建消息会话，不创建新的 TaskRun。
func TestPlanMessageSessionRecovery(t *testing.T) {
	child, permit, snapshot, page := planMessageFixture(t, 6)
	first, err := child.MessageStep(permit, snapshot, true)
	if err != nil || !first.Remaining || page.sends != 3 {
		t.Fatal("第一批没有保留剩余名单", first, err, page.sends)
	}
	cp, err := child.parent.runner.db.LoadActionCheckpoint(t.Context(), child.state.options.LocalRunID)
	if err != nil || len(cp.MessageState) == 0 {
		t.Fatal("原消息检查点未持久保存", err)
	}
	// 仅保留受控页面及 AI 实现，清空全部业务队列和调度内存；真实进程重启另行验收。
	rebuilt := *child.messages
	rebuilt.replies = nil
	rebuilt.reGreets = nil
	rebuilt.infoQueue = nil
	rebuilt.handledInfo = nil
	rebuilt.handledReGreets = nil
	rebuilt.scheduler = actiondispatch.New(snapshot.Options.PrioritizeReply)
	child.messages = &rebuilt
	child.messageStatsRestored = false
	second, err := child.MessageStep(permit, snapshot, false)
	if err != nil || second.Remaining || page.sends != 6 || second.TaskRunID != first.TaskRunID {
		t.Fatal("重建后重复发送或原归属改变", second, err, page.sends)
	}
	restored, err := child.parent.runner.db.LoadActionCheckpoint(t.Context(), cp.RunID)
	if err != nil || restored.Replied != cp.Replied+3 || restored.CloudRunID != cp.CloudRunID {
		t.Fatal("重建重置实际数量", restored, err)
	}
}
