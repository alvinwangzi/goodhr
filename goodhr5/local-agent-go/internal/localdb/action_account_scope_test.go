// 本文件用真实 SQLite 验证运行账号不可变、云端所有者隔离和绑定失败事务回滚。
package localdb

import (
	"testing"
)

// TestActionAccountBinding 验证两个实际账号使用独立作用域，旧默认记录不自动归属新账号。
func TestActionAccountBinding(t *testing.T) {
	db, _ := openReplyDB(t)
	create := func() ActionCheckpoint {
		cp, err := db.CreateActionRun(t.Context(), ActionCheckpoint{PositionID: "p", ProfileScope: "legacy-default", Platform: "boss"})
		if err != nil {
			t.Fatal(err)
		}
		return cp
	}
	first := create()
	bound, err := db.BindActionRunAccount(t.Context(), first.RunID, "account-A", "cloud-owner-A")
	if err != nil || !bound.AccountBound || bound.ProfileScope != "account-A" {
		t.Fatalf("账号绑定失败 %+v %v", bound, err)
	}
	stale := bound
	stale.AccountBound = false
	if err = db.SaveActionCheckpoint(t.Context(), stale); err == nil {
		t.Fatal("旧检查点清除了账号绑定标记")
	}
	if err = db.SaveActionCandidate(t.Context(), stale, "candidate-stale", "processing", "candidate_processing"); err == nil {
		t.Fatal("候选人保存绕过账号绑定标记")
	}
	if _, err = db.BindActionRunAccount(t.Context(), first.RunID, "account-B", "cloud-owner-A"); err == nil {
		t.Fatal("运行内换账号被允许")
	}
	if _, err = db.BindActionRunAccount(t.Context(), first.RunID, "account-A", "cloud-owner-B"); err == nil {
		t.Fatal("账号作用域跨所有者复用")
	}
	second := create()
	if _, err = db.BindActionRunAccount(t.Context(), second.RunID, "account-B", "cloud-owner-B"); err != nil {
		t.Fatal(err)
	}
	for owner, wanted := range map[string]string{"cloud-owner-A": "account-A", "cloud-owner-B": "account-B"} {
		scopes, err := db.AccountScopesForOwner(t.Context(), owner)
		if err != nil || len(scopes) != 1 || scopes[0] != wanted {
			t.Fatalf("混入其他所有者或旧默认作用域 %v %v", scopes, err)
		}
	}
	third := create()
	if err = db.SaveActionCandidate(t.Context(), third, "candidate", "processing", "greet_sending"); err != nil {
		t.Fatal(err)
	}
	if _, err = db.BindActionRunAccount(t.Context(), third.RunID, "account-C", "cloud-owner-A"); err == nil {
		t.Fatal("已开始处理后改变账号")
	}
	stored, err := db.LoadActionCheckpoint(t.Context(), third.RunID)
	if err != nil || stored.AccountBound || stored.ProfileScope != "legacy-default" {
		t.Fatal("拒绝绑定后检查点被修改")
	}
	scopes, err := db.AccountScopesForOwner(t.Context(), "cloud-owner-A")
	if err != nil || len(scopes) != 1 {
		t.Fatal("失败事务创建了账号登记")
	}
}
