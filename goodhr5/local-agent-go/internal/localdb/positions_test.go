// Package localdb 负责测试本地岗位运行数据库能力。
package localdb

import (
	"database/sql"
	"encoding/json"
	"path/filepath"
	"testing"

	"goodhr5/local-agent-go/internal/config"
)

// TestPositionLogCandidateFlow 验证岗位运行、日志和候选人的基本读写流程。
func TestPositionLogCandidateFlow(t *testing.T) {
	db := openTestDB(t)
	position, err := db.CreatePosition(map[string]any{
		"name":        "测试岗位运行",
		"platform_id": "boss",
		"match_limit": 3,
	})
	if err != nil {
		t.Fatal(err)
	}
	if position.ID == "" || position.Status != "pending" {
		t.Fatalf("unexpected position: %+v", position)
	}
	updated, err := db.UpdatePositionStatus(position.ID, "running")
	if err != nil {
		t.Fatal(err)
	}
	if updated.Status != "running" {
		t.Fatalf("status = %s", updated.Status)
	}
	if _, err := db.AddPositionLog(position.ID, "info", "开始岗位运行"); err != nil {
		t.Fatal(err)
	}
	logs, err := db.ListPositionLogs(position.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(logs) != 1 || logs[0].Message != "开始岗位运行" {
		t.Fatalf("logs = %+v", logs)
	}
	candidate, err := db.SaveCandidate(position.ID, map[string]any{"name": "候选人A", "status": "scanned"})
	if err != nil {
		t.Fatal(err)
	}
	if candidate["id"] == "" {
		t.Fatalf("candidate missing id: %+v", candidate)
	}
	candidates, err := db.ListCandidates(position.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 1 {
		t.Fatalf("candidates len = %d", len(candidates))
	}
}

// TestSettingsRecordsFlow 验证本地设置和运行记录读写流程。
func TestSettingsRecordsFlow(t *testing.T) {
	db := openTestDB(t)
	settings, err := db.SaveSettings(map[string]any{"browser_download_dir": "/tmp/goodhr-downloads"})
	if err != nil {
		t.Fatal(err)
	}
	if settings["browser_download_dir"] != "/tmp/goodhr-downloads" {
		t.Fatalf("settings = %+v", settings)
	}

	download, err := db.SaveDownload(map[string]any{
		"position_id": "position-1",
		"url":         "https://example.com/a.pdf",
		"file_path":   "/tmp/a.pdf",
		"file_name":   "a.pdf",
		"mime_type":   "application/pdf",
		"size":        json.Number("12"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if download.FileName != "a.pdf" || download.Size != 12 {
		t.Fatalf("download = %+v", download)
	}
	downloads, err := db.ListDownloads("position-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(downloads) != 1 {
		t.Fatalf("downloads len = %d", len(downloads))
	}

}

// TestMigratePositionScannedCountsRepairsLegacyValueOnce 验证旧版扫描统计只补偿一次跳过和失败人数。
func TestMigratePositionScannedCountsRepairsLegacyValueOnce(t *testing.T) {
	db := openTestDB(t)
	position, err := db.CreatePosition(map[string]any{"name": "旧版统计岗位", "platform_id": "boss"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.conn.Exec(`
UPDATE local_positions
SET scanned_count=53, greeted_count=53, skipped_count=9, failed_count=0
WHERE id=?
`, position.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.conn.Exec(`DELETE FROM local_meta WHERE key='position_scanned_count_semantics_v2'`); err != nil {
		t.Fatal(err)
	}
	if err := db.migratePositionScannedCounts(); err != nil {
		t.Fatal(err)
	}
	if err := db.migratePositionScannedCounts(); err != nil {
		t.Fatal(err)
	}
	updated, err := db.GetPosition(position.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.ScannedCount != 62 || updated.GreetedCount != 53 || updated.SkippedCount != 9 || updated.FailedCount != 0 {
		t.Fatalf("position counts = %+v", updated)
	}
}

// TestDownloadSavedCannotBeDowngraded 验证异步成功通知先到时，迟到的失败或等待结果不能抹掉文件记录。
func TestDownloadSavedCannotBeDowngraded(t *testing.T) {
	db := openTestDB(t)
	if _, err := db.SaveDownload(map[string]any{"id": "download1", "source_key": "account-platform-conversation", "position_id": "p1", "status": "saved", "file_path": "resume.pdf", "size": 100}); err != nil {
		t.Fatal(err)
	}
	for _, status := range []string{"pending", "failed"} {
		if _, err := db.SaveDownload(map[string]any{"id": "download1", "status": status}); err != nil {
			t.Fatal(err)
		}
		got, err := db.LatestSourceDownload("account-platform-conversation")
		if err != nil || got.Status != "saved" || got.FilePath != "resume.pdf" || got.PositionID != "p1" {
			t.Fatalf("成功记录被迟到消息覆盖：%+v %v", got, err)
		}
	}
}

// TestDownloadSourceMigrationPreservesHistory 验证旧下载表升级后保留历史，不猜测旧文件归属，重启后仍可按来源去重。
func TestDownloadSourceMigrationPreservesHistory(t *testing.T) {
	dir := t.TempDir()
	conn, err := sql.Open("sqlite", filepath.Join(dir, "goodhr_local_go.db"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = conn.Exec(`CREATE TABLE local_downloads (id TEXT PRIMARY KEY, position_id TEXT, url TEXT, file_path TEXT, file_name TEXT, mime_type TEXT, size INTEGER, status TEXT, created_at TEXT, updated_at TEXT);
INSERT INTO local_downloads VALUES ('legacy','p1','','old.pdf','old.pdf','',10,'saved','2026-01-01','2026-01-01')`)
	_ = conn.Close()
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{DataDir: dir}
	db, err := Open(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	list, err := db.ListDownloads("p1")
	if err != nil || len(list) != 1 || list[0].SourceKey != "" || list[0].FilePath != "old.pdf" {
		t.Fatalf("旧记录丢失或错误关联：%+v %v", list, err)
	}
	if _, err := db.SaveDownload(map[string]any{"id": "new", "source_key": "source1", "status": "saved", "file_path": "new.pdf"}); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	db, err = Open(cfg)
	if err != nil {
		t.Fatal(err)
	}
	got, err := db.LatestSourceDownload("source1")
	if err != nil || got.ID != "new" || got.Status != "saved" {
		t.Fatalf("重启后关联丢失：%+v %v", got, err)
	}
	if _, err := db.LatestSourceDownload("different-account"); err != sql.ErrNoRows {
		t.Fatalf("不同来源错误命中：%v", err)
	}
}

// openTestDB 创建测试数据库。
// t 为测试对象。
func openTestDB(t *testing.T) *DB {
	t.Helper()
	cfg := &config.Config{DataDir: t.TempDir()}
	db, err := Open(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}
