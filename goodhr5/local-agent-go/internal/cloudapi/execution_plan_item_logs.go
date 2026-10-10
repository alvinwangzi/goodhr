// 本文件通过 HRPlus 原日志批次摘要确认云端存储，不将 HTTP 成功作为原内容已确认。
package cloudapi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"goodhr5/local-agent-go/internal/localdb"
	"net/http"
	"net/url"
)

// PlanItemLogHash 对原安全日志字段生成与云端一致的内容摘要，不包含所有者摘要或令牌。
func PlanItemLogHash(value any) string {
	raw, _ := json.Marshal(value)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// UploadPlanItemLogs 要求整批原归属、原内容和数量回执一致，日志重试不改变原时间。
func (c *Client) UploadPlanItemLogs(ctx context.Context, token, machine string, logs []localdb.PlanItemLog) error {
	if len(logs) == 0 || len(logs) > 100 || machine == "" {
		return fmt.Errorf("原日志批次不完整")
	}
	first := logs[0]
	for _, entry := range logs {
		if entry.PlanRunID != first.PlanRunID || entry.ItemRunID != first.ItemRunID || entry.TaskRunID != first.TaskRunID {
			return fmt.Errorf("日志批次包含不同原执行项")
		}
	}
	payload, err := c.requestPlan(ctx, token, http.MethodPost, "/api/execution-plan-runs/"+url.PathEscape(first.PlanRunID)+"/items/"+url.PathEscape(first.ItemRunID)+"/logs", map[string]any{"machine_id": machine, "logs": logs})
	if err != nil {
		return err
	}
	var receipt struct {
		RunID  string `json:"run_id"`
		ItemID string `json:"item_run_id"`
		TaskID string `json:"task_run_id"`
		Hash   string `json:"batch_hash"`
		Count  int    `json:"count"`
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	if err = json.Unmarshal(raw, &receipt); err != nil {
		return err
	}
	if receipt.RunID != first.PlanRunID || receipt.ItemID != first.ItemRunID || receipt.TaskID != first.TaskRunID || receipt.Hash != PlanItemLogHash(logs) || receipt.Count != len(logs) {
		return fmt.Errorf("云端尚未确认完整原日志批次")
	}
	return nil
}
