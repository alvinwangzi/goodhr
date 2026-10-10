// 本文件保存 HRPlus 已核对的计划和运行快照；普通 SQLite 不保存登录令牌或占用凭证。
package localdb

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"goodhr5/local-agent-go/internal/planmodel"
	"reflect"
	"time"
)

var ErrPlanSnapshotStale = errors.New("计划快照序号已过期，保留当前进度")

// migratePlanSnapshots 增量建立账号隔离的安全快照表，字段均使用中文说明。
func (db *DB) migratePlanSnapshots() error {
	_, err := db.conn.Exec(`CREATE TABLE IF NOT EXISTS plan_cache (
 -- 已确认的云端所有者作用域，不含登录令牌。
 owner_scope TEXT NOT NULL,
 -- 原计划编号。
 plan_id TEXT NOT NULL,
 -- 原配置版本。
 config_version INTEGER NOT NULL,
 -- 云端递增计划状态序号。
 state_sequence INTEGER NOT NULL,
 -- 不含凭证的完整类型化编排快照。
 plan_json TEXT NOT NULL,
 -- 本次保存时间，不作为执行许可。
 saved_at TEXT NOT NULL,
 PRIMARY KEY(owner_scope,plan_id)
);
CREATE TABLE IF NOT EXISTS plan_run_snapshots (
 -- 已确认的云端所有者作用域，不含登录令牌。
 owner_scope TEXT NOT NULL,
 -- 原当天运行编号，不在重试时重新生成。
 run_id TEXT NOT NULL,
 -- 原计划编号。
 plan_id TEXT NOT NULL,
 -- 原启用批次编号。
 activation_id TEXT NOT NULL,
 -- 原计划时区中的执行日期。
 execution_date TEXT NOT NULL,
 -- 不可变配置版本。
 config_version INTEGER NOT NULL,
 -- 已保存的递增运行序号。
 state_sequence INTEGER NOT NULL,
 -- 原独立执行项与安全动作进度，不含占用凭证。
 snapshot_json TEXT NOT NULL,
 -- 本次保存时间，不作为页面收尾证明。
 saved_at TEXT NOT NULL,
 PRIMARY KEY(owner_scope,run_id),
 UNIQUE(owner_scope,plan_id,activation_id,execution_date)
);`)
	return err
}

