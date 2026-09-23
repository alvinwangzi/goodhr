// 本文件验证回复发送意图、防重、并发状态转换及崩溃恢复，使用真实 SQLite。
package localdb

import (
	"context"
	"errors"
	"sync"
	"testing"

	"goodhr5/local-agent-go/internal/config"
)

// openReplyDB 为测试创建独立数据库。
func openReplyDB(t *testing.T) (*DB, *config.Config) {
	t.Helper()
	cfg := &config.Config{DataDir: t.TempDir()}
	db, err := Open(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db, cfg
}

// replyIntent 提供仅含标识和指纹的发送意图，不保存正文。
func replyIntent() AutoReplyRecord {
	return AutoReplyRecord{ProfileScope: "profile-hash", Platform: "boss", ConversationID: "conversation-1", InboundFingerprint: "inbound-hash", PositionID: "position-1", RunID: "run-1", ContextFingerprint: "context-hash", ReplyFingerprint: "reply-hash"}
}

// TestAutoReplyDedupAcrossRuns 验证换任务、岗位或 AI 措辞不能重复发送。
func TestAutoReplyDedupAcrossRuns(t *testing.T) {
	db, _ := openReplyDB(t)
	first, err := db.PrepareAutoReply(t.Context(), replyIntent())
	if err != nil {
		t.Fatal(err)
	}
	if err := db.TransitionAutoReply(t.Context(), first.ID, "prepared", "sending", ""); err != nil {
		t.Fatal(err)
	}
	if err := db.TransitionAutoReply(t.Context(), first.ID, "sending", "sent", ""); err != nil {
		t.Fatal(err)
	}
	next := replyIntent()
	next.RunID = "run-2"
	next.PositionID = "position-2"
	next.ReplyFingerprint = "different-answer"
	if _, err := db.PrepareAutoReply(t.Context(), next); !errors.Is(err, ErrAutoReplyConflict) {
		t.Fatalf("重复消息未拦截：%v", err)
	}
	next.ConversationID = "conversation-2"
	if _, err := db.PrepareAutoReply(t.Context(), next); err != nil {
		t.Fatalf("不同会话被误拦截：%v", err)
	}
	next.ProfileScope = "another-profile"
	if _, err := db.PrepareAutoReply(t.Context(), next); err != nil {
		t.Fatalf("不同账号被误拦截：%v", err)
	}
}

// TestAutoReplyCrashRecovery 验证重启遗留 sending 变为 unknown，不能重新生成并发送。
func TestAutoReplyCrashRecovery(t *testing.T) {
	db, cfg := openReplyDB(t)
	record, err := db.PrepareAutoReply(t.Context(), replyIntent())
	if err != nil {
		t.Fatal(err)
	}
	if err := db.TransitionAutoReply(t.Context(), record.ID, "prepared", "sending", ""); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	db, err = Open(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	got, err := db.FindAutoReply(t.Context(), replyIntent())
	if err != nil || got.Status != "unknown" {
		t.Fatalf("恢复状态=%+v，错误=%v", got, err)
	}
	if _, err := db.PrepareAutoReply(t.Context(), replyIntent()); !errors.Is(err, ErrAutoReplyConflict) {
		t.Fatalf("不明结果被重发：%v", err)
	}
	if err := db.TransitionAutoReply(t.Context(), record.ID, "unknown", "prepared", ""); err == nil {
		t.Fatal("unknown 不能回退")
	}
	if err := db.TransitionAutoReply(t.Context(), record.ID, "unknown", "sent", ""); err != nil {
		t.Fatal(err)
	}
}

// TestAutoReplyRegenerationInvalidatesOldIntent 验证取消后重新生成会更换意图 ID，迟到调用无效。
func TestAutoReplyRegenerationInvalidatesOldIntent(t *testing.T) {
	db, _ := openReplyDB(t)
	first, err := db.PrepareAutoReply(t.Context(), replyIntent())
	if err != nil {
		t.Fatal(err)
	}
	if err := db.TransitionAutoReply(t.Context(), first.ID, "prepared", "sent", ""); err == nil {
		t.Fatal("不能跳过 sending")
	}
	if err := db.TransitionAutoReply(t.Context(), first.ID, "prepared", "obsolete", "cancelled"); err != nil {
		t.Fatal(err)
	}
	next := replyIntent()
	next.RunID = "run-2"
	next.ReplyFingerprint = "new-answer"
	second, err := db.PrepareAutoReply(t.Context(), next)
	if err != nil {
		t.Fatal(err)
	}
	if first.ID == second.ID {
		t.Fatal("新生成不能复用旧意图")
	}
	if err := db.TransitionAutoReply(t.Context(), first.ID, "prepared", "sending", ""); !errors.Is(err, ErrAutoReplyConflict) {
		t.Fatalf("旧意图仍可发送：%v", err)
	}
}

// TestAutoReplyConcurrentSending 验证同一意图并发进入发送时只有一次成功。
func TestAutoReplyConcurrentSending(t *testing.T) {
	db, _ := openReplyDB(t)
	record, err := db.PrepareAutoReply(t.Context(), replyIntent())
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results <- db.TransitionAutoReply(t.Context(), record.ID, "prepared", "sending", "")
		}()
	}
	wg.Wait()
	close(results)
	success := 0
	for err := range results {
		if err == nil {
			success++
		} else if !errors.Is(err, ErrAutoReplyConflict) {
			t.Fatal(err)
		}
	}
	if success != 1 {
		t.Fatalf("成功次数=%d", success)
	}
}

// TestAutoReplyStorageFailure 验证存储失败或标识不足时不产生可发送意图。
func TestAutoReplyStorageFailure(t *testing.T) {
	db, _ := openReplyDB(t)
	invalid := replyIntent()
	invalid.InboundFingerprint = ""
	if _, err := db.PrepareAutoReply(t.Context(), invalid); err == nil {
		t.Fatal("空指纹不应入库")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := db.PrepareAutoReply(ctx, replyIntent()); !errors.Is(err, context.Canceled) {
		t.Fatalf("取消错误=%v", err)
	}
	_ = db.Close()
	if _, err := db.PrepareAutoReply(t.Context(), replyIntent()); err == nil {
		t.Fatal("数据库关闭后不应允许发送")
	}
}
