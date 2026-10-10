// 本文件上传 HRPlus 原报告并核对原摘要回执，不以 HTTP 成功代替原内容确认。
package cloudapi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"goodhr5/local-agent-go/internal/planmodel"
	"net/http"
	"net/url"
	"time"
)

// PlanReportReceipt 保留云端原摘要和当前传输、通知状态，通知 pending 不代表已发送。
type PlanReportReceipt struct {
	RunID             string           `json:"run_id"`
	BodyHash          string           `json:"body_hash"`
	Summary           planmodel.Report `json:"summary"`
	SyncState         string           `json:"sync_state"`
	NotificationState string           `json:"notification_state"`
	CreatedAt         time.Time        `json:"created_at"`
	UpdatedAt         time.Time        `json:"updated_at"`
}

// UploadExecutionPlanReport 上传原安全快照，要求回执保持原摘要及生成时间。
func (c *Client) UploadExecutionPlanReport(ctx context.Context, token, machineID string, original planmodel.Report, syncState string) (PlanReportReceipt, error) {
	if err := original.Validate(); err != nil {
		return PlanReportReceipt{}, err
	}
	if machineID == "" || syncState != "pending" && syncState != "confirmed" {
		return PlanReportReceipt{}, fmt.Errorf("报告缺少执行电脑或同步状态")
	}
	input := struct {
		Summary   planmodel.Report `json:"summary"`
		SyncState string           `json:"sync_state"`
		MachineID string           `json:"machine_id"`
	}{original, syncState, machineID}
	payload, err := c.requestPlan(ctx, token, http.MethodPost, "/api/execution-plan-runs/"+url.PathEscape(original.RunID)+"/report", input)
	if err != nil {
		return PlanReportReceipt{}, err
	}
	var result PlanReportReceipt
	if err := decodePlanField(payload, "report", &result); err != nil {
		return result, err
	}
	if err := result.Summary.Validate(); err != nil {
		return result, err
	}
	expected, _ := json.Marshal(original)
	actual, _ := json.Marshal(result.Summary)
	hash := sha256.Sum256(expected)
	if result.RunID != original.RunID || result.BodyHash != hex.EncodeToString(hash[:]) || string(actual) != string(expected) || result.SyncState != syncState || result.CreatedAt.IsZero() || result.UpdatedAt.IsZero() {
		return PlanReportReceipt{}, fmt.Errorf("云端没有确认原报告内容和同步状态")
	}
	return result, nil
}
