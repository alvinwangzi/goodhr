// 本文件保存 HRPlus 每次编排项的独立动作进度；同岗位重复项和已激活消息各保留原身份。
package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"reflect"
)

// ExecutionPlanActionProgress 保存动作的真实完成数量及未知结果统计，未知不算成功。
type ExecutionPlanActionProgress struct {
	State        string `json:"state"`
	Count        int64  `json:"count"`
	UnknownCount int64  `json:"unknown_count"`
}

// ExecutionPlanItemRun 是独立编排项的运行快照，不以岗位编号合并计数或激活状态。
type ExecutionPlanItemRun struct {
	ID        string                                 `json:"id"`
	ItemID    string                                 `json:"item_id"`
	Order     int                                    `json:"order"`
	TaskRunID string                                 `json:"task_run_id,omitempty"`
	Snapshot  ExecutionPlanItem                      `json:"snapshot"`
	State     string                                 `json:"state"`
	Actions   map[string]ExecutionPlanActionProgress `json:"actions"`
}

// ExecutionPlanItemUpdate 只接受原执行项身份与动作进度，不接受客户端修改岗位快照或任务归属。
type ExecutionPlanItemUpdate struct {
	ID      string                                 `json:"id"`
	ItemID  string                                 `json:"item_id"`
	State   string                                 `json:"state"`
	Actions map[string]ExecutionPlanActionProgress `json:"actions"`
}

// initialPlanItemRuns 为每条编排项创建独立运行编号和零计数，尚未开始的动作保持 pending。
func initialPlanItemRuns(config ExecutionPlanConfig) ([]ExecutionPlanItemRun, error) {
	result := make([]ExecutionPlanItemRun, 0, len(config.Items))
	for _, item := range config.Items {
		id, err := newExecutionPlanID()
		if err != nil {
			return nil, err
		}
		row := ExecutionPlanItemRun{ID: id, ItemID: item.ID, Order: item.Order, Snapshot: item, State: "pending", Actions: map[string]ExecutionPlanActionProgress{}}
		for _, action := range item.Actions {
			row.Actions[action] = ExecutionPlanActionProgress{State: "pending"}
		}
		result = append(result, row)
	}
	return result, nil
}

// validPlanItemState 使用已确认的主执行项状态，不把消息动作活跃情况混入主项结束状态。
func validPlanItemState(state string) bool {
	switch state {
	case "pending", "running", "completed", "failed", "stopped":
		return true
	}
	return false
}

