// 本文件上传 HRPlus 原排队时间并核对完整回执，排队回执不是任务执行许可。
package cloudapi

import (
	"context"
	"fmt"
	"goodhr5/local-agent-go/internal/planmodel"
	"net/http"
	"net/url"
	"time"
)

// PlanWaitFact 不含登录或执行凭证，所有时刻来自原持久队列。
type PlanWaitFact struct {
	PlanID        string     `json:"plan_id"`
	RequestID     string     `json:"request_id"`
	ActivationID  string     `json:"activation_id"`
	ConfigVersion int64      `json:"config_version"`
	MachineID     string     `json:"machine_id"`
	TriggeredAt   time.Time  `json:"triggered_at"`
	QueuedAt      *time.Time `json:"queued_at,omitempty"`
}

// RecordExecutionPlanWait 核对原时间、批次和电脑，不接受其他原请求的成功回执。
func (c *Client) RecordExecutionPlanWait(ctx context.Context, token string, input PlanWaitFact) error {
	if !planmodel.ValidID(input.PlanID) || !planmodel.ValidID(input.RequestID) || !planmodel.ValidID(input.ActivationID) || input.ConfigVersion < 1 || input.MachineID == "" || input.TriggeredAt.IsZero() {
		return fmt.Errorf("原排队事实不完整")
	}
	payload, err := c.requestPlan(ctx, token, http.MethodPost, "/api/execution-plans/"+url.PathEscape(input.PlanID)+"/wait", input)
	if err != nil {
		return err
	}
	var result PlanWaitFact
	if err = decodePlanField(payload, "wait", &result); err != nil {
		return err
	}
	queuedEqual := result.QueuedAt == nil && input.QueuedAt == nil || result.QueuedAt != nil && input.QueuedAt != nil && result.QueuedAt.Equal(*input.QueuedAt)
	if result.PlanID != input.PlanID || result.RequestID != input.RequestID || result.ActivationID != input.ActivationID || result.ConfigVersion != input.ConfigVersion || result.MachineID != input.MachineID || !result.TriggeredAt.Equal(input.TriggeredAt) || !queuedEqual {
		return fmt.Errorf("原排队事实尚未完整确认")
	}
	return nil
}
