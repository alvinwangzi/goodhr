// 本文件在 HRPlus 父计划状态事务中同步原 TaskRun 和岗位展示，不按当前岗位猜测历史任务归属。
package httpapi

import (
	"context"
	"database/sql"
	"time"
)

// planItemTaskStatus 将主项和已激活动作一起映射到任务展示，窗口休息保留进度但不显示正在占用页面。
func planItemTaskStatus(r ExecutionPlanRun, item ExecutionPlanItemRun) string {
	status := item.State
	if status == "pending" {
		return "starting"
	}
	if status == "completed" {
		for _, progress := range item.Actions {
			if progress.State == "active" {
				status = "running"
				break
			}
		}
	}
	if status == "running" && r.State == "waiting_window" {
		return "waiting_window"
	}
	return status
}

// syncPlanItemTaskStates 将关联任务实际开始或结束与父进度同事务保存，不提前标记未开始项。
func syncPlanItemTaskStates(ctx context.Context, tx *sql.Tx, r ExecutionPlanRun, preserveNewer bool) error {
	for _, item := range r.Items {
		if item.TaskRunID == "" {
			continue
		}
		status := planItemTaskStatus(r, item)
		_, err := tx.ExecContext(ctx, `UPDATE task_runs SET status=$2,started_at=CASE WHEN $2='running' THEN COALESCE(started_at,NOW()) ELSE started_at END,finished_at=CASE WHEN $2 IN ('completed','failed','stopped') THEN COALESCE(finished_at,NOW()) ELSE finished_at END WHERE id=$1`, item.TaskRunID, status)
		if err != nil {
			return err
		}
		if status != "starting" {
			_, err = tx.ExecContext(ctx, `UPDATE positions SET status=$2,started_at=CASE WHEN $2='running' THEN (SELECT started_at FROM task_runs WHERE id=$3) ELSE started_at END,finished_at=CASE WHEN $2 IN ('completed','failed','stopped') THEN NOW() ELSE NULL END,updated_at=NOW() WHERE id=$1 AND (NOT $4 OR (status<>'running' AND started_at IS NOT DISTINCT FROM (SELECT started_at FROM task_runs WHERE id=$3) AND NOT EXISTS(SELECT 1 FROM task_runs newer WHERE newer.position_id=positions.id AND newer.id<>$3 AND newer.created_at>=(SELECT created_at FROM task_runs WHERE id=$3))))`, item.Snapshot.PositionID, status, item.TaskRunID, preserveNewer)
			if err != nil {
				return err
			}
		}
	}
	return nil
}

// syncMemoryItemTaskStates 在既有计划和岗位锁内同步任务，任务锁独立串行，重复岗位按原项 ID 保存。
func (s *MemoryExecutionPlanStore) syncMemoryItemTaskStates(r ExecutionPlanRun, preserveNewer bool) {
	if s.taskRuns == nil {
		return
	}
	s.taskRuns.mu.Lock()
	defer s.taskRuns.mu.Unlock()
	now := time.Now().UTC()
	for _, item := range r.Items {
		if item.TaskRunID == "" {
			continue
		}
		task, exists := s.taskRuns.runs[item.TaskRunID]
		if !exists {
			continue
		}
		status := planItemTaskStatus(r, item)
		task.Status = status
		if status == "running" && task.StartedAt == nil {
			started := now
			task.StartedAt = &started
		}
		if (status == "completed" || status == "failed" || status == "stopped") && task.FinishedAt == nil {
			finished := now
			task.FinishedAt = &finished
		}
		s.taskRuns.runs[task.ID] = task
		if s.positions != nil && status != "starting" {
			position, exists := s.positions.positions[item.Snapshot.PositionID]
			if !exists {
				continue
			}
			if preserveNewer {
				if position.Status == "running" || !samePlanTaskStart(position.StartedAt, task.StartedAt) {
					continue
				}
				newer := false
				for _, candidate := range s.taskRuns.runs {
					if candidate.PositionID == position.ID && candidate.ID != task.ID && !candidate.CreatedAt.Before(task.CreatedAt) {
						newer = true
						break
					}
				}
				if newer {
					continue
				}
			}
			position.Status = status
			position.UpdatedAt = now
			if status == "running" {
				position.StartedAt = task.StartedAt
				position.FinishedAt = nil
			} else if status == "waiting_window" {
				position.FinishedAt = nil
			} else {
				position.FinishedAt = task.FinishedAt
			}
			s.positions.positions[position.ID] = position
		}
	}
}

// samePlanTaskStart 比较实际开始事实，旧停止确认不能覆盖已重新开始的岗位，即使新任务记录尚未创建。
func samePlanTaskStart(a, b *time.Time) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return a.Equal(*b)
}

// syncStoppedPlanTasks 在停止事务内结算已关联的原任务，保留其他任务后来写入的岗位展示。
func syncStoppedPlanTasks(ctx context.Context, tx *sql.Tx, p ExecutionPlan) error {
	rows, err := tx.QueryContext(ctx, `SELECT `+executionPlanRunColumns+` FROM execution_plan_runs WHERE plan_id=$1 AND activation_id=$2 AND EXISTS(SELECT 1 FROM execution_plan_item_runs i WHERE i.run_id=execution_plan_runs.id AND i.task_run_id IS NOT NULL)`, p.ID, p.ActivationID)
	if err != nil {
		return err
	}
	runs := []ExecutionPlanRun{}
	for rows.Next() {
		run, err := scanPlanRun(rows)
		if err != nil {
			rows.Close()
			return err
		}
		runs = append(runs, run)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, run := range runs {
		run, err = loadPlanItemRuns(ctx, tx, run)
		if err != nil {
			return err
		}
		if err = syncPlanItemTaskStates(ctx, tx, run, true); err != nil {
			return err
		}
	}
	return nil
}
