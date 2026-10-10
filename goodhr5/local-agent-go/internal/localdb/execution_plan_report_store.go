// 本文件持久保存 HRPlus 原报告摘要与独立同步状态，补传不能覆盖首次生成内容。
package localdb

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"goodhr5/local-agent-go/internal/planmodel"
)

// PlanReportRecord 保留原报告及可更新传输状态，正文只含安全统计快照。
type PlanReportRecord struct {
	OwnerScope, RunID, BodyHash, UploadState, SyncState string
	Report                                              planmodel.Report
}

// migratePlanReports 建立原报告表，每个字段都有中文说明。
func (db *DB) migratePlanReports() error {
	_, err := db.conn.Exec(`CREATE TABLE IF NOT EXISTS plan_report_snapshots (
 -- 已核对的原账号作用域。
 owner_scope TEXT NOT NULL,
 -- 原运行编号，每次运行只有一份首次结束摘要。
 run_id TEXT NOT NULL,
 -- 原报告内容摘要。
 body_hash TEXT NOT NULL,
 -- 不含凭证的首次报告 JSON。
 summary_json TEXT NOT NULL,
 -- 报告补传是否获得原回执。
 upload_state TEXT NOT NULL DEFAULT 'pending' CHECK(upload_state IN ('pending','confirmed')),
 -- 当前运行事实是否仍等待同步。
 sync_state TEXT NOT NULL CHECK(sync_state IN ('pending','confirmed')),
 PRIMARY KEY(owner_scope,run_id)
);`)
	return err
}

// scanPlanReport 读取并核对原内容摘要，不解读用户名为任务指令。
func scanPlanReport(row interface{ Scan(...any) error }) (PlanReportRecord, error) {
	var item PlanReportRecord
	var raw string
	err := row.Scan(&item.OwnerScope, &item.RunID, &item.BodyHash, &raw, &item.UploadState, &item.SyncState)
	if err != nil {
		return item, err
	}
	hash := sha256.Sum256([]byte(raw))
	if hex.EncodeToString(hash[:]) != item.BodyHash {
		return item, ErrPlanRequestConflict
	}
	if err := json.Unmarshal([]byte(raw), &item.Report); err != nil {
		return item, err
	}
	if err := item.Report.Validate(); err != nil {
		return item, err
	}
	if item.Report.RunID != item.RunID {
		return item, ErrPlanRequestConflict
	}
	return item, nil
}

// PlanReportSnapshot 读取原报告，不重新生成时间或内容。
func (db *DB) PlanReportSnapshot(ctx context.Context, scope, runID string) (PlanReportRecord, error) {
	return scanPlanReport(db.conn.QueryRowContext(ctx, `SELECT owner_scope,run_id,body_hash,summary_json,upload_state,sync_state FROM plan_report_snapshots WHERE owner_scope=? AND run_id=?`, scope, runID))
}

// SavePlanReportSnapshot 首次保存原报告，同一运行内容改变时拒绝覆盖。
func (db *DB) SavePlanReportSnapshot(ctx context.Context, scope string, r planmodel.Report) (PlanReportRecord, error) {
	if scope == "" {
		return PlanReportRecord{}, ErrPlanRequestConflict
	}
	if err := r.Validate(); err != nil {
		return PlanReportRecord{}, err
	}
	raw, err := json.Marshal(r)
	if err != nil {
		return PlanReportRecord{}, err
	}
	hash := sha256.Sum256(raw)
	digest := hex.EncodeToString(hash[:])
	tx, err := db.conn.BeginTx(ctx, nil)
	if err != nil {
		return PlanReportRecord{}, err
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, `INSERT INTO plan_report_snapshots(owner_scope,run_id,body_hash,summary_json,sync_state) VALUES(?,?,?,?,?) ON CONFLICT(owner_scope,run_id) DO NOTHING`, scope, r.RunID, digest, string(raw), r.SyncState)
	if err != nil {
		return PlanReportRecord{}, err
	}
	item, err := scanPlanReport(tx.QueryRowContext(ctx, `SELECT owner_scope,run_id,body_hash,summary_json,upload_state,sync_state FROM plan_report_snapshots WHERE owner_scope=? AND run_id=?`, scope, r.RunID))
	if err != nil {
		return item, err
	}
	if item.BodyHash != digest {
		return item, ErrPlanRequestConflict
	}
	return item, tx.Commit()
}

// SetPlanReportSync 仅更新同步状态，不改写原摘要、生成时间或动作数量。
func (db *DB) SetPlanReportSync(ctx context.Context, scope, runID, hash, state string) error {
	if state != "pending" && state != "confirmed" {
		return ErrPlanRequestConflict
	}
	result, err := db.conn.ExecContext(ctx, `UPDATE plan_report_snapshots SET upload_state=CASE WHEN sync_state<>? THEN 'pending' ELSE upload_state END,sync_state=? WHERE owner_scope=? AND run_id=? AND body_hash=?`, state, state, scope, runID, hash)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrPlanRequestConflict
	}
	return nil
}

// NextPlanReportUpload 读取原账号的原报告补传，不重新生成摘要或通知接收人。
func (db *DB) NextPlanReportUpload(ctx context.Context, scope string) (PlanReportRecord, error) {
	return scanPlanReport(db.conn.QueryRowContext(ctx, `SELECT owner_scope,run_id,body_hash,summary_json,upload_state,sync_state FROM plan_report_snapshots WHERE owner_scope=? AND upload_state='pending' ORDER BY run_id LIMIT 1`, scope))
}

// ConfirmPlanReportUpload 只确认发送时的原内容和同步状态，迟到旧回执不确认后来状态。
func (db *DB) ConfirmPlanReportUpload(ctx context.Context, o PlanReportRecord) error {
	result, err := db.conn.ExecContext(ctx, `UPDATE plan_report_snapshots SET upload_state='confirmed' WHERE owner_scope=? AND run_id=? AND body_hash=? AND sync_state=?`, o.OwnerScope, o.RunID, o.BodyHash, o.SyncState)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrPlanSnapshotStale
	}
	return nil
}

// PlanRunSnapshots 读取原账号运行供历史报告补生成，不使用读取结果领取执行权。
func (db *DB) PlanRunSnapshots(ctx context.Context, scope string) ([]planmodel.Run, error) {
	rows, err := db.conn.QueryContext(ctx, `SELECT snapshot_json FROM plan_run_snapshots WHERE owner_scope=? ORDER BY execution_date,run_id`, scope)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []planmodel.Run{}
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var item planmodel.Run
		if err := json.Unmarshal([]byte(raw), &item); err != nil {
			return nil, err
		}
		if err := item.Validate(); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}
