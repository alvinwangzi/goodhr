// 本文件在 HRPlus 父计划状态事务中同步原 TaskRun 和岗位展示，不按当前岗位猜测历史任务归属。
package httpapi

import (
	"context"
	"database/sql"
	"time"
)

// syncPlanItemTaskStates 将关联任务实际开始或结束与父进度同事务保存，不提前标记未开始项。
func syncPlanItemTaskStates(ctx context.Context, tx *sql.Tx, r ExecutionPlanRun) error {
	for _, item := range r.Items {
		if item.TaskRunID == "" {
			continue
		}
		status := item.State
		if status == "pending" {
			status = "starting"
		}
		_, err := tx.ExecContext(ctx, `UPDATE task_runs SET status=$2,started_at=CASE WHEN $2='running' THEN COALESCE(started_at,NOW()) ELSE started_at END,finished_at=CASE WHEN $2 IN ('completed','failed','stopped') THEN COALESCE(finished_at,NOW()) ELSE finished_at END WHERE id=$1`, item.TaskRunID, status)
		if err != nil {
			return err
		}
		if status != "starting" {
			_, err = tx.ExecContext(ctx, `UPDATE positions SET status=$2,started_at=CASE WHEN $2='running' THEN COALESCE(started_at,NOW()) ELSE started_at END,finished_at=CASE WHEN $2 IN ('completed','failed','stopped') THEN NOW() ELSE NULL END,updated_at=NOW() WHERE id=$1`, item.Snapshot.PositionID, status)
			if err != nil {
				return err
			}
		}
	}
	return nil
}

// syncMemoryItemTaskStates 在既有计划和岗位锁内同步任务，任务锁独立串行，重复岗位按原项 ID 保存。
func (s *MemoryExecutionPlanStore) syncMemoryItemTaskStates(r ExecutionPlanRun) {
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
		status := item.State
		if status == "pending" {
			status = "starting"
		}
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
			position := s.positions.positions[item.Snapshot.PositionID]
			position.Status = status
			position.UpdatedAt = now
			if status == "running" {
				position.StartedAt = task.StartedAt
				position.FinishedAt = nil
			} else {
				position.FinishedAt = task.FinishedAt
			}
			s.positions.positions[position.ID] = position
		}
	}
}