// applyPlanItemUpdates 先在深拷贝上校验全部子项，任何错误都不能部分覆盖原计数。
func applyPlanItemUpdates(r ExecutionPlanRun, u ExecutionPlanRunUpdate) ([]ExecutionPlanItemRun, error) {
	items := clonePlanRunPermit(ExecutionPlanRunPermit{Run: r}).Run.Items
	if len(items) != len(r.Snapshot.Items) {
		return nil, ErrExecutionPlanSequence
	}
	if u.Items != nil {
		if len(u.Items) != len(items) {
			return nil, ErrExecutionPlanRequest
		}
		for index, input := range u.Items {
			old := items[index]
			if input.ID != old.ID || input.ItemID != old.ItemID || !validPlanItemState(input.State) || len(input.Actions) != len(old.Actions) {
				return nil, ErrExecutionPlanRequest
			}
			if (old.State == "completed" || old.State == "failed" || old.State == "stopped") && input.State != old.State {
				return nil, ErrExecutionPlanSequence
			}
			if old.State == "running" && input.State == "pending" {
				return nil, ErrExecutionPlanSequence
			}
			if index > u.CurrentItem && input.State != "pending" && !(input.State == "stopped" && u.Action == "release" && u.State != "waiting_window") {
				return nil, ErrExecutionPlanSequence
			}
			for action, next := range input.Actions {
				previous, exists := old.Actions[action]
				if !exists || next.Count < previous.Count || next.UnknownCount < previous.UnknownCount || next.Count < 0 || next.UnknownCount < 0 {
					return nil, ErrExecutionPlanSequence
				}
				if next.State != "pending" && next.State != "active" && next.State != "completed" && next.State != "stopped" {
					return nil, ErrExecutionPlanRequest
				}
				if next.State == "stopped" && input.State != "failed" && !(u.Action == "release" && u.State != "waiting_window") {
					return nil, ErrExecutionPlanSequence
				}
				if index > u.CurrentItem && (next.State != "pending" || next.Count != 0 || next.UnknownCount != 0) {
					return nil, ErrExecutionPlanSequence
				}
				if previous.State != "pending" && next.State == "pending" {
					return nil, ErrExecutionPlanSequence
				}
				if previous.State == "stopped" && (next.State != "stopped" || next.Count != previous.Count || next.UnknownCount != previous.UnknownCount) {
					return nil, ErrExecutionPlanSequence
				}
				if previous.State == "completed" && next.State == "active" && action == "greeting" {
					return nil, ErrExecutionPlanSequence
				}
				if next.State == "pending" && (next.Count != 0 || next.UnknownCount != 0) {
					return nil, ErrExecutionPlanSequence
				}
				if next.State == "active" && input.State == "pending" {
					return nil, ErrExecutionPlanSequence
				}
				if previous.State == "pending" && index < r.CurrentItem && next.State != "pending" {
					return nil, ErrExecutionPlanSequence
				}
			}
			items[index].State = input.State
			items[index].Actions = map[string]ExecutionPlanActionProgress{}
			for action, progress := range input.Actions {
				items[index].Actions[action] = progress
			}
		}
	}
	// 游标只能越过已结束的主项；已激活消息可继续活跃，直到整次运行结算。
	for index, item := range items {
		if index < u.CurrentItem && item.State != "completed" && item.State != "failed" && !(u.Action == "release" && item.State == "stopped") {
			return nil, ErrExecutionPlanSequence
		}
		if u.State == "completed" {
			if item.State != "completed" && item.State != "failed" {
				return nil, ErrExecutionPlanSequence
			}
			for _, action := range item.Actions {
				if action.State == "active" || (item.State == "completed" && action.State != "completed") {
					return nil, ErrExecutionPlanSequence
				}
			}
		}
	}
	// 日末、停止或整体故障只结束本批次服务，保留已确认数量与未完成项的事实。
	if u.Action == "release" && u.State != "waiting_window" && u.State != "completed" {
		for index, item := range items {
			if item.State == "pending" || item.State == "running" {
				items[index].State = "stopped"
			}
			for action, progress := range item.Actions {
				if progress.State == "active" {
					progress.State = "stopped"
					items[index].Actions[action] = progress
				}
			}
		}
	}
	return items, nil
}

// planItemProgressJSON 分开保存动作状态和数量，保持现有计数字段及原任务关联。
func planItemProgressJSON(item ExecutionPlanItemRun) ([]byte, []byte, error) {
	counts := map[string]struct {
		Count        int64 `json:"count"`
		UnknownCount int64 `json:"unknown_count"`
	}{}
	states := map[string]string{}
	for action, progress := range item.Actions {
		counts[action] = struct {
			Count        int64 `json:"count"`
			UnknownCount int64 `json:"unknown_count"`
		}{progress.Count, progress.UnknownCount}
		states[action] = progress.State
	}
	raw, err := json.Marshal(counts)
	if err != nil {
		return nil, nil, err
	}
	stateRaw, err := json.Marshal(states)
	return raw, stateRaw, err
}

