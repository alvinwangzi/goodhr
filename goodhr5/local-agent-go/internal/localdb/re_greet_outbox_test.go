// 本文件用真实 SQLite 验证 HRPlus 已确认发送与补传原子性、重启恢复和待补传阻止下一次发送。
package localdb

import (
	"encoding/json"
	"errors"
	"testing"
	"time"
)

// TestReGreetOutboxAtomicRestart 验证确认成功和补传一起保存，重启仍保留原发送意图。
func TestReGreetOutboxAtomicRestart(t *testing.T) {
	db, cfg := openReplyDB(t)
	record, err := db.PrepareAutoReply(t.Context(), replyIntent())
	if err != nil {
		t.Fatal(err)
	}
	if err = db.TransitionAutoReply(t.Context(), record.ID, "prepared", "sending", ""); err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(map[string]any{"operation_id": record.ID, "platform_candidate_id": "candidate-A", "message_text": "复打消息"})
	entry := ReGreetOutbox{OperationID: record.ID, ProfileScope: record.ProfileScope, Platform: record.Platform, CandidateID: "candidate-A", APIBase: "https://fixture.invalid", Payload: payload}
	// 实际 INSERT 失败必须回滚前面的 sent 更新。
	if _, err = db.conn.Exec(`CREATE TRIGGER fail_outbox BEFORE INSERT ON re_greet_outbox BEGIN SELECT RAISE(ABORT,'test failure'); END;`); err != nil {
		t.Fatal(err)
	}
	if err = db.ConfirmReGreetAndQueue(t.Context(), entry); err == nil {
		t.Fatal("补传插入失败未返回错误")
	}
	saved, err := db.FindAutoReply(t.Context(), record)
	if err != nil || saved.Status != "sending" {
		t.Fatalf("事务未回滚 %+v %v", saved, err)
	}
	if _, err = db.conn.Exec(`DROP TRIGGER fail_outbox`); err != nil {
		t.Fatal(err)
	}
	if err = db.ConfirmReGreetAndQueue(t.Context(), entry); err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = Open(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	entries, err := db.DueReGreetOutbox(t.Context(), record.ProfileScope, time.Now().Add(time.Second), 10)
	if err != nil || len(entries) != 1 || entries[0].OperationID != record.ID || string(entries[0].Payload) != string(payload) {
		t.Fatalf("重启未保留事实 %+v %v", entries, err)
	}
	if pending, err := db.PendingReGreetForCandidate(t.Context(), record.ProfileScope, record.Platform, "candidate-A"); err != nil || !pending {
		t.Fatalf("候选人未阻止下一次 %v %v", pending, err)
	}
	if err = db.FinishReGreetUpload(t.Context(), entries[0], "acked", "", time.Now()); err != nil {
		t.Fatal(err)
	}
	if err = db.FinishReGreetUpload(t.Context(), entries[0], "pending", "network", time.Now()); !errors.Is(err, ErrAutoReplyConflict) {
		t.Fatal("迟到失败覆盖了已确认收据")
	}
	if pending, err := db.PendingReGreetForCandidate(t.Context(), record.ProfileScope, record.Platform, "candidate-A"); err != nil || pending {
		t.Fatalf("已确认仍被阻止 %v %v", pending, err)
	}
}

// TestReGreetRetrySchedule 验证重试退避和不同账号队列隔离。
func TestReGreetRetrySchedule(t *testing.T) {
	want := []time.Duration{5 * time.Second, 30 * time.Second, 2 * time.Minute, 5 * time.Minute, 5 * time.Minute}
	for i, delay := range want {
		if ReGreetRetryDelay(i) != delay {
			t.Fatalf("第%d次退避错误", i)
		}
	}
	db, _ := openReplyDB(t)
	record, err := db.PrepareAutoReply(t.Context(), replyIntent())
	if err != nil {
		t.Fatal(err)
	}
	if err = db.TransitionAutoReply(t.Context(), record.ID, "prepared", "sending", ""); err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(map[string]any{"operation_id": record.ID, "platform_candidate_id": "A"})
	if err = db.ConfirmReGreetAndQueue(t.Context(), ReGreetOutbox{OperationID: record.ID, ProfileScope: record.ProfileScope, Platform: record.Platform, CandidateID: "A", APIBase: "https://fixture.invalid", Payload: payload}); err != nil {
		t.Fatal(err)
	}
	entries, err := db.DueReGreetOutbox(t.Context(), "other-account", time.Now().Add(time.Hour), 10)
	if err != nil || len(entries) != 0 {
		t.Fatalf("跨账号补传 %+v %v", entries, err)
	}
}
