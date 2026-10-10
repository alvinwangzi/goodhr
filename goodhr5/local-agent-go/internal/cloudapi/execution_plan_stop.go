// 本文件访问 HRPlus 计划停止确认接口，核对原批次、设备和版本，不把页面按钮状态作为收尾证明。
package cloudapi

import (
	"context"
	"fmt"
	"goodhr5/local-agent-go/internal/planmodel"
	"net/http"
	"net/url"
	"reflect"
)

// PlanStopRequest 保存指定电脑对原停止批次的实际收尾确认，不包含登录或占用凭证。
type PlanStopRequest struct {
	RequestID        string `json:"request_id"`
	ExpectedVersion  int64  `json:"expected_version"`
	ActivationID     string `json:"activation_id"`
	MachineID        string `json:"machine_id"`
	CleanupConfirmed bool   `json:"cleanup_confirmed"`
}

// Validate 复用计划编号校验，拒绝无真实收尾声明的请求。
func (r PlanStopRequest) Validate() error {
	if !planmodel.ValidID(r.RequestID) || !planmodel.ValidID(r.ActivationID) || r.ExpectedVersion < 1 || r.MachineID == "" || !r.CleanupConfirmed {
		return fmt.Errorf("计划停止确认缺少原批次或收尾事实")
	}
	return nil
}

// ConfirmExecutionPlanStop 要求云端确认原停止结果，不接受另一批次、设备或修改后的岗位配置。
func (c *Client) ConfirmExecutionPlanStop(ctx context.Context, token string, original planmodel.Plan, input PlanStopRequest) (planmodel.Plan, error) {
	if err := original.Validate(); err != nil {
		return planmodel.Plan{}, err
	}
	if err := input.Validate(); err != nil {
		return planmodel.Plan{}, err
	}
	if original.State != "stopped" || !original.StopRequested || original.ActivationID != input.ActivationID || original.Version != input.ExpectedVersion || original.MachineID != input.MachineID {
		return planmodel.Plan{}, fmt.Errorf("停止确认不属于原计划")
	}
	payload, err := c.requestPlan(ctx, token, http.MethodPost, "/api/execution-plans/"+url.PathEscape(original.ID)+"/confirm-stop", input)
	if err != nil {
		return planmodel.Plan{}, err
	}
	var result planmodel.Plan
	if err = decodePlanField(payload, "plan", &result); err != nil {
		return result, err
	}
	if err = result.Validate(); err != nil {
		return result, err
	}
	if result.ID != original.ID || result.UserEmail != original.UserEmail || result.TenantID != original.TenantID || result.Version != input.ExpectedVersion || result.ActivationID != input.ActivationID || result.MachineID != input.MachineID || result.State != "stopped" || result.StopRequested || result.StateSequence <= original.StateSequence || !reflect.DeepEqual(result.Config, original.Config) || !result.CreatedAt.Equal(original.CreatedAt) {
		return planmodel.Plan{}, fmt.Errorf("云端没有确认原计划停止结果")
	}
	return result, nil
}
