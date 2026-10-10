// 本文件用真实 SQLite 重开验证 HRPlus 报告原摘要不可变及同步状态独立更新。
package localdb

import (
	"database/sql"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

// TestReportSnapshotRestart 验证重复保存不新增，原内容改变拒绝，补同步与重开不修改生成时间和数量。
func TestReportSnapshotRestart(t *testing.T) {
	db, cfg := openReplyDB(t)
	_, run := snapshotFixture(t)
	run.State = "incomplete"
	run.Items[0].State = "stopped"
	run.Items[1].State = "stopped"
	report, err := db.BuildPlanReport(t.Context(), "A", run, time.Date(2026, 10, 10, 12, 6, 0, 0, time.UTC), true)
	if err != nil {
		t.Fatal(err)
	}
	report.SyncState = "pending"
	first, err := db.SavePlanReportSnapshot(t.Context(), "A", report)
	if err != nil {
		t.Fatal(err)
	}
	if repeated, err := db.SavePlanReportSnapshot(t.Context(), "A", report); err != nil || repeated.BodyHash != first.BodyHash {
		t.Fatal("重复保存产生新报告", err)
	}
	changed := report
	changed.PlanName = "改写"
	if _, err := db.SavePlanReportSnapshot(t.Context(), "A", changed); !errors.Is(err, ErrPlanRequestConflict) {
		t.Fatal("允许替换原内容", err)
	}
	if err := db.SetPlanReportSync(t.Context(), "A", report.RunID, first.BodyHash, "confirmed"); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = Open(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	actual, err := db.PlanReportSnapshot(t.Context(), "A", report.RunID)
	actualJSON, _ := json.Marshal(actual.Report)
	expectedJSON, _ := json.Marshal(report)
	if err != nil || actual.SyncState != "confirmed" || actual.Report.SyncState != "pending" || string(actualJSON) != string(expectedJSON) {
		t.Fatal("重开或同步改写原快照", err)
	}
	if _, err := db.PlanReportSnapshot(t.Context(), "B", report.RunID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("另一账号读取报告", err)
	}
}
