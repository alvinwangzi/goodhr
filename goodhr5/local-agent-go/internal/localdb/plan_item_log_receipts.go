// 本文件把 HRPlus 原日志本身作为持久待办，完整云端回执后才确认，不清空本地历史。
package localdb

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
)

// migratePlanItemLogReceipts 保存原内容确认摘要，重开后不重复补传已确认记录。
func (db *DB) migratePlanItemLogReceipts() error {
	_, err := db.conn.Exec(`CREATE TABLE IF NOT EXISTS plan_item_log_receipts(
-- 原账号作用域摘要。
owner_scope TEXT NOT NULL,
-- 原本地日志流水编号。
log_id INTEGER NOT NULL REFERENCES plan_item_logs(id),
-- 已确认原日志内容摘要。
body_hash TEXT NOT NULL,
PRIMARY KEY(owner_scope,log_id));`)
	return err
}

// NextPlanItemLogBatch 按最早原记录读取同一原执行项的待办，不把同岗位另一项混进批次。
func (db *DB) NextPlanItemLogBatch(ctx context.Context, scope string) ([]PlanItemLog, error) {
	var run, item string
	err := db.conn.QueryRowContext(ctx, `SELECT l.plan_run_id,l.item_run_id FROM plan_item_logs l LEFT JOIN plan_item_log_receipts q ON q.owner_scope=l.owner_scope AND q.log_id=l.id WHERE l.owner_scope=? AND q.log_id IS NULL ORDER BY l.id LIMIT 1`, scope).Scan(&run, &item)
	if err != nil {
		return nil, err
	}
	rows, err := db.conn.QueryContext(ctx, `SELECT l.id,l.owner_scope,l.plan_run_id,l.item_run_id,l.task_run_id,l.local_run_id,l.position_id,l.level,l.message,l.created_at FROM plan_item_logs l LEFT JOIN plan_item_log_receipts q ON q.owner_scope=l.owner_scope AND q.log_id=l.id WHERE l.owner_scope=? AND l.plan_run_id=? AND l.item_run_id=? AND q.log_id IS NULL ORDER BY l.id LIMIT 10`, scope, run, item)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []PlanItemLog{}
	for rows.Next() {
		var entry PlanItemLog
		if err := rows.Scan(&entry.ID, &entry.OwnerScope, &entry.PlanRunID, &entry.ItemRunID, &entry.TaskRunID, &entry.LocalRunID, &entry.PositionID, &entry.Level, &entry.Message, &entry.CreatedAt); err != nil {
			return nil, err
		}
		result = append(result, entry)
	}
	return result, rows.Err()
}

// ConfirmPlanItemLogs 在事务内逐条核对原内容，再确认整批，迟到回执不能确认另一个账号的流水。
func (db *DB) ConfirmPlanItemLogs(ctx context.Context, scope string, logs []PlanItemLog, hashes []string) error {
	if scope == "" || len(logs) == 0 || len(logs) != len(hashes) {
		return ErrPlanRequestConflict
	}
	tx, err := db.conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for index, expected := range logs {
		var actual PlanItemLog
		err = tx.QueryRowContext(ctx, `SELECT id,owner_scope,plan_run_id,item_run_id,task_run_id,local_run_id,position_id,level,message,created_at FROM plan_item_logs WHERE owner_scope=? AND id=?`, scope, expected.ID).Scan(&actual.ID, &actual.OwnerScope, &actual.PlanRunID, &actual.ItemRunID, &actual.TaskRunID, &actual.LocalRunID, &actual.PositionID, &actual.Level, &actual.Message, &actual.CreatedAt)
		if err != nil {
			return err
		}
		left, _ := json.Marshal(actual)
		right, _ := json.Marshal(expected)
		sum := sha256.Sum256(left)
		if expected.OwnerScope != scope || string(left) != string(right) || hashes[index] != hex.EncodeToString(sum[:]) {
			return ErrPlanRequestConflict
		}
		var old string
		err = tx.QueryRowContext(ctx, `SELECT body_hash FROM plan_item_log_receipts WHERE owner_scope=? AND log_id=?`, scope, expected.ID).Scan(&old)
		if err != nil && err != sql.ErrNoRows {
			return err
		}
		if old != "" && old != hashes[index] {
			return ErrPlanRequestConflict
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO plan_item_log_receipts(owner_scope,log_id,body_hash) VALUES(?,?,?) ON CONFLICT(owner_scope,log_id) DO NOTHING`, scope, expected.ID, hashes[index])
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}
