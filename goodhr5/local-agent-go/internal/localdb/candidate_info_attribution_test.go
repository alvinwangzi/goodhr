// 本文件用真实 SQLite 验证 HRPlus 索要动作原任务归属和原子结果保存，不涉及真实候选人。
package localdb

import "testing"

// TestInfoAttributionOriginalRun 验证重复岗位不能抢原索要，未知结果恢复后仍记原任务。
func TestInfoAttributionOriginalRun(t *testing.T) {
	db, _ := openReplyDB(t)
	if _, err := db.UpsertPositionSnapshot(map[string]any{"id": "same-job", "name": "岗位", "platform_id": "boss"}); err != nil {
		t.Fatal(err)
	}
	first, err := db.CreateActionRun(t.Context(), ActionCheckpoint{PositionID: "same-job", Platform: "boss", ProfileScope: "fixture", CloudRunID: "task-first"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := db.CreateActionRun(t.Context(), ActionCheckpoint{PositionID: "same-job", Platform: "boss", ProfileScope: "fixture", CloudRunID: "task-second"})
	if err != nil {
		t.Fatal(err)
	}
	request, err := db.EnqueueCandidateInfoRequest("same-job", "boss", "fixture-geek", "同名", map[string]bool{"resume": true, "phone": true})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.ClaimCandidateInfoForRun(t.Context(), request.ID, "resume", first.RunID); err != nil {
		t.Fatal(err)
	}
	if err := db.ClaimCandidateInfoForRun(t.Context(), request.ID, "resume", second.RunID); err == nil {
		t.Fatal("后续任务抢走索要")
	}
	if err := db.SaveCandidateInfoResult(request.ID, "resume", "unknown"); err != nil {
		t.Fatal(err)
	}
	if err := db.SaveCandidateInfoResult(request.ID, "resume", "satisfied"); err != nil {
		t.Fatal(err)
	}
	items, err := db.InfoAttributions(t.Context(), first.RunID)
	if err != nil || len(items) != 1 || items[0].CloudRunID != "task-first" || items[0].State != "satisfied" {
		t.Fatal("恢复改写来源或丢失结果", err)
	}
	if items, err := db.InfoAttributions(t.Context(), second.RunID); err != nil || len(items) != 0 {
		t.Fatal("原索要计入后续任务", err)
	}
}

// TestInfoAttributionRollback 验证归属写入失败不留下已发送状态，结果更新失败不丢原未知状态。
func TestInfoAttributionRollback(t *testing.T) {
	db, _ := openReplyDB(t)
	if _, err := db.UpsertPositionSnapshot(map[string]any{"id": "same-job", "name": "岗位", "platform_id": "boss"}); err != nil {
		t.Fatal(err)
	}
	cp, err := db.CreateActionRun(t.Context(), ActionCheckpoint{PositionID: "same-job", Platform: "boss", ProfileScope: "fixture", CloudRunID: "task-first"})
	if err != nil {
		t.Fatal(err)
	}
	request, err := db.EnqueueCandidateInfoRequest("same-job", "boss", "fixture-geek", "同名", map[string]bool{"resume": true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.conn.Exec(`CREATE TRIGGER fail_info_owner BEFORE INSERT ON candidate_info_attributions BEGIN SELECT RAISE(ABORT,'fixture failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err := db.ClaimCandidateInfoForRun(t.Context(), request.ID, "resume", cp.RunID); err == nil {
		t.Fatal("归属故障仍领取发送")
	}
	if _, err := db.conn.Exec(`DROP TRIGGER fail_info_owner`); err != nil {
		t.Fatal(err)
	}
	if err := db.ClaimCandidateInfoForRun(t.Context(), request.ID, "resume", cp.RunID); err != nil {
		t.Fatal("失败领取没有回滚", err)
	}
	if _, err := db.conn.Exec(`CREATE TRIGGER fail_info_result BEFORE UPDATE ON candidate_info_attributions BEGIN SELECT RAISE(ABORT,'fixture failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err := db.SaveCandidateInfoResult(request.ID, "resume", "requested"); err == nil {
		t.Fatal("结果故障没有回滚")
	}
	items, err := db.ListCandidateInfoRequests("same-job")
	if err != nil || items[0].Results["resume"] != "sending" {
		t.Fatal("原名单结果没有回滚", err)
	}
	if facts, err := db.InfoAttributions(t.Context(), cp.RunID); err != nil || facts[0].State != "unknown" {
		t.Fatal("发送中被当作成功", err)
	}
}
