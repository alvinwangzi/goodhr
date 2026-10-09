// 本文件为 HRPlus 父计划准备独立岗位任务，复用父占用且核对当前执行项，不申请第二份执行权。
package cloudapi

import (
	"context"
	"fmt"
	"goodhr5/local-agent-go/internal/planmodel"
	"net/http"
	"net/url"
)

// PlanItemTaskRequest 保留原运行、执行项和请求编号，凭证只供发送或受保护存储使用。
type PlanItemTaskRequest struct {
	RunID      string `json:"-"`
	ItemRunID  string `json:"-"`
	RequestID  string `json:"request_id"`
	OwnerID    string `json:"owner_id"`
	MachineID  string `json:"machine_id"`
	Credential string `json:"credential"`
}

// String 隐藏普通日志中的占用凭证。
func (r PlanItemTaskRequest) String() string { return "HRPlus 执行项准备请求（凭证隐藏）" }

// GoString 隐藏调试日志中的占用凭证。
func (r PlanItemTaskRequest) GoString() string { return r.String() }

// Validate 在落盘或发送前核对原编号，不允许使用缺失凭证的准备请求。
func (r PlanItemTaskRequest) Validate() error {
	if !planmodel.ValidID(r.RunID) || !planmodel.ValidID(r.ItemRunID) || !planmodel.ValidID(r.RequestID) || !planmodel.ValidID(r.OwnerID) || r.MachineID == "" || len(r.Credential) < 32 {
		return fmt.Errorf("执行项准备缺少原编号、凭证或执行电脑")
	}
	return nil
}

// PrepareExecutionPlanItemTask 按原请求准备当前执行项，泛成功或其他执行项不能提供岗位任务编号。
func (c *Client) PrepareExecutionPlanItemTask(ctx context.Context, token string, input PlanItemTaskRequest) (planmodel.Permit, error) {
	if err := input.Validate(); err != nil {
		return planmodel.Permit{}, err
	}
	path := "/api/execution-plan-runs/" + url.PathEscape(input.RunID) + "/items/" + url.PathEscape(input.ItemRunID) + "/prepare"
	payload, err := c.requestPlan(ctx, token, http.MethodPost, path, input)
	if err != nil {
		return planmodel.Permit{}, err
	}
	result, err := decodePlanPermit(payload, input.RunID, input.OwnerID, input.MachineID)
	if err != nil {
		return planmodel.Permit{}, err
	}
	if (result.Run.State != "starting" && result.Run.State != "running") || (result.Owner.State != "starting" && result.Owner.State != "running") || result.Run.CurrentItem >= len(result.Run.Items) {
		return planmodel.Permit{}, fmt.Errorf("执行项准备结果不允许开始岗位任务")
	}
	item := result.Run.Items[result.Run.CurrentItem]
	if item.ID != input.ItemRunID || !planmodel.ValidID(item.TaskRunID) || (item.State != "pending" && item.State != "running") {
		return planmodel.Permit{}, fmt.Errorf("云端未确认当前执行项的岗位任务编号")
	}
	return result, nil
}
