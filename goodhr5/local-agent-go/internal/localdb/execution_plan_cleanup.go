// 本文件持久保存 HRPlus 已完成页面清理的原结算意图，未知云端序号不拼成释放请求。
package localdb

import "context"

// PlanCleanupOperation 保存原占用的清理意图路由，正文由 Windows 加密保护。
type PlanCleanupOperation struct {
	Sequence                                                       int64
	OwnerScope, RequestID, PlanID, RunID, OwnerID, BodyHash, State string
	Cipher                                                         []byte
}

// migratePlanCleanup 建立持久清理意图表，所有数据库字段均注明中文含义。
func (db *DB) migratePlanCleanup() error {
	_, err := db.conn.Exec(`CREATE TABLE IF NOT EXISTS plan_cleanup_operations (
 -- 本地原意图顺序。
 sequence INTEGER PRIMARY KEY AUTOINCREMENT,
 -- 原账号作用域。
 owner_scope TEXT NOT NULL,
 -- 原清理意图幂等编号。
 request_id TEXT NOT NULL,
 -- 原计划编号。
 plan_id TEXT NOT NULL,
 -- 原运行编号。
 run_id TEXT NOT NULL,
 -- 原占用编号。
 owner_id TEXT NOT NULL,
 -- 原内容摘要。
 body_hash TEXT NOT NULL,
 -- Windows 当前用户加密的清理事实及实际进度。
 cipher BLOB NOT NULL,
 -- 原释放是否得到云端确认。
 state TEXT NOT NULL DEFAULT 'pending' CHECK(state IN ('pending','confirmed')),
 UNIQUE(owner_scope,request_id)
);`)
	return err
}

const cleanupColumns = `sequence,owner_scope,request_id,plan_id,run_id,owner_id,body_hash,cipher,state`

// scanPlanCleanup 读取原路由，不解密或输出正文。
func scanPlanCleanup(row interface{ Scan(...any) error }) (PlanCleanupOperation, error) {
	var o PlanCleanupOperation
	err := row.Scan(&o.Sequence, &o.OwnerScope, &o.RequestID, &o.PlanID, &o.RunID, &o.OwnerID, &o.BodyHash, &o.Cipher, &o.State)
	return o, err
}

// PlanCleanupOperation 按原账号与编号读取已冻结的事实。
func (db *DB) PlanCleanupOperation(ctx context.Context, scope, id string) (PlanCleanupOperation, error) {
	return scanPlanCleanup(db.conn.QueryRowContext(ctx, `SELECT `+cleanupColumns+` FROM plan_cleanup_operations WHERE owner_scope=? AND request_id=?`, scope, id))
}

// SavePlanCleanup 保存原密文，重复意图不能改写事实或落盘顺序。
func (db *DB) SavePlanCleanup(ctx context.Context, o PlanCleanupOperation) (PlanCleanupOperation, error) {
	if o.OwnerScope == "" || o.RequestID == "" || o.PlanID == "" || o.RunID == "" || o.OwnerID == "" || len(o.BodyHash) != 64 || len(o.Cipher) == 0 {
		return PlanCleanupOperation{}, ErrPlanRequestConflict
	}
	tx, err := db.conn.BeginTx(ctx, nil)
	if err != nil {
		return PlanCleanupOperation{}, err
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, `INSERT INTO plan_cleanup_operations(owner_scope,request_id,plan_id,run_id,owner_id,body_hash,cipher) VALUES(?,?,?,?,?,?,?) ON CONFLICT(owner_scope,request_id) DO NOTHING`, o.OwnerScope, o.RequestID, o.PlanID, o.RunID, o.OwnerID, o.BodyHash, o.Cipher)
	if err != nil {
		return PlanCleanupOperation{}, err
	}
	old, err := scanPlanCleanup(tx.QueryRowContext(ctx, `SELECT `+cleanupColumns+` FROM plan_cleanup_operations WHERE owner_scope=? AND request_id=?`, o.OwnerScope, o.RequestID))
	if err != nil {
		return old, err
	}
	if old.PlanID != o.PlanID || old.RunID != o.RunID || old.OwnerID != o.OwnerID || old.BodyHash != o.BodyHash {
		return PlanCleanupOperation{}, ErrPlanRequestConflict
	}
	return old, tx.Commit()
}

// NextPlanCleanup 读取原账号最早待结算意图，不重新领取执行权。
func (db *DB) NextPlanCleanup(ctx context.Context, scope string) (PlanCleanupOperation, error) {
	return scanPlanCleanup(db.conn.QueryRowContext(ctx, `SELECT `+cleanupColumns+` FROM plan_cleanup_operations WHERE owner_scope=? AND state='pending' ORDER BY sequence LIMIT 1`, scope))
}

// ConfirmPlanCleanup 仅在原释放已确认后完成意图，不以本地清理代替云端回执。
func (db *DB) ConfirmPlanCleanup(ctx context.Context, o PlanCleanupOperation, releaseID string) error {
	result, err := db.conn.ExecContext(ctx, `UPDATE plan_cleanup_operations SET state='confirmed' WHERE owner_scope=? AND request_id=? AND body_hash=? AND EXISTS(SELECT 1 FROM plan_operations WHERE owner_scope=? AND request_id=? AND run_id=? AND owner_id=? AND kind='release' AND state='confirmed')`, o.OwnerScope, o.RequestID, o.BodyHash, o.OwnerScope, releaseID, o.RunID, o.OwnerID)
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
