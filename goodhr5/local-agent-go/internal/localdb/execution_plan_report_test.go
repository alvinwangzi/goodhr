// 本文件用真实检查点和原索要归属验证 HRPlus 报告，重复岗位保持独立，不推算累计数量。
package localdb

import (
	"strings"
	"testing"
	"time"
)

// TestPlanReportOriginalFacts 验证原任务实际数量、同步等待、未完成项与次日时间分别呈现。
func TestPlanReportOriginalFacts(t *testing.T) {
	db, _ := openReplyDB(t)
	_, run := snapshotFixture(t)
	if _, err := db.UpsertPositionSnapshot(map[string]any{"id": "same-job", "name": "岗位", "platform_id": "boss"}); err != nil {
		t.Fatal(err)
	}
	run.State = "incomplete"
	run.EndReason = "plan_window_ended"
	run.Items[0].State = "stopped"
	run.Items[1].State = "stopped"
	run.Items[0].TaskRunID = "70000000-0000-0000-0000-000000000001"
	for action, p := range run.Items[0].Actions {
		p.State = "stopped"
		run.Items[0].Actions[action] = p
	}
	cp, err := db.EnsurePlanActionRun(t.Context(), ActionCheckpoint{PlanRunID: run.ID, ItemRunID: run.Items[0].ID, CloudRunID: run.Items[0].TaskRunID, OwnerScope: "A", PositionID: "same-job", Platform: "boss", ProfileScope: "fixture", TaskType: "auto_reply,re_greet", PositionSnapshot: map[string]any{"name": "岗位"}})
	if err != nil {
		t.Fatal(err)
	}
	cp.Scanned = 4
	cp.Replied = 1
	cp.ReGreeted = 2
	cp.ReplyStats = map[string]int{"unknown": 1, "failed": 1}
	if err := db.SaveActionCheckpoint(t.Context(), cp); err != nil {
		t.Fatal(err)
	}
	request, err := db.EnqueueCandidateInfoRequest("same-job", "boss", "fixture-geek", "同名", map[string]bool{"resume": true})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.ClaimCandidateInfoForRun(t.Context(), request.ID, "resume", cp.RunID); err != nil {
		t.Fatal(err)
	}
	if err := db.SaveCandidateInfoResult(request.ID, "resume", "requested"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SavePlanOperation(t.Context(), PlanOperation{OwnerScope: "A", RequestID: "60000000-0000-0000-0000-000000000099", PlanID: run.PlanID, RunID: run.ID, OwnerID: run.OwnerID, Kind: "release", RunSequence: 3, BodyHash: strings.Repeat("a", 64), Cipher: []byte("fixture encrypted")}); err != nil {
		t.Fatal(err)
	}
	generated := time.Date(2026, 10, 10, 12, 6, 0, 0, time.UTC)
	report, err := db.BuildPlanReport(t.Context(), "A", run, generated, true)
	if err != nil {
		t.Fatal(err)
	}
	if report.Kind != "day_incomplete" || report.SyncState != "pending" || len(report.UnfinishedItemIDs) != 2 || report.NextNominalAt == nil || report.NextNominalAt.Format("2006-01-02 15:04") != "2026-10-11 09:00" {
		t.Fatal("报告周期或状态错误")
	}
	first, second := report.Items[0], report.Items[1]
	if first.Actions["auto_reply"].Confirmed != 1 || first.Actions["auto_reply"].Unknown != 1 || first.Actions["re_greet"].Confirmed != 2 || first.Information["resume"].Confirmed != 1 || first.TaskRunID == "" || second.TaskRunID != "" || second.Actions["greeting"].Confirmed != 0 {
		t.Fatal("实际数量被混算")
	}
	if _, err := db.BuildPlanReport(t.Context(), "B", run, generated, true); err == nil {
		t.Fatal("另一账号读取原检查点生成报告")
	}
	if cp.Scanned != 4 || run.Items[0].Actions["auto_reply"].Count != 0 {
		t.Fatal("报告改写原输入")
	}
}

// TestPlanReportRejectsActiveRun 验证运行未结束时不能生成结束报告，也不把缺失细节当完整数据。
func TestPlanReportRejectsActiveRun(t *testing.T) {
	db, _ := openReplyDB(t)
	_, run := snapshotFixture(t)
	if _, err := db.BuildPlanReport(t.Context(), "A", run, time.Now(), true); err == nil {
		t.Fatal("starting 生成结束报告")
	}
}
