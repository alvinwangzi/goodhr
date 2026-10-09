// 本文件访问 HRPlus 执行计划接口，核对原运行与占用身份，凭证只用于请求且不写入安全快照。
package cloudapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"goodhr5/local-agent-go/internal/planmodel"
	"net/http"
	"net/url"
	"strings"
)

// PlanRequestError 区分账号占用等待、事实冲突与服务错误，不把冲突当作启动许可。
type PlanRequestError struct {
	Status        int
	Code, Message string
}

// Error 返回适合本地主流程记录的错误，不包含请求凭证。
func (e PlanRequestError) Error() string { return e.Message }

// PlanClaimRequest 使用原编号申请许可，Credential 必须从受保护的本地存储读取。
type PlanClaimRequest struct {
	PlanID          string `json:"plan_id"`
	ActivationID    string `json:"activation_id"`
	ExpectedVersion int64  `json:"expected_version"`
	ExecutionDate   string `json:"execution_date"`
	RunID           string `json:"run_id"`
	RequestID       string `json:"request_id"`
	MachineID       string `json:"machine_id"`
	OwnerID         string `json:"owner_id"`
	Credential      string `json:"credential"`
	LocalReserved   bool   `json:"local_reserved"`
}

// String 防止普通日志输出请求凭证。
func (r PlanClaimRequest) String() string { return "HRPlus 计划领取请求（凭证隐藏）" }

// GoString 防止调试格式输出请求凭证。
func (r PlanClaimRequest) GoString() string { return r.String() }

// PlanRunUpdateRequest 上报原运行的递增状态与子项进度，不允许修改岗位配置。
type PlanRunUpdateRequest struct {
	RunID            string                 `json:"-"`
	Action           string                 `json:"-"`
	RequestID        string                 `json:"request_id"`
	OwnerID          string                 `json:"owner_id"`
	MachineID        string                 `json:"machine_id"`
	Credential       string                 `json:"credential"`
	Sequence         int64                  `json:"sequence"`
	State            string                 `json:"state"`
	CurrentItem      int                    `json:"current_item"`
	EndReason        string                 `json:"end_reason,omitempty"`
	CleanupConfirmed bool                   `json:"cleanup_confirmed"`
	Items            []planmodel.ItemUpdate `json:"items,omitempty"`
}

// String 防止普通日志输出收尾凭证。
func (r PlanRunUpdateRequest) String() string { return "HRPlus 计划状态请求（凭证隐藏）" }

// GoString 防止调试格式输出收尾凭证。
func (r PlanRunUpdateRequest) GoString() string { return r.String() }

