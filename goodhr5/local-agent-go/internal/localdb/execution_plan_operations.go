// 本文件保存 HRPlus 执行请求的密文与稳定顺序；普通元数据不保存明文凭证或登录令牌。
package localdb

import (
	"context"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"goodhr5/local-agent-go/internal/planmodel"
)

// PlanOperation 保存已加密的不可变原请求，Sequence 为原落盘顺序，RunSequence 为云端状态序号。
type PlanOperation struct {
	Sequence, RunSequence                                                int64
	OwnerScope, RequestID, PlanID, RunID, OwnerID, Kind, BodyHash, State string
	Cipher                                                               []byte
}

// migratePlanOperations 建立持久原请求表，每个字段均有中文说明。
func (db *DB) migratePlanOperations() error {
	_, err := db.conn.Exec(`CREATE TABLE IF NOT EXISTS plan_operations (
 -- 原请求本地落盘顺序，重试不重新排序。
 sequence INTEGER PRIMARY KEY AUTOINCREMENT,
 -- 已核对的云端所有者作用域。
 owner_scope TEXT NOT NULL,
 -- 原请求幂等编号。
 request_id TEXT NOT NULL,
 -- 原计划编号。
 plan_id TEXT NOT NULL,
 -- 原运行编号。
 run_id TEXT NOT NULL,
 -- 原账号占用编号。
 owner_id TEXT NOT NULL,
 -- 领取、状态或释放操作。
 kind TEXT NOT NULL CHECK(kind IN ('claim','status','release')),
 -- 状态操作的原递增序号，领取为零。
 run_sequence INTEGER NOT NULL,
 -- 原内容摘要，不含凭证原文。
 body_hash TEXT NOT NULL,
 -- Windows 当前用户加密的完整原请求。
 cipher BLOB NOT NULL,
 -- 待核对或已获原回执确认。
 state TEXT NOT NULL DEFAULT 'pending' CHECK(state IN ('pending','confirmed')),
 UNIQUE(owner_scope,request_id)
);
CREATE INDEX IF NOT EXISTS idx_plan_operation_pending ON plan_operations(owner_scope,state,sequence);`)
	return err
}

const planOperationColumns = `sequence,owner_scope,request_id,plan_id,run_id,owner_id,kind,run_sequence,body_hash,cipher,state`

// scanPlanOperation 统一读取元数据与密文，不解密或输出请求凭证。
func scanPlanOperation(row interface{ Scan(...any) error }) (PlanOperation, error) {
	var o PlanOperation
	err := row.Scan(&o.Sequence, &o.OwnerScope, &o.RequestID, &o.PlanID, &o.RunID, &o.OwnerID, &o.Kind, &o.RunSequence, &o.BodyHash, &o.Cipher, &o.State)
	return o, err
}

// SavePlanOperation 原子保存密文原请求，重试保留原密文、确认状态与落盘顺序。
func (db *DB) SavePlanOperation(ctx context.Context, o PlanOperation) (PlanOperation, error) {
	hash, err := hex.DecodeString(o.BodyHash)
	if err != nil || len(hash) != 32 || o.OwnerScope == "" || !planmodel.ValidID(o.RequestID) || !planmodel.ValidID(o.PlanID) || !planmodel.ValidID(o.RunID) || !planmodel.ValidID(o.OwnerID) || len(o.Cipher) == 0 || (o.Kind != "claim" && o.Kind != "status" && o.Kind != "release") || (o.Kind == "claim" && o.RunSequence != 0) || (o.Kind != "claim" && o.RunSequence < 1) {
		return PlanOperation{}, fmt.Errorf("原执行请求元数据不完整")
	}
	tx, err := db.conn.BeginTx(ctx, nil)
	if err != nil {
		return PlanOperation{}, err
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, `INSERT INTO plan_operations(owner_scope,request_id,plan_id,run_id,owner_id,kind,run_sequence,body_hash,cipher) VALUES(?,?,?,?,?,?,?,?,?) ON CONFLICT(owner_scope,request_id) DO NOTHING`, o.OwnerScope, o.RequestID, o.PlanID, o.RunID, o.OwnerID, o.Kind, o.RunSequence, o.BodyHash, o.Cipher)
	if err != nil {
		return PlanOperation{}, err
	}
	saved, err := scanPlanOperation(tx.QueryRowContext(ctx, `SELECT `+planOperationColumns+` FROM plan_operations WHERE owner_scope=? AND request_id=?`, o.OwnerScope, o.RequestID))
	if err != nil {
		return saved, err
	}
	if saved.PlanID != o.PlanID || saved.RunID != o.RunID || saved.OwnerID != o.OwnerID || saved.Kind != o.Kind || saved.RunSequence != o.RunSequence || saved.BodyHash != o.BodyHash {
		return PlanOperation{}, ErrPlanRequestConflict
	}
	if err = tx.Commit(); err != nil {
		return PlanOperation{}, err
	}
	return saved, nil
}

// PlanOperation 读取指定账号的原请求，恢复时不重新生成编号或替换内容。
func (db *DB) PlanOperation(ctx context.Context, scope, request string) (PlanOperation, error) {
	return scanPlanOperation(db.conn.QueryRowContext(ctx, `SELECT `+planOperationColumns+` FROM plan_operations WHERE owner_scope=? AND request_id=?`, scope, request))
}

// NextPlanUpdate 仅查找状态和释放，绝不在后台补传线程重新领取页面执行权。
func (db *DB) NextPlanUpdate(ctx context.Context, scope string) (PlanOperation, error) {
	return scanPlanOperation(db.conn.QueryRowContext(ctx, `SELECT `+planOperationColumns+` FROM plan_operations WHERE owner_scope=? AND state='pending' AND kind IN ('status','release') ORDER BY sequence LIMIT 1`, scope))
}

// ConfirmPlanOperation 仅用已核对的原请求摘要确认，不会确认同编号的另一份内容。
func (db *DB) ConfirmPlanOperation(ctx context.Context, scope, request, hash string) error {
	result, err := db.conn.ExecContext(ctx, `UPDATE plan_operations SET state='confirmed' WHERE owner_scope=? AND request_id=? AND body_hash=?`, scope, request, hash)
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

// HasPlanUpdate 用于启动补传服务的只读检查，无记录不代表计划任务已经结束。
func (db *DB) HasPlanUpdate(ctx context.Context, scope string) (bool, error) {
	_, err := db.NextPlanUpdate(ctx, scope)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}
