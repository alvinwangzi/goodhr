// 本文件验证 HRPlus 原消息名单与公平预算从真实 SQLite 检查点恢复，错任务不能接管队列。
package positionrunner

import (
	"encoding/json"
	"goodhr5/local-agent-go/internal/actiondispatch"
	"goodhr5/local-agent-go/internal/cloudapi"
	"testing"
)

// TestMessageCheckpointRemainingQueue 处理六人后恢复原剩余两人，保留已消耗消息预算及原到期时间。
func TestMessageCheckpointRemainingQueue(t *testing.T) {
	s, page, now := cooperativeSessionFixture(t, true, 8)
	if _, err := s.service(t.Context(), true, true); err != nil {
		t.Fatal(err)
	}
	cp, err := s.runner.db.LoadActionCheckpoint(t.Context(), s.options.LocalRunID)
	if err != nil || len(cp.MessageState) == 0 || page.sends != 6 {
		t.Fatal("消息工作没有随检查点保存", err)
	}
	restored := *s
	restored.replies = nil
	restored.reGreets = nil
	restored.scheduler = actiondispatch.New(false)
	if err = restored.restoreMessageState(cp); err != nil {
		t.Fatal(err)
	}
	if len(restored.replies) != 2 || restored.replies[0].ID != s.replies[0].ID || restored.scheduler.Next(*now, actiondispatch.Work{Greeting: true, Reply: true}) != actiondispatch.Greeting {
		t.Fatal("恢复漏名单或重置公平预算")
	}
	s.reGreets = []dueReGreet{{candidate: cloudapi.ReGreetCandidate{PositionID: cp.PositionID, PlatformCandidateID: "geek-original"}, basis: "basis-original", due: *now, queuedAt: *now}}
	s.handledReGreets = map[string]bool{"already-processed": true}
	if err = s.saveDispatchCheckpoint(t.Context(), actiondispatch.ReGreet); err != nil {
		t.Fatal(err)
	}
	cp, err = s.runner.db.LoadActionCheckpoint(t.Context(), s.options.LocalRunID)
	if err != nil {
		t.Fatal(err)
	}
	if err = restored.restoreMessageState(cp); err != nil {
		t.Fatal(err)
	}
	if len(restored.reGreets) != 1 || restored.reGreets[0].basis != "basis-original" || !restored.reGreets[0].due.Equal(*now) || !restored.handledReGreets["already-processed"] {
		t.Fatal("原复打时间或已处理事实丢失")
	}
	changed := cp
	changed.CloudRunID = "other-task"
	if err = restored.restoreMessageState(changed); err == nil {
		t.Fatal("另一任务接管原名单")
	}
	var value savedMessageState
	if err = json.Unmarshal(cp.MessageState, &value); err != nil {
		t.Fatal(err)
	}
	value.Replies[0].ID = ""
	changed = cp
	changed.MessageState, _ = json.Marshal(value)
	if err = restored.restoreMessageState(changed); err == nil {
		t.Fatal("缺少会话身份仍恢复")
	}
}