// requestPlan 复用 HTTP 传输并保留整数精度，不记录带凭证的请求体。
func (c *Client) requestPlan(ctx context.Context, token, method, path string, body any) (map[string]any, error) {
	base, err := c.safeBaseURL()
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(token) == "" {
		return nil, fmt.Errorf("请先登录后再操作计划")
	}
	var raw []byte
	if body != nil {
		raw, err = json.Marshal(body)
		if err != nil {
			return nil, err
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, base+path, bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	payload, status, err := c.doJSONPrecision(req, true)
	if err != nil {
		return nil, err
	}
	if status == 401 {
		return nil, AuthExpiredError{Message: cloudMessage(payload, "计划登录状态已失效")}
	}
	if status < 200 || status >= 300 {
		code, _ := payload["error_code"].(string)
		return nil, PlanRequestError{Status: status, Code: code, Message: cloudMessage(payload, "计划请求未确认，请保留原请求核对")}
	}
	if ok, _ := payload["ok"].(bool); !ok {
		return nil, fmt.Errorf("云端未返回明确的计划成功结果")
	}
	return payload, nil
}

// decodePlanField 转换保留精度的响应字段，缺失实际结果不能当作成功。
func decodePlanField(payload map[string]any, key string, target any) error {
	value, exists := payload[key]
	if !exists || value == nil {
		return fmt.Errorf("云端未返回完整的计划结果")
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, target)
}

// ListExecutionPlans 读取完整用户计划，任何一条结构错误都不返回半套可执行列表。
func (c *Client) ListExecutionPlans(ctx context.Context, token string) ([]planmodel.Plan, error) {
	payload, err := c.requestPlan(ctx, token, http.MethodGet, "/api/execution-plans", nil)
	if err != nil {
		return nil, err
	}
	var result []planmodel.Plan
	if err = decodePlanField(payload, "plans", &result); err != nil {
		return nil, err
	}
	for _, plan := range result {
		if err = plan.Validate(); err != nil {
			return nil, err
		}
	}
	return result, nil
}

// ExecutionPlanRun 读取原运行快照，用于断线或重启后核对，不创建新运行。
func (c *Client) ExecutionPlanRun(ctx context.Context, token, id string) (planmodel.Run, error) {
	var result planmodel.Run
	if !planmodel.ValidID(id) {
		return result, fmt.Errorf("运行编号不正确")
	}
	payload, err := c.requestPlan(ctx, token, http.MethodGet, "/api/execution-plan-runs/"+url.PathEscape(id), nil)
	if err != nil {
		return result, err
	}
	if err = decodePlanField(payload, "run", &result); err != nil {
		return result, err
	}
	if result.ID != id {
		return planmodel.Run{}, fmt.Errorf("云端返回了不同的运行编号")
	}
	if err = result.Validate(); err != nil {
		return planmodel.Run{}, err
	}
	return result, nil
}

// decodePlanPermit 核对真实运行与账号占用，拒绝旧服务的泛成功或不匹配许可。
func decodePlanPermit(payload map[string]any, runID, ownerID, machine string) (planmodel.Permit, error) {
	var result planmodel.Permit
	if err := decodePlanField(payload, "permit", &result); err != nil {
		return result, err
	}
	if err := result.Run.Validate(); err != nil {
		return planmodel.Permit{}, err
	}
	if result.Run.ID != runID || result.Run.OwnerID != ownerID || result.Owner.OwnerID != ownerID || result.Owner.OwnerType != "plan" || result.Owner.MachineID != machine {
		return planmodel.Permit{}, fmt.Errorf("云端许可与原运行或执行电脑不匹配")
	}
	switch result.Owner.State {
	case "starting", "running", "releasing", "released":
	default:
		return planmodel.Permit{}, fmt.Errorf("云端占用状态无法识别")
	}
	return result, nil
}

// ClaimExecutionPlanRun 按原编号领取并核对批次日期，重试不补造新的运行身份。
func (c *Client) ClaimExecutionPlanRun(ctx context.Context, token string, input PlanClaimRequest) (planmodel.Permit, error) {
	if !planmodel.ValidID(input.RunID) || !planmodel.ValidID(input.RequestID) || !planmodel.ValidID(input.OwnerID) || !input.LocalReserved || len(input.Credential) < 32 {
		return planmodel.Permit{}, fmt.Errorf("计划领取缺少原编号、凭证或本地预留")
	}
	payload, err := c.requestPlan(ctx, token, http.MethodPost, "/api/execution-plan-runs/claim", input)
	if err != nil {
		return planmodel.Permit{}, err
	}
	result, err := decodePlanPermit(payload, input.RunID, input.OwnerID, input.MachineID)
	if err != nil {
		return result, err
	}
	if result.Run.PlanID != input.PlanID || result.Run.ActivationID != input.ActivationID || result.Run.ExecutionDate != input.ExecutionDate || result.Run.ConfigVersion != input.ExpectedVersion || result.Owner.State == "released" {
		return planmodel.Permit{}, fmt.Errorf("云端许可与原计划批次、日期或版本不匹配")
	}
	return result, nil
}

// UpdateExecutionPlanRun 只确认原序号和收尾事实，收到不匹配结果时保留待核对请求。
func (c *Client) UpdateExecutionPlanRun(ctx context.Context, token string, input PlanRunUpdateRequest) (planmodel.Permit, error) {
	if !planmodel.ValidID(input.RunID) || !planmodel.ValidID(input.RequestID) || (input.Action != "status" && input.Action != "release") || input.Sequence < 1 || (input.Action == "release" && !input.CleanupConfirmed) {
		return planmodel.Permit{}, fmt.Errorf("计划状态缺少原编号、序号或收尾确认")
	}
	payload, err := c.requestPlan(ctx, token, http.MethodPost, "/api/execution-plan-runs/"+url.PathEscape(input.RunID)+"/"+input.Action, input)
	if err != nil {
		return planmodel.Permit{}, err
	}
	result, err := decodePlanPermit(payload, input.RunID, input.OwnerID, input.MachineID)
	if err != nil {
		return result, err
	}
	if result.Run.Sequence != input.Sequence || result.Run.State != input.State || result.Run.CurrentItem != input.CurrentItem || (input.Action == "release" && result.Owner.State != "released") {
		return planmodel.Permit{}, fmt.Errorf("云端未确认原状态序号或收尾结果")
	}
	return result, nil
}
