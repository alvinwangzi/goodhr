// 本文件读取 HRPlus 单岗位的真实动作与本次统计，运行结束后继续展示持久化检查点。
package positionrunner

import (
	"context"
	"goodhr5/local-agent-go/internal/localdb"
	"time"
)

// actionStatusCheckpoint 优先读取当前运行编号，结束后按原子开始顺序读取最近一轮，避免旧补传覆盖新状态。
func (r *Runner) actionStatusCheckpoint(positionID string) (localdb.ActionCheckpoint, bool) {
	r.mu.Lock()
	runID := ""
	if state := r.running[positionID]; state != nil {
		runID = state.options.LocalRunID
	}
	r.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var checkpoint localdb.ActionCheckpoint
	var err error
	if runID != "" {
		checkpoint, err = r.db.LoadActionCheckpoint(ctx, runID)
	} else {
		checkpoint, err = r.db.LatestActionCheckpoint(ctx, positionID)
	}
	return checkpoint, err == nil
}

// dispatchStatusMap 将已保存事实用于现有状态接口，不用前端计时器启动任务。
func dispatchStatusMap(checkpoint localdb.ActionCheckpoint, running bool) map[string]any {
	var last, next any
	if !checkpoint.LastMessageCheck.IsZero() {
		last = checkpoint.LastMessageCheck.Format(time.RFC3339)
	}
	if !checkpoint.NextMessageCheck.IsZero() {
		next = checkpoint.NextMessageCheck.Format(time.RFC3339)
	}
	return map[string]any{"local_run_id": checkpoint.RunID, "current_action": checkpoint.CurrentAction, "prioritize_reply": checkpoint.PrioritizeReply, "last_message_check": last, "next_message_check": next, "waiting_for_check": running && !checkpoint.NextMessageCheck.IsZero() && time.Now().Before(checkpoint.NextMessageCheck)}
}