// SaveCachedPlan 只接受完整快照，旧序号不能覆盖停止意图或新版配置。
func (db *DB) SaveCachedPlan(ctx context.Context, scope string, p planmodel.Plan) error {
	if scope == "" {
		return ErrPlanRequestConflict
	}
	if err := p.Validate(); err != nil {
		return err
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
	if err := saveCachedPlanTx(ctx, tx, scope, p, raw); err != nil {
		return err
	}
	return tx.Commit()
}

// saveCachedPlanTx 在调用方事务内保存已验证快照，供原回执和确认标记原子提交。
func saveCachedPlanTx(ctx context.Context, tx *sql.Tx, scope string, p planmodel.Plan, raw []byte) error {
	var oldVersion, oldSequence int64
	var oldRaw string
	err := tx.QueryRowContext(ctx, `SELECT config_version,state_sequence,plan_json FROM plan_cache WHERE owner_scope=? AND plan_id=?`, scope, p.ID).Scan(&oldVersion, &oldSequence, &oldRaw)
	if err == nil {
		var old planmodel.Plan
		if err = json.Unmarshal([]byte(oldRaw), &old); err != nil {
			return err
		}
		if old.UserEmail != p.UserEmail || old.TenantID != p.TenantID || !old.CreatedAt.Equal(p.CreatedAt) || (oldVersion == p.Version && (!reflect.DeepEqual(old.Config, p.Config) || old.MachineID != p.MachineID)) {
			return ErrPlanRequestConflict
		}
		if p.Version < oldVersion || p.StateSequence < oldSequence {
			return ErrPlanSnapshotStale
		}
		if p.StateSequence == oldSequence {
			if oldRaw != string(raw) {
				return ErrPlanRequestConflict
			}
			return nil
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO plan_cache(owner_scope,plan_id,config_version,state_sequence,plan_json,saved_at) VALUES(?,?,?,?,?,?) ON CONFLICT(owner_scope,plan_id) DO UPDATE SET config_version=excluded.config_version,state_sequence=excluded.state_sequence,plan_json=excluded.plan_json,saved_at=excluded.saved_at`, scope, p.ID, p.Version, p.StateSequence, string(raw), time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return err
	}
	return nil
}

// CachedPlans 读取指定账号的缓存，不通过缓存状态自行给予启动许可。
func (db *DB) CachedPlans(ctx context.Context, scope string) ([]planmodel.Plan, error) {
	rows, err := db.conn.QueryContext(ctx, `SELECT plan_json FROM plan_cache WHERE owner_scope=? ORDER BY plan_id`, scope)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []planmodel.Plan{}
	for rows.Next() {
		var raw string
		if err = rows.Scan(&raw); err != nil {
			return nil, err
		}
		var p planmodel.Plan
		if err = json.Unmarshal([]byte(raw), &p); err != nil {
			return nil, err
		}
		if err = p.Validate(); err != nil {
			return nil, err
		}
		result = append(result, p)
	}
	return result, rows.Err()
}

// PlanRunForDate 读取原计划批次在指定日期的规范运行，用于反馈开始状态，不创建新执行权。
func (db *DB) PlanRunForDate(ctx context.Context, scope, planID, activation, date string) (planmodel.Run, error) {
	var raw string
	err := db.conn.QueryRowContext(ctx, `SELECT snapshot_json FROM plan_run_snapshots WHERE owner_scope=? AND plan_id=? AND activation_id=? AND execution_date=?`, scope, planID, activation, date).Scan(&raw)
	if err != nil {
		return planmodel.Run{}, err
	}
	var run planmodel.Run
	if err = json.Unmarshal([]byte(raw), &run); err != nil {
		return run, err
	}
	if err = run.Validate(); err != nil {
		return run, err
	}
	if run.PlanID != planID || run.ActivationID != activation || run.ExecutionDate != date {
		return planmodel.Run{}, ErrPlanRequestConflict
	}
	return run, nil
}

// validatePlanSnapshotAdvance 防止重启或迟到回执替换原日期、编排身份或减少已确认数量。
func validatePlanSnapshotAdvance(old, next planmodel.Run) error {
	if old.PlanID != next.PlanID || old.ActivationID != next.ActivationID || old.ExecutionDate != next.ExecutionDate || old.ConfigVersion != next.ConfigVersion || !reflect.DeepEqual(old.Snapshot, next.Snapshot) {
		return ErrPlanRequestConflict
	}
	if next.Sequence < old.Sequence || next.CurrentItem < old.CurrentItem {
		return ErrPlanSnapshotStale
	}
	if old.StartedAt != nil && (next.StartedAt == nil || !old.StartedAt.Equal(*next.StartedAt)) {
		return ErrPlanRequestConflict
	}
	if old.FinishedAt != nil && (next.FinishedAt == nil || !old.FinishedAt.Equal(*next.FinishedAt)) {
		return ErrPlanRequestConflict
	}
	switch old.State {
	case "completed", "incomplete", "stopped", "blocked":
		if next.State != old.State {
			return ErrPlanSnapshotStale
		}
	}
	for index, item := range old.Items {
		current := next.Items[index]
		if current.ID != item.ID || current.ItemID != item.ItemID || (item.TaskRunID != "" && current.TaskRunID != item.TaskRunID) {
			return ErrPlanRequestConflict
		}
		for action, progress := range item.Actions {
			updated := current.Actions[action]
			if updated.Count < progress.Count || updated.UnknownCount < progress.UnknownCount {
				return ErrPlanSnapshotStale
			}
		}
	}
	return nil
}

// SavePlanRunSnapshot 按原运行编号保存完整安全进度，重复回执不覆盖新计数。
func (db *DB) SavePlanRunSnapshot(ctx context.Context, scope string, r planmodel.Run) error {
	tx, err := db.conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = savePlanRunSnapshotTx(ctx, tx, scope, r); err != nil {
		return err
	}
	return tx.Commit()
}

// savePlanRunSnapshotTx 复用安全快照校验，由独立保存或原请求回执确认事务控制提交。
func savePlanRunSnapshotTx(ctx context.Context, tx *sql.Tx, scope string, r planmodel.Run) error {
	if scope == "" {
		return ErrPlanRequestConflict
	}
	if err := r.Validate(); err != nil {
		return err
	}
	if r.StartedAt != nil {
		normalized := r.StartedAt.UTC()
		r.StartedAt = &normalized
	}
	if r.FinishedAt != nil {
		normalized := r.FinishedAt.UTC()
		r.FinishedAt = &normalized
	}
	raw, err := json.Marshal(r)
	if err != nil {
		return err
	}
	var oldRaw string
	err = tx.QueryRowContext(ctx, `SELECT snapshot_json FROM plan_run_snapshots WHERE owner_scope=? AND run_id=?`, scope, r.ID).Scan(&oldRaw)
	if err == nil {
		var old planmodel.Run
		if err = json.Unmarshal([]byte(oldRaw), &old); err != nil {
			return err
		}
		if err = old.Validate(); err != nil {
			return err
		}
		if err = validatePlanSnapshotAdvance(old, r); err != nil {
			return err
		}
		if old.Sequence == r.Sequence {
			if oldRaw != string(raw) {
				return ErrPlanRequestConflict
			}
			return nil
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	var existingID string
	err = tx.QueryRowContext(ctx, `SELECT run_id FROM plan_run_snapshots WHERE owner_scope=? AND plan_id=? AND activation_id=? AND execution_date=?`, scope, r.PlanID, r.ActivationID, r.ExecutionDate).Scan(&existingID)
	if err == nil && existingID != r.ID {
		return ErrPlanRequestConflict
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO plan_run_snapshots(owner_scope,run_id,plan_id,activation_id,execution_date,config_version,state_sequence,snapshot_json,saved_at) VALUES(?,?,?,?,?,?,?,?,?) ON CONFLICT(owner_scope,run_id) DO UPDATE SET state_sequence=excluded.state_sequence,snapshot_json=excluded.snapshot_json,saved_at=excluded.saved_at`, scope, r.ID, r.PlanID, r.ActivationID, r.ExecutionDate, r.ConfigVersion, r.Sequence, string(raw), time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return err
	}
	return nil
}

// PlanRunSnapshot 读取指定账号的完整原运行，结构损坏时不自动重建或重置进度。
func (db *DB) PlanRunSnapshot(ctx context.Context, scope, id string) (planmodel.Run, error) {
	var raw string
	var r planmodel.Run
	err := db.conn.QueryRowContext(ctx, `SELECT snapshot_json FROM plan_run_snapshots WHERE owner_scope=? AND run_id=?`, scope, id).Scan(&raw)
	if err != nil {
		return r, err
	}
	if err = json.Unmarshal([]byte(raw), &r); err != nil {
		return r, err
	}
	if err = r.Validate(); err != nil {
		return planmodel.Run{}, err
	}
	return r, nil
}

// PlanRunForDay 按原批次与执行日找回已保存编号，重启不依赖网页或重新生成当天运行。
func (db *DB) PlanRunForDay(ctx context.Context, scope, planID, activation, date string) (planmodel.Run, error) {
	var id string
	if err := db.conn.QueryRowContext(ctx, `SELECT run_id FROM plan_run_snapshots WHERE owner_scope=? AND plan_id=? AND activation_id=? AND execution_date=?`, scope, planID, activation, date).Scan(&id); err != nil {
		return planmodel.Run{}, err
	}
	return db.PlanRunSnapshot(ctx, scope, id)
}
