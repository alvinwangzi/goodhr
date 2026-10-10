// 本文件保留 HRPlus 原队列入队时刻和上报回执，旧记录缺少时刻时不补造。
package localdb

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// migratePlanWaitReceipts 为原队列建立独立元数据，不改变既有排队顺序或状态。
func (db *DB) migratePlanWaitReceipts() error {
	_, err := db.conn.Exec(`CREATE TABLE IF NOT EXISTS plan_wait_receipts(
-- 已核对的账号作用域摘要。
owner_scope TEXT NOT NULL,
-- 原排队请求编号。
request_id TEXT NOT NULL,
-- 实际首次入队纳秒时刻，历史未知时为空。
queued_ns INTEGER,
-- 已确认原上报内容摘要，未上报为空。
body_hash TEXT NOT NULL DEFAULT '',
PRIMARY KEY(owner_scope,request_id));`)
	return err
}

// PlanWaitReceipt 只读原入队时间与已确认摘要，找不到旧元数据时返回未知。
func (db *DB) PlanWaitReceipt(ctx context.Context, scope, request string) (*time.Time, string, error) {
	var queued sql.NullInt64
	var hash string
	err := db.conn.QueryRowContext(ctx, `SELECT queued_ns,body_hash FROM plan_wait_receipts WHERE owner_scope=? AND request_id=?`, scope, request).Scan(&queued, &hash)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, "", nil
	}
	if err != nil {
		return nil, "", err
	}
	if !queued.Valid {
		return nil, hash, nil
	}
	at := time.Unix(0, queued.Int64).UTC()
	return &at, hash, nil
}

// ConfirmPlanWaitReceipt 只为仍属原请求的排队事实保存摘要，不更新首次入队时间。
func (db *DB) ConfirmPlanWaitReceipt(ctx context.Context, request PlanWaitingRequest, hash string) error {
	if request.OwnerScope == "" || request.RequestID == "" || len(hash) != 64 {
		return ErrPlanRequestConflict
	}
	tx, err := db.conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var plan, activation string
	var triggered int64
	err = tx.QueryRowContext(ctx, `SELECT plan_id,activation_id,triggered_ns FROM plan_waiting_requests WHERE owner_scope=? AND request_id=?`, request.OwnerScope, request.RequestID).Scan(&plan, &activation, &triggered)
	if err != nil {
		return err
	}
	if plan != request.PlanID || activation != request.ActivationID || triggered != request.TriggeredAt.UnixNano() {
		return ErrPlanRequestConflict
	}
	var old string
	err = tx.QueryRowContext(ctx, `SELECT body_hash FROM plan_wait_receipts WHERE owner_scope=? AND request_id=?`, request.OwnerScope, request.RequestID).Scan(&old)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if old != "" && old != hash {
		return ErrPlanRequestConflict
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO plan_wait_receipts(owner_scope,request_id,body_hash) VALUES(?,?,?) ON CONFLICT(owner_scope,request_id) DO UPDATE SET body_hash=excluded.body_hash`, request.OwnerScope, request.RequestID, hash)
	if err != nil {
		return err
	}
	return tx.Commit()
}
