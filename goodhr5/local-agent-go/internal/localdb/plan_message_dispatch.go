// 本文件保存 HRPlus 父计划消息轮换安全快照，只关联已确认规范运行，不授予执行权。
package localdb

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"goodhr5/local-agent-go/internal/planmodel"
	"reflect"
)

// migratePlanMessageDispatch 创建账号及原运行隔离的消息轮换记录，字段均有中文说明。
func (db *DB) migratePlanMessageDispatch() error {
	_, err := db.conn.Exec(`CREATE TABLE IF NOT EXISTS plan_message_dispatch (
-- 原已验证账号摘要。
owner_scope TEXT NOT NULL,
-- 原当天父运行编号。
run_id TEXT NOT NULL,
-- 保存时规范运行序号。
run_sequence INTEGER NOT NULL,
-- 不包含凭证或页面对象的消息轮换快照。
snapshot_json TEXT NOT NULL,
PRIMARY KEY(owner_scope,run_id));`)
	return err
}

// SavePlanMessageDispatch 在同一事务核对已确认运行序号并保存轮换，旧序号不能覆盖后来进度。
func (db *DB) SavePlanMessageDispatch(ctx context.Context, scope, runID string, sequence int64, raw []byte) error {
	if scope == "" || runID == "" || sequence < 1 || !json.Valid(raw) {
		return ErrPlanRequestConflict
	}
	var value struct {
		Schema     int
		OwnerScope string
		Run        planmodel.Run
	}
	if json.Unmarshal(raw, &value) != nil || value.Schema != 1 || value.OwnerScope != scope || value.Run.ID != runID || value.Run.Sequence != sequence {
		return ErrPlanRequestConflict
	}
	if err := value.Run.Validate(); err != nil {
		return err
	}
	if value.Run.StartedAt != nil {
		normalized := value.Run.StartedAt.UTC()
		value.Run.StartedAt = &normalized
	}
	if value.Run.FinishedAt != nil {
		normalized := value.Run.FinishedAt.UTC()
		value.Run.FinishedAt = &normalized
	}
	tx, err := db.conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var current int64
	var canonicalRaw string
	if err = tx.QueryRowContext(ctx, `SELECT state_sequence,snapshot_json FROM plan_run_snapshots WHERE owner_scope=? AND run_id=?`, scope, runID).Scan(&current, &canonicalRaw); err != nil {
		return err
	}
	if current != sequence {
		return ErrPlanSnapshotStale
	}
	var canonical planmodel.Run
	if json.Unmarshal([]byte(canonicalRaw), &canonical) != nil || !reflect.DeepEqual(canonical, value.Run) {
		return ErrPlanRequestConflict
	}
	var oldSequence int64
	var oldRaw string
	err = tx.QueryRowContext(ctx, `SELECT run_sequence,snapshot_json FROM plan_message_dispatch WHERE owner_scope=? AND run_id=?`, scope, runID).Scan(&oldSequence, &oldRaw)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err == nil && oldSequence == sequence {
		if oldRaw != string(raw) {
			return ErrPlanRequestConflict
		}
		return tx.Commit()
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO plan_message_dispatch(owner_scope,run_id,run_sequence,snapshot_json) VALUES(?,?,?,?) ON CONFLICT(owner_scope,run_id) DO UPDATE SET run_sequence=excluded.run_sequence,snapshot_json=excluded.snapshot_json WHERE plan_message_dispatch.run_sequence<=excluded.run_sequence`, scope, runID, sequence, string(raw))
	if err != nil {
		return err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if changed != 1 {
		return ErrPlanSnapshotStale
	}
	return tx.Commit()
}

// PlanMessageDispatch 读取当前账号原快照，尚未保存时由调用方重建服务并重新核对当前工作。
func (db *DB) PlanMessageDispatch(ctx context.Context, scope, runID string) ([]byte, error) {
	var raw string
	err := db.conn.QueryRowContext(ctx, `SELECT snapshot_json FROM plan_message_dispatch WHERE owner_scope=? AND run_id=?`, scope, runID).Scan(&raw)
	return []byte(raw), err
}
