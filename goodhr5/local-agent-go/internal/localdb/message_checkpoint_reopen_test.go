// 本文件验证 HRPlus 消息名单与统计在同一检查点保存，真实 SQLite 重开不丢原快照。
package localdb

import (
	"bytes"
	"encoding/json"
	"testing"
)

// TestMessageCheckpointSQLiteReopen 验证队列原字节与累计统计同时恢复，来源仍是原执行项。
func TestMessageCheckpointSQLiteReopen(t *testing.T) {
	db, cfg := openReplyDB(t)
	_, run := snapshotFixture(t)
	cp, err := db.EnsurePlanActionRun(t.Context(), ActionCheckpoint{PlanRunID: run.ID, ItemRunID: run.Items[0].ID, CloudRunID: "70000000-0000-0000-0000-000000000001", OwnerScope: "original-owner", PositionID: "same-job", Platform: "boss", ProfileScope: "fixture", TaskType: "auto_reply"})
	if err != nil {
		t.Fatal(err)
	}
	cp.MessageState = json.RawMessage(`{"schema":1,"replies":[{"ID":"conversation-original"}],"scheduler":{"message_batches":2}}`)
	cp.Replied = 6
	if err = db.SaveActionCheckpoint(t.Context(), cp); err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	restored, err := reopened.LoadActionCheckpoint(t.Context(), cp.RunID)
	if err != nil || restored.Replied != 6 || !bytes.Equal(restored.MessageState, cp.MessageState) || restored.CloudRunID != cp.CloudRunID || restored.ItemRunID != cp.ItemRunID {
		t.Fatal("消息名单与原统计/归属没有同时恢复", restored, err)
	}
}
