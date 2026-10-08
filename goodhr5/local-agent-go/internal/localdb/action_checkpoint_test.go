// 本文件用真实 SQLite 验证 HRPlus 单岗位检查点原子性、身份冲突和复打到期恢复。
package localdb

import (
	"errors"
	"testing"
	"time"
)

// TestActionCheckpointRollback 验证候选人结果失败时检查点和计数也回滚。
func TestActionCheckpointRollback(t *testing.T) {
	db, _ := openReplyDB(t)
	checkpoint, err := db.CreateActionRun(t.Context(), ActionCheckpoint{PositionID: "p", ProfileScope: "s", Platform: "boss"})
	if err != nil {
		t.Fatal(err)
	}
	checkpoint.Greeted = 1
	if err = db.SaveActionCandidate(t.Context(), checkpoint, "geek-A", "invalid", ""); err == nil {
		t.Fatal("无效状态应回滚")
	}
	saved, err := db.LoadActionCheckpoint(t.Context(), checkpoint.RunID)
	if err != nil || saved.Greeted != 0 {
		t.Fatalf("事务未回滚: %+v %v", saved, err)
	}
	checkpoint.Anchors = []string{"geek-A"}
	if err = db.SaveActionCandidate(t.Context(), checkpoint, "geek-A", "completed", ""); err != nil {
		t.Fatal(err)
	}
	if status, err := db.ActionCandidateStatus(t.Context(), checkpoint.RunID, "p", "geek-A"); err != nil || status != "completed" {
		t.Fatalf("%s %v", status, err)
	}
}

// TestCandidateIdentityConflict 验证双向映射冲突不会覆盖真实身份，账号隔离且未知不能降级已验证映射。
func TestCandidateIdentityConflict(t *testing.T) {
	db, _ := openReplyDB(t)
	identity := CandidateIdentity{ProfileScope: "s", Platform: "boss", RecommendationID: "opaque-A", ConversationID: "123-0", Source: "direct-transition", Status: "verified"}
	if err := db.SaveCandidateIdentity(t.Context(), identity); err != nil {
		t.Fatal(err)
	}
	unknown := identity
	unknown.ConversationID = ""
	unknown.Status = "unresolved"
	unknown.Source = "legacy"
	if err := db.SaveCandidateIdentity(t.Context(), unknown); err != nil {
		t.Fatal(err)
	}
	saved, err := db.CandidateIdentityFor(t.Context(), "s", "boss", "opaque-A")
	if err != nil || saved.Status != "verified" || saved.ConversationID != "123-0" || saved.Source != "direct-transition" {
		t.Fatalf("%+v %v", saved, err)
	}
	other := identity
	other.RecommendationID = "opaque-B"
	if err := db.SaveCandidateIdentity(t.Context(), other); !errors.Is(err, ErrIdentityConflict) {
		t.Fatalf("应拒绝反向同一身份: %v", err)
	}
	for _, id := range []string{"opaque-A", "opaque-B"} {
		saved, err := db.CandidateIdentityFor(t.Context(), "s", "boss", id)
		if err != nil || saved.Status != "conflict" {
			t.Fatalf("%+v %v", saved, err)
		}
	}
	identity.ProfileScope = "other-account"
	if err := db.SaveCandidateIdentity(t.Context(), identity); err != nil {
		t.Fatal(err)
	}
}

// TestReGreetDueSurvivesRestart 验证同一联系事实只采样一次，重新联系后才更新到期时间。
func TestReGreetDueSurvivesRestart(t *testing.T) {
	db, cfg := openReplyDB(t)
	original := time.Date(2026, 10, 8, 1, 38, 0, 0, time.UTC)
	if _, err := db.EnsureReGreetDue(t.Context(), "s", "boss", "A", "contact-1", original); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := Open(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	due, err := db.EnsureReGreetDue(t.Context(), "s", "boss", "A", "contact-1", original.Add(time.Hour))
	if err != nil || !due.Equal(original) {
		t.Fatalf("重启重新采样: %v %v", due, err)
	}
	due, err = db.EnsureReGreetDue(t.Context(), "s", "boss", "A", "contact-2", original.Add(time.Hour))
	if err != nil || !due.Equal(original.Add(time.Hour)) {
		t.Fatalf("新基准未更新: %v %v", due, err)
	}
}
