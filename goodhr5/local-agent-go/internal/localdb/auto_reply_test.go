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

// TestAutoReplyDecisionRestart 验证不发送决策跨数据库重开仍防重，且不影响新消息和其他账号。
func TestAutoReplyDecisionRestart(t *testing.T) {
	for _, action := range []string{"skip", "uncertain"} {
		t.Run(action, func(t *testing.T) {
			db, cfg := openReplyDB(t)
			key := replyIntent()
			if err := db.SaveAutoReplyDecision(t.Context(), key, action); err != nil {
				t.Fatal(err)
			}
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			reopened, err := Open(cfg)
			if err != nil {
				t.Fatal(err)
			}
			defer reopened.Close()
			key.PositionID, key.RunID = "another-position", "another-run"
			got, err := reopened.FindAutoReply(t.Context(), key)
			if err != nil || got.Status != action {
				t.Fatalf("重启丢失决策：%+v %v", got, err)
			}
			if _, err := reopened.PrepareAutoReply(t.Context(), key); !errors.Is(err, ErrAutoReplyConflict) {
				t.Fatalf("跳过后仍可发送：%v", err)
			}
			if err := reopened.SaveAutoReplyDecision(t.Context(), key, action); !errors.Is(err, ErrAutoReplyConflict) {
				t.Fatalf("允许重复判断：%v", err)
			}
			for _, field := range []string{"message", "profile", "platform", "conversation"} {
				next := key
				switch field {
				case "message":
					next.InboundFingerprint = "new-message"
				case "profile":
					next.ProfileScope = "other-profile"
				case "platform":
					next.Platform = "other-platform"
				case "conversation":
					next.ConversationID = "other-conversation"
				}
				if _, err := reopened.PrepareAutoReply(t.Context(), next); err != nil {
					t.Fatalf("%s 被旧决策误拦截：%v", field, err)
				}
			}
		})
	}
}

// TestAutoReplyDecisionProtectsSendState 验证跳过不能覆盖已发送或结果未知，也不能让旧发送意图重新生效。
func TestAutoReplyDecisionProtectsSendState(t *testing.T) {
	for _, state := range []string{"prepared", "obsolete", "sending", "sent", "unknown"} {
		t.Run(state, func(t *testing.T) {
			db, _ := openReplyDB(t)
			key := replyIntent()
			record, err := db.PrepareAutoReply(t.Context(), key)
			if err != nil {
				t.Fatal(err)
			}
			if state == "obsolete" {
				if err := db.TransitionAutoReply(t.Context(), record.ID, "prepared", state, ""); err != nil {
					t.Fatal(err)
				}
			} else if state != "prepared" {
				if err := db.TransitionAutoReply(t.Context(), record.ID, "prepared", "sending", ""); err != nil {
					t.Fatal(err)
				}
				if state != "sending" {
					if err := db.TransitionAutoReply(t.Context(), record.ID, "sending", state, ""); err != nil {
						t.Fatal(err)
					}
				}
			}
			err = db.SaveAutoReplyDecision(t.Context(), key, "skip")
			want := state
			if state == "prepared" || state == "obsolete" {
				if err != nil {
					t.Fatal(err)
				}
				want = "skip"
				if err := db.TransitionAutoReply(t.Context(), record.ID, "prepared", "sending", ""); !errors.Is(err, ErrAutoReplyConflict) {
					t.Fatalf("旧意图覆盖跳过结果：%v", err)
				}
			} else if !errors.Is(err, ErrAutoReplyConflict) {
				t.Fatalf("%s 被跳过覆盖：%v", state, err)
			}
			got, err := db.FindAutoReply(t.Context(), key)
			if err != nil || got.Status != want {
				t.Fatalf("状态=%s want=%s err=%v", got.Status, want, err)
			}
		})
	}
}

// TestAutoReplyDecisionConcurrentSending 验证并发跳过与发送抢占时，只允许一个最终决策成功。
func TestAutoReplyDecisionConcurrentSending(t *testing.T) {
	db, _ := openReplyDB(t)
	for i := 0; i < 10; i++ {
		key := replyIntent()
		key.ConversationID = string(rune('a' + i))
		record, err := db.PrepareAutoReply(t.Context(), key)
		if err != nil {
			t.Fatal(err)
		}
		start := make(chan struct{})
		results := make(chan error, 2)
		go func() { <-start; results <- db.SaveAutoReplyDecision(t.Context(), key, "skip") }()
		go func() { <-start; results <- db.TransitionAutoReply(t.Context(), record.ID, "prepared", "sending", "") }()
		close(start)
		success := 0
		for j := 0; j < 2; j++ {
			if err := <-results; err == nil {
				success++
			} else if !errors.Is(err, ErrAutoReplyConflict) {
				t.Fatal(err)
			}
		}
		if success != 1 {
			t.Fatalf("跳过与发送同时生效：成功次数=%d", success)
		}
	}
}

// TestAutoReplyDecisionInvalid 验证格式、身份、取消和存储错误不会保存跳过决策。
func TestAutoReplyDecisionInvalid(t *testing.T) {
	db, _ := openReplyDB(t)
	for _, action := range []string{"", "reply", "sent"} {
		if err := db.SaveAutoReplyDecision(t.Context(), replyIntent(), action); err == nil {
			t.Fatalf("接受错误决策 %q", action)
		}
	}
	key := replyIntent()
	key.InboundFingerprint = ""
	if err := db.SaveAutoReplyDecision(t.Context(), key, "skip"); err == nil {
		t.Fatal("接受空消息标识")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := db.SaveAutoReplyDecision(ctx, replyIntent(), "skip"); !errors.Is(err, context.Canceled) {
		t.Fatalf("取消未生效：%v", err)
	}
	_ = db.Close()
	if err := db.SaveAutoReplyDecision(t.Context(), replyIntent(), "skip"); err == nil {
		t.Fatal("存储关闭仍保存成功")
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
