// 本文件保存 HRPlus M2 稳定等待队列与随机收尾安排，重试和数据库重开不改变原触发顺序或延迟。
package localdb

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// ErrPlanRequestConflict 表示同一请求编号绑定了不同计划事实，不能重新入队改变顺序。
var ErrPlanRequestConflict = errors.New("计划请求编号与原记录不一致")

// PlanWaitingRequest 保存原触发时间与单调序号，账号作用域不含登录凭证。
type PlanWaitingRequest struct {
	Sequence                                           int64
	OwnerScope, RequestID, PlanID, ActivationID, State string
	TriggeredAt                                        time.Time
}

// migrateExecutionPlans 增量建立队列与收尾表；每个字段都有中文说明。
func (db *DB) migrateExecutionPlans() error {
	_, err := db.conn.Exec(`
CREATE TABLE IF NOT EXISTS plan_waiting_requests (
 -- 本地稳定请求序号，重试与重启不重新生成。
 sequence INTEGER PRIMARY KEY AUTOINCREMENT,
 -- 已核对的云端所有者作用域摘要。
 owner_scope TEXT NOT NULL,
 -- 明确请求编号，同一作用域只登记一次。
 request_id TEXT NOT NULL,
 -- 原计划编号。
 plan_id TEXT NOT NULL,
 -- 原启用批次编号。
 activation_id TEXT NOT NULL,
 -- 原定触发时刻的纳秒时间戳，避免文本时间排序错误。
 triggered_ns INTEGER NOT NULL,
 -- 等待、启动准备、运行、取消或已结算状态。
 state TEXT NOT NULL DEFAULT 'waiting' CHECK(state IN ('waiting','starting','running','cancelled','done')),
 UNIQUE(owner_scope,request_id)
);
CREATE INDEX IF NOT EXISTS idx_plan_waiting_fifo ON plan_waiting_requests(owner_scope,state,triggered_ns,sequence);
CREATE TABLE IF NOT EXISTS plan_window_grace (
 -- 已核对的云端所有者作用域摘要。
 owner_scope TEXT NOT NULL,
 -- 原计划编号。
 plan_id TEXT NOT NULL,
 -- 本地执行日期，重新启用不改变同日窗口抽样。
 execution_date TEXT NOT NULL,
 -- 名义开始分钟值。
 start_minute INTEGER NOT NULL,
 -- 名义结束分钟值。
 end_minute INTEGER NOT NULL,
 -- 首次确定的收尾延迟纳秒值。
 grace_ns INTEGER NOT NULL CHECK(grace_ns BETWEEN 180000000000 AND 360000000000),
 PRIMARY KEY(owner_scope,plan_id,execution_date,start_minute,end_minute)
);`)
	return err
}

// EnqueuePlanRequest 原子登记请求并返回原序号；重复请求不更新原时间、批次或当前状态。
func (db *DB) EnqueuePlanRequest(ctx context.Context, request PlanWaitingRequest) (PlanWaitingRequest, error) {
	if request.OwnerScope == "" || request.RequestID == "" || request.PlanID == "" || request.ActivationID == "" || request.TriggeredAt.IsZero() {
		return PlanWaitingRequest{}, fmt.Errorf("计划等待请求缺少必要事实")
	}
	if !time.Unix(0, request.TriggeredAt.UnixNano()).Equal(request.TriggeredAt) {
		return PlanWaitingRequest{}, fmt.Errorf("触发时间超出可保存范围")
	}
	tx, err := db.conn.BeginTx(ctx, nil)
	if err != nil {
		return PlanWaitingRequest{}, err
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, `INSERT INTO plan_waiting_requests(owner_scope,request_id,plan_id,activation_id,triggered_ns) VALUES(?,?,?,?,?) ON CONFLICT(owner_scope,request_id) DO NOTHING`, request.OwnerScope, request.RequestID, request.PlanID, request.ActivationID, request.TriggeredAt.UnixNano())
	if err != nil {
		return PlanWaitingRequest{}, err
	}
	var saved PlanWaitingRequest
	var triggered int64
	err = tx.QueryRowContext(ctx, `SELECT sequence,owner_scope,request_id,plan_id,activation_id,triggered_ns,state FROM plan_waiting_requests WHERE owner_scope=? AND request_id=?`, request.OwnerScope, request.RequestID).Scan(&saved.Sequence, &saved.OwnerScope, &saved.RequestID, &saved.PlanID, &saved.ActivationID, &triggered, &saved.State)
	if err != nil {
		return PlanWaitingRequest{}, err
	}
	if saved.PlanID != request.PlanID || saved.ActivationID != request.ActivationID || triggered != request.TriggeredAt.UnixNano() {
		return PlanWaitingRequest{}, ErrPlanRequestConflict
	}
	saved.TriggeredAt = time.Unix(0, triggered).UTC()
	if err = tx.Commit(); err != nil {
		return PlanWaitingRequest{}, err
	}
	return saved, nil
}

// NextPlanRequest 读取本账号最早的等待请求，不提前宣称已取得本地或云端执行权。
func (db *DB) NextPlanRequest(ctx context.Context, scope string) (PlanWaitingRequest, error) {
	var result PlanWaitingRequest
	var triggered int64
	err := db.conn.QueryRowContext(ctx, `SELECT sequence,owner_scope,request_id,plan_id,activation_id,triggered_ns,state FROM plan_waiting_requests WHERE owner_scope=? AND state='waiting' ORDER BY triggered_ns,sequence LIMIT 1`, scope).Scan(&result.Sequence, &result.OwnerScope, &result.RequestID, &result.PlanID, &result.ActivationID, &triggered, &result.State)
	result.TriggeredAt = time.Unix(0, triggered).UTC()
	return result, err
}

// PlanWindowGrace 首次进入窗口时生成并保存三至六分钟收尾延迟，重复读取和重启不重新抽样。
func (db *DB) PlanWindowGrace(ctx context.Context, scope, planID, date string, start, end int, sample func() time.Duration) (time.Duration, error) {
	if scope == "" || planID == "" || start < 0 || end > 1440 || start >= end || sample == nil {
		return 0, fmt.Errorf("随机收尾窗口配置不正确")
	}
	if _, err := time.Parse("2006-01-02", date); err != nil {
		return 0, err
	}
	tx, err := db.conn.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	var grace int64
	err = tx.QueryRowContext(ctx, `SELECT grace_ns FROM plan_window_grace WHERE owner_scope=? AND plan_id=? AND execution_date=? AND start_minute=? AND end_minute=?`, scope, planID, date, start, end).Scan(&grace)
	if errors.Is(err, sql.ErrNoRows) {
		delay := sample()
		if delay < 3*time.Minute || delay > 6*time.Minute {
			return 0, fmt.Errorf("随机收尾超出三至六分钟")
		}
		grace = int64(delay)
		_, err = tx.ExecContext(ctx, `INSERT INTO plan_window_grace(owner_scope,plan_id,execution_date,start_minute,end_minute,grace_ns) VALUES(?,?,?,?,?,?)`, scope, planID, date, start, end, grace)
	}
	if err != nil {
		return 0, err
	}
	if err = tx.Commit(); err != nil {
		return 0, err
	}
	return time.Duration(grace), nil
}
