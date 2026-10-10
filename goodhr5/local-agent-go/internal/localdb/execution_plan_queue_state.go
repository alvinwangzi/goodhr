// 本文件读取 HRPlus 持久等待队列并按原状态更新，保留原触发顺序，不授予页面执行权。
package localdb

import (
	"context"
	"fmt"
	"time"
)

// WaitingPlanRequests 按原触发时间与稳定序号读取全部等待项，暂不在时段内的计划不会阻挡其他可执行项。
func (db *DB) WaitingPlanRequests(ctx context.Context, scope string) ([]PlanWaitingRequest, error) {
	rows, err := db.conn.QueryContext(ctx, `SELECT sequence,owner_scope,request_id,plan_id,activation_id,triggered_ns,state FROM plan_waiting_requests WHERE owner_scope=? AND state='waiting' ORDER BY triggered_ns,sequence`, scope)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []PlanWaitingRequest{}
	for rows.Next() {
		var item PlanWaitingRequest
		var triggered int64
		if err := rows.Scan(&item.Sequence, &item.OwnerScope, &item.RequestID, &item.PlanID, &item.ActivationID, &triggered, &item.State); err != nil {
			return nil, err
		}
		item.TriggeredAt = time.Unix(0, triggered).UTC()
		result = append(result, item)
	}
	return result, rows.Err()
}

// TransitionPlanRequest 只允许原等待项取得许可后运行，或明确取消、完成；迟到修改不能覆盖后续状态。
func (db *DB) TransitionPlanRequest(ctx context.Context, scope, request, from, to string) error {
	allowed := from == "waiting" && (to == "running" || to == "cancelled" || to == "done") || from == "running" && to == "done"
	if !allowed || scope == "" || request == "" {
		return fmt.Errorf("计划排队状态转换不正确")
	}
	result, err := db.conn.ExecContext(ctx, `UPDATE plan_waiting_requests SET state=? WHERE owner_scope=? AND request_id=? AND state=?`, to, scope, request, from)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		if to == "done" {
			var state string
			if err := db.conn.QueryRowContext(ctx, `SELECT state FROM plan_waiting_requests WHERE owner_scope=? AND request_id=?`, scope, request).Scan(&state); err == nil && state == "done" {
				return nil
			}
		}
		return ErrPlanSnapshotStale
	}
	return nil
}

// RepairReleasedPlanQueue 用原领取和已确认释放的同占用证据结算旧队列，不根据时间、离线或当前快照猜测。
func (db *DB) RepairReleasedPlanQueue(ctx context.Context, scope string) (int64, error) {
	if scope == "" {
		return 0, ErrPlanRequestConflict
	}
	result, err := db.conn.ExecContext(ctx, `UPDATE plan_waiting_requests SET state='done' WHERE owner_scope=? AND state IN ('waiting','running') AND EXISTS (
 SELECT 1 FROM plan_operations claim JOIN plan_operations released ON released.owner_scope=claim.owner_scope AND released.run_id=claim.run_id AND released.owner_id=claim.owner_id AND released.plan_id=claim.plan_id
 WHERE claim.owner_scope=plan_waiting_requests.owner_scope AND claim.request_id=plan_waiting_requests.request_id AND claim.plan_id=plan_waiting_requests.plan_id AND claim.kind='claim' AND released.kind='release' AND released.state='confirmed'
)`, scope)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}
