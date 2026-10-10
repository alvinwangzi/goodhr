// 本文件把 HRPlus 原计划回执与安全运行快照放在同一 SQLite 事务，迟到回执不覆盖较新进度。
package localdb

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"goodhr5/local-agent-go/internal/planmodel"
)

// ConfirmPlanOperationSnapshot 核对原请求身份和摘要后原子保存回执；已有较新运行时仅确认旧回执。
func (db *DB) ConfirmPlanOperationSnapshot(ctx context.Context, scope, requestID, hash string, run planmodel.Run, dispatch ...[]byte) error {
	if err := run.Validate(); err != nil {
		return err
	}
	tx, err := db.conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	op, err := scanPlanOperation(tx.QueryRowContext(ctx, `SELECT `+planOperationColumns+` FROM plan_operations WHERE owner_scope=? AND request_id=?`, scope, requestID))
	if err != nil {
		return err
	}
	if op.BodyHash != hash || op.PlanID != run.PlanID || op.RunID != run.ID || op.OwnerID != run.OwnerID || ((op.Kind == "status" || op.Kind == "release") && op.RunSequence != run.Sequence) {
		return ErrPlanRequestConflict
	}
	var raw string
	err = tx.QueryRowContext(ctx, `SELECT snapshot_json FROM plan_run_snapshots WHERE owner_scope=? AND run_id=?`, scope, run.ID).Scan(&raw)
	newer := false
	if err == nil {
		var current planmodel.Run
		if err = json.Unmarshal([]byte(raw), &current); err != nil {
			return err
		}
		if err = current.Validate(); err != nil {
			return err
		}
		newer = current.Sequence > run.Sequence
		if newer {
			if err = validatePlanSnapshotAdvance(run, current); err != nil {
				return err
			}
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if !newer {
		if err = savePlanRunSnapshotTx(ctx, tx, scope, run); err != nil {
			return err
		}
		if len(dispatch) > 1 {
			return ErrPlanRequestConflict
		}
		if len(dispatch) == 1 && len(dispatch[0]) > 0 {
			if op.Kind != "status" {
				return ErrPlanRequestConflict
			}
			if err = saveConfirmedDispatchTx(ctx, tx, scope, run, dispatch[0]); err != nil {
				return err
			}
		}
	}
	result, err := tx.ExecContext(ctx, `UPDATE plan_operations SET state='confirmed' WHERE owner_scope=? AND request_id=? AND body_hash=?`, scope, requestID, hash)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return ErrPlanRequestConflict
	}
	return tx.Commit()
}
