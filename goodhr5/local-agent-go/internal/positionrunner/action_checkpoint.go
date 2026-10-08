// 本文件在 HRPlus 单岗位候选人安全边界保存实际进度，纯读取、发送未知和待重试不算完成。
package positionrunner

import (
	"context"
	"strings"
)

// checkpointQueue 移除临时 DOM 引用、下标和异步句柄，只保存可重新按真实 ID 定位的候选人事实。
func checkpointQueue(queue []map[string]any) []map[string]any {
	result := make([]map[string]any, 0, len(queue))
	for _, candidate := range queue {
		item := map[string]any{}
		for key, value := range candidate {
			if key == "element_ref" || key == "card_index" || strings.HasPrefix(key, "_") {
				continue
			}
			item[key] = value
		}
		result = append(result, item)
	}
	return result
}

// appendCompletedAnchor 保留最近三个已安全处理的完整 ID，不记录未知结果。
func appendCompletedAnchor(anchors []string, id string) []string {
	result := []string{}
	for _, old := range anchors {
		if old != id {
			result = append(result, old)
		}
	}
	result = append(result, id)
	if len(result) > 3 {
		result = result[len(result)-3:]
	}
	return result
}

// saveScanCheckpoint 在候选人已确认结束后原子保存实际结果、队列和本次计数。
func (r *Runner) saveScanCheckpoint(ctx context.Context, options StartOptions, candidate map[string]any, queue []map[string]any, greeted int, counts ...batchProcessResult) error {
	if options.LocalRunID == "" {
		return nil
	}
	checkpoint, err := r.db.LoadActionCheckpoint(ctx, options.LocalRunID)
	if err != nil {
		return err
	}
	status, reason := "retry_pending", stringFromMap(candidate, "error")
	switch stringFromMap(candidate, "status") {
	case "greeted", "contacted", "saved", "resume_received":
		status = "completed"
	case "skipped":
		status = "skipped"
		reason = stringFromMap(candidate, "skip_reason")
	case "unknown":
		status = "unknown"
	case "processing":
		status = "processing"
	}
	if !options.EnableGreet && (stringFromMap(candidate, "status") == "passed" || stringFromMap(candidate, "status") == "ai_passed" || stringFromMap(candidate, "status") == "detail_fetched") {
		status = "completed"
	}
	checkpoint.Greeted = greeted
	if len(counts) > 0 {
		checkpoint.Scanned = counts[0].Scanned
		checkpoint.Skipped = counts[0].Skipped
		checkpoint.Failed = counts[0].Failed
	}
	checkpoint.Queue = checkpointQueue(queue)
	checkpoint.CurrentAction = "greeting"
	if stats, ok := r.currentReplyStats(checkpoint.PositionID); ok {
		checkpoint.Replied = stats.Replied
	}
	if stats, ok := r.currentReGreetStats(checkpoint.PositionID); ok {
		checkpoint.ReGreeted = stats["sent"]
	}
	id := stringFromMap(candidate, "id")
	if status == "completed" || status == "skipped" {
		checkpoint.Anchors = appendCompletedAnchor(checkpoint.Anchors, id)
	}
	return r.db.SaveActionCandidate(ctx, checkpoint, id, status, reason)
}

// scanCandidateNeedsReview 查询本次未知或处理中记录，恢复时必须先核对，不能再打一次。
func (r *Runner) scanCandidateNeedsReview(ctx context.Context, runID, positionID, candidateID string) (bool, error) {
	status, err := r.db.ActionCandidateStatus(ctx, runID, positionID, candidateID)
	return status == "unknown" || status == "processing", err
}