// insertPlanItemRuns 与父运行及账号领取同事务初始化独立执行项。
func insertPlanItemRuns(ctx context.Context, tx *sql.Tx, runID string, items []ExecutionPlanItemRun) error {
	for _, item := range items {
		snapshot, err := json.Marshal(item.Snapshot)
		if err != nil {
			return err
		}
		counts, states, err := planItemProgressJSON(item)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO execution_plan_item_runs(id,run_id,item_id,ordinal,snapshot,state,counts,action_states) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, item.ID, runID, item.ItemID, item.Order, string(snapshot), item.State, string(counts), string(states))
		if err != nil {
			return err
		}
	}
	return nil
}

// planItemQueryer 复用连接池与已持锁事务的读取方法，避免再开事务读取未提交数据。
type planItemQueryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

// loadPlanItemRuns 读取完整有序执行项及动作计数，不通过岗位编号猜测或合并。
func loadPlanItemRuns(ctx context.Context, q planItemQueryer, r ExecutionPlanRun) (ExecutionPlanRun, error) {
	rows, err := q.QueryContext(ctx, `SELECT id::text,item_id,ordinal,COALESCE(task_run_id::text,''),snapshot,state,counts,action_states FROM execution_plan_item_runs WHERE run_id=$1 ORDER BY ordinal`, r.ID)
	if err != nil {
		return r, err
	}
	defer rows.Close()
	r.Items = make([]ExecutionPlanItemRun, 0, len(r.Snapshot.Items))
	for rows.Next() {
		var item ExecutionPlanItemRun
		var snapshot, countsRaw, statesRaw []byte
		if err = rows.Scan(&item.ID, &item.ItemID, &item.Order, &item.TaskRunID, &snapshot, &item.State, &countsRaw, &statesRaw); err != nil {
			return r, err
		}
		if err = json.Unmarshal(snapshot, &item.Snapshot); err != nil {
			return r, err
		}
		var counts map[string]ExecutionPlanActionProgress
		var states map[string]string
		if err = json.Unmarshal(countsRaw, &counts); err != nil {
			return r, err
		}
		if err = json.Unmarshal(statesRaw, &states); err != nil {
			return r, err
		}
		index := len(r.Items)
		if index >= len(r.Snapshot.Items) || item.Order != index || item.ItemID != r.Snapshot.Items[index].ID || !reflect.DeepEqual(item.Snapshot, r.Snapshot.Items[index]) {
			return r, ErrExecutionPlanSequence
		}
		allowed := map[string]bool{}
		for _, action := range item.Snapshot.Actions {
			allowed[action] = true
		}
		for action := range counts {
			if !allowed[action] {
				return r, ErrExecutionPlanSequence
			}
		}
		for action := range states {
			if !allowed[action] {
				return r, ErrExecutionPlanSequence
			}
		}
		item.Actions = map[string]ExecutionPlanActionProgress{}
		for _, action := range item.Snapshot.Actions {
			progress := counts[action]
			progress.State = states[action]
			if progress.State == "" {
				progress.State = "pending"
			}
			if progress.Count < 0 || progress.UnknownCount < 0 {
				return r, ErrExecutionPlanSequence
			}
			item.Actions[action] = progress
		}
		r.Items = append(r.Items, item)
	}
	if err = rows.Err(); err != nil {
		return r, err
	}
	if len(r.Items) != len(r.Snapshot.Items) {
		return r, ErrExecutionPlanSequence
	}
	return r, nil
}

// savePlanItemRuns 与父状态和释放同事务保存全量子项，不改变岗位快照或 TaskRun 关联。
func savePlanItemRuns(ctx context.Context, tx *sql.Tx, runID string, items []ExecutionPlanItemRun) error {
	for _, item := range items {
		counts, states, err := planItemProgressJSON(item)
		if err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx, `UPDATE execution_plan_item_runs SET state=$3,counts=$4,action_states=$5,updated_at=NOW() WHERE run_id=$1 AND id=$2`, runID, item.ID, item.State, string(counts), string(states))
		if err != nil {
			return err
		}
		n, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if n != 1 {
			return ErrExecutionPlanSequence
		}
	}
	return nil
}
