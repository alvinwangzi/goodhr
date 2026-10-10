// 本文件保存 HRPlus 原停止确认密文，并将规范计划快照和原确认标记一起提交，不保存明文令牌。
package localdb

import (
	"context"
	"encoding/json"
	"errors"
	"goodhr5/local-agent-go/internal/planmodel"
)

// PlanStopOperation 保存原停止请求路由与密文，字段不含执行权凭证。
type PlanStopOperation struct {
	Sequence, Version                                                       int64
	OwnerScope, RequestID, PlanID, ActivationID, MachineID, BodyHash, State string
	Cipher                                                                  []byte
}

// migratePlanStops 建立原停止确认队列，每个数据库字段都有中文说明。
func (db *DB) migratePlanStops() error {
	_, err := db.conn.Exec(`CREATE TABLE IF NOT EXISTS plan_stop_operations (
 -- 原停止确认落盘顺序。
 sequence INTEGER PRIMARY KEY AUTOINCREMENT,
 -- 已核对账号作用域。
 owner_scope TEXT NOT NULL,
 -- 原幂等请求编号。
 request_id TEXT NOT NULL,
 -- 原计划编号。
 plan_id TEXT NOT NULL,
 -- 原启用批次编号。
 activation_id TEXT NOT NULL,
 -- 原配置版本。
 config_version INTEGER NOT NULL,
 -- 指定执行电脑编号。
 machine_id TEXT NOT NULL,
 -- 原内容摘要。
 body_hash TEXT NOT NULL,
 -- Windows 当前用户加密的原停止确认。
 cipher BLOB NOT NULL,
 -- 原回执是否已确认。
 state TEXT NOT NULL DEFAULT 'pending' CHECK(state IN ('pending','confirmed')),
 UNIQUE(owner_scope,request_id)
);`)
	return err
}

const planStopColumns = `sequence,owner_scope,request_id,plan_id,activation_id,config_version,machine_id,body_hash,cipher,state`

// scanPlanStop 复用标准 Scan 接口读取原路由，不解密凭证或输出密文。
func scanPlanStop(row interface{ Scan(...any) error }) (PlanStopOperation, error) {
	var o PlanStopOperation
	err := row.Scan(&o.Sequence, &o.OwnerScope, &o.RequestID, &o.PlanID, &o.ActivationID, &o.Version, &o.MachineID, &o.BodyHash, &o.Cipher, &o.State)
	return o, err
}

// PlanStopOperation 读取指定账号的原确认记录，不重新生成请求。
func (db *DB) PlanStopOperation(ctx context.Context, scope, id string) (PlanStopOperation, error) {
	return scanPlanStop(db.conn.QueryRowContext(ctx, `SELECT `+planStopColumns+` FROM plan_stop_operations WHERE owner_scope=? AND request_id=?`, scope, id))
}

// SavePlanStop 保存已加密原确认，同一编号内容变化拒绝覆盖。
func (db *DB) SavePlanStop(ctx context.Context, o PlanStopOperation) (PlanStopOperation, error) {
	if o.OwnerScope == "" || !planmodel.ValidID(o.RequestID) || !planmodel.ValidID(o.PlanID) || !planmodel.ValidID(o.ActivationID) || o.Version < 1 || o.MachineID == "" || len(o.BodyHash) != 64 || len(o.Cipher) == 0 {
		return PlanStopOperation{}, ErrPlanRequestConflict
	}
	tx, err := db.conn.BeginTx(ctx, nil)
	if err != nil {
		return PlanStopOperation{}, err
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, `INSERT INTO plan_stop_operations(owner_scope,request_id,plan_id,activation_id,config_version,machine_id,body_hash,cipher) VALUES(?,?,?,?,?,?,?,?) ON CONFLICT(owner_scope,request_id) DO NOTHING`, o.OwnerScope, o.RequestID, o.PlanID, o.ActivationID, o.Version, o.MachineID, o.BodyHash, o.Cipher)
	if err != nil {
		return PlanStopOperation{}, err
	}
	old, err := scanPlanStop(tx.QueryRowContext(ctx, `SELECT `+planStopColumns+` FROM plan_stop_operations WHERE owner_scope=? AND request_id=?`, o.OwnerScope, o.RequestID))
	if err != nil {
		return old, err
	}
	if old.PlanID != o.PlanID || old.ActivationID != o.ActivationID || old.Version != o.Version || old.MachineID != o.MachineID || old.BodyHash != o.BodyHash {
		return PlanStopOperation{}, ErrPlanRequestConflict
	}
	return old, tx.Commit()
}

// NextPlanStop 仅查询指定账号的原停止确认，不重新领取页面执行权。
func (db *DB) NextPlanStop(ctx context.Context, scope string) (PlanStopOperation, error) {
	return scanPlanStop(db.conn.QueryRowContext(ctx, `SELECT `+planStopColumns+` FROM plan_stop_operations WHERE owner_scope=? AND state='pending' ORDER BY sequence LIMIT 1`, scope))
}

// PlanStopReady 不把仍活动或未确认释放的本地运行视为已收尾；云端还须核对真实账号占用。
func (db *DB) PlanStopReady(ctx context.Context, scope, planID string) (bool, error) {
	var count int
	err := db.conn.QueryRowContext(ctx, `SELECT COUNT(*) FROM plan_run_snapshots WHERE owner_scope=? AND plan_id=? AND json_extract(snapshot_json,'$.state') IN ('starting','running','draining')`, scope, planID).Scan(&count)
	if err != nil || count != 0 {
		return false, err
	}
	err = db.conn.QueryRowContext(ctx, `SELECT COUNT(*) FROM plan_operations WHERE owner_scope=? AND plan_id=? AND state='pending' AND kind IN ('status','release')`, scope, planID).Scan(&count)
	return count == 0, err
}

// ConfirmPlanStopSnapshot 原子确认快照及原请求；合法旧回执不覆盖更新计划。
func (db *DB) ConfirmPlanStopSnapshot(ctx context.Context, o PlanStopOperation, p planmodel.Plan) error {
	if err := p.Validate(); err != nil {
		return err
	}
	if p.ID != o.PlanID || p.Version != o.Version || p.ActivationID != o.ActivationID || p.MachineID != o.MachineID || p.StopRequested || p.State != "stopped" {
		return ErrPlanRequestConflict
	}
	p.CreatedAt = p.CreatedAt.UTC()
	p.UpdatedAt = p.UpdatedAt.UTC()
	raw, err := json.Marshal(p)
	if err != nil {
		return err
	}
	tx, err := db.conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = saveCachedPlanTx(ctx, tx, o.OwnerScope, p, raw); err != nil && !errors.Is(err, ErrPlanSnapshotStale) {
		return err
	}
	result, err := tx.ExecContext(ctx, `UPDATE plan_stop_operations SET state='confirmed' WHERE owner_scope=? AND request_id=? AND body_hash=?`, o.OwnerScope, o.RequestID, o.BodyHash)
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
	if _, err = tx.ExecContext(ctx, `UPDATE plan_waiting_requests SET state='cancelled' WHERE owner_scope=? AND plan_id=? AND activation_id=? AND state='waiting'`, o.OwnerScope, o.PlanID, o.ActivationID); err != nil {
		return err
	}
	return tx.Commit()
}
