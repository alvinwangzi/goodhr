// 本文件保存 HRPlus 执行项原归属日志，不从岗位最新运行或当前页面推断原 TaskRun。
package localdb

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
)

// PlanItemLog 固定原检查点的账号、父运行、执行项和任务归属。
type PlanItemLog struct {
	ID         int64  `json:"id"`
	OwnerScope string `json:"-"`
	PlanRunID  string `json:"plan_run_id"`
	ItemRunID  string `json:"item_run_id"`
	TaskRunID  string `json:"task_run_id"`
	LocalRunID string `json:"local_run_id"`
	PositionID string `json:"position_id"`
	Level      string `json:"level"`
	Message    string `json:"message"`
	CreatedAt  string `json:"created_at"`
}

// migratePlanItemLogs 为原执行项建立独立日志索引，旧岗位日志不猜测迁移。
func (db *DB) migratePlanItemLogs() error {
	_, err := db.conn.Exec(`CREATE TABLE IF NOT EXISTS plan_item_logs (
-- 日志流水编号。
id INTEGER PRIMARY KEY AUTOINCREMENT,
-- 原登录所有者摘要，不包含令牌。
owner_scope TEXT NOT NULL,
-- 原父计划运行编号。
plan_run_id TEXT NOT NULL,
-- 原独立执行项运行编号。
item_run_id TEXT NOT NULL,
-- 原云端 TaskRun 编号。
task_run_id TEXT NOT NULL,
-- 原本地检查点编号。
local_run_id TEXT NOT NULL,
-- 原招聘岗位编号，不作为唯一日志归属。
position_id TEXT NOT NULL,
-- 原日志级别。
level TEXT NOT NULL,
-- 实际日志内容。
message TEXT NOT NULL,
-- 日志实际生成时间。
created_at TEXT NOT NULL);
CREATE INDEX IF NOT EXISTS idx_plan_item_logs_original ON plan_item_logs(owner_scope,plan_run_id,item_run_id,id);`)
	return err
}

// AddPlanItemLog 在同一事务中核对指定原检查点并固定归属，不接受调用方任意传入任务编号。
func (db *DB) AddPlanItemLog(ctx context.Context, ownerScope, localRunID, level, message string) (PlanItemLog, error) {
	if ownerScope == "" || localRunID == "" || strings.TrimSpace(message) == "" {
		return PlanItemLog{}, errors.New("执行项日志缺少原归属或内容")
	}
	if level == "" {
		level = "info"
	}
	tx, err := db.conn.BeginTx(ctx, nil)
	if err != nil {
		return PlanItemLog{}, err
	}
	defer tx.Rollback()
	var raw string
	if err = tx.QueryRowContext(ctx, `SELECT checkpoint FROM action_runs WHERE run_id=?`, localRunID).Scan(&raw); err != nil {
		return PlanItemLog{}, err
	}
	var cp ActionCheckpoint
	if err = json.Unmarshal([]byte(raw), &cp); err != nil {
		return PlanItemLog{}, err
	}
	if cp.OwnerScope != ownerScope || cp.RunID != localRunID || cp.PlanRunID == "" || cp.ItemRunID == "" || cp.CloudRunID == "" || cp.PositionID == "" {
		return PlanItemLog{}, errors.New("日志不属于原执行项检查点")
	}
	var originalPlan, originalItem, originalTask, originalPosition string
	err = tx.QueryRowContext(ctx, `SELECT plan_run_id,item_run_id,task_run_id,position_id FROM plan_item_logs WHERE owner_scope=? AND local_run_id=? ORDER BY id LIMIT 1`, ownerScope, localRunID).Scan(&originalPlan, &originalItem, &originalTask, &originalPosition)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return PlanItemLog{}, err
	}
	if err == nil && (originalPlan != cp.PlanRunID || originalItem != cp.ItemRunID || originalTask != cp.CloudRunID || originalPosition != cp.PositionID) {
		return PlanItemLog{}, errors.New("执行项日志的原归属不能改写")
	}
	item := PlanItemLog{OwnerScope: cp.OwnerScope, PlanRunID: cp.PlanRunID, ItemRunID: cp.ItemRunID, TaskRunID: cp.CloudRunID, LocalRunID: cp.RunID, PositionID: cp.PositionID, Level: level, Message: message, CreatedAt: nowISO()}
	result, err := tx.ExecContext(ctx, `INSERT INTO plan_item_logs(owner_scope,plan_run_id,item_run_id,task_run_id,local_run_id,position_id,level,message,created_at) VALUES(?,?,?,?,?,?,?,?,?)`, item.OwnerScope, item.PlanRunID, item.ItemRunID, item.TaskRunID, item.LocalRunID, item.PositionID, item.Level, item.Message, item.CreatedAt)
	if err != nil {
		return PlanItemLog{}, err
	}
	item.ID, err = result.LastInsertId()
	if err != nil {
		return PlanItemLog{}, err
	}
	return item, tx.Commit()
}

// ListPlanItemLogs 只读取指定所有者的原执行项，支持流水分页，旧回调不混入后来同岗位。
func (db *DB) ListPlanItemLogs(ctx context.Context, ownerScope, planRunID, itemRunID string, before int64, limit int) ([]PlanItemLog, error) {
	if ownerScope == "" || planRunID == "" || itemRunID == "" || before < 0 {
		return nil, errors.New("执行项日志读取缺少原归属")
	}
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	rows, err := db.conn.QueryContext(ctx, `SELECT id,owner_scope,plan_run_id,item_run_id,task_run_id,local_run_id,position_id,level,message,created_at FROM plan_item_logs WHERE owner_scope=? AND plan_run_id=? AND item_run_id=? AND (?=0 OR id<?) ORDER BY id DESC LIMIT ?`, ownerScope, planRunID, itemRunID, before, before, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []PlanItemLog{}
	for rows.Next() {
		var item PlanItemLog
		if err = rows.Scan(&item.ID, &item.OwnerScope, &item.PlanRunID, &item.ItemRunID, &item.TaskRunID, &item.LocalRunID, &item.PositionID, &item.Level, &item.Message, &item.CreatedAt); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}
