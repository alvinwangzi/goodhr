// Package cloudapi 负责 Go 版本本地程序访问云端公开接口和会员接口。
package cloudapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Client 是云端接口客户端。
type Client struct {
	BaseURL    string
	HTTPClient *http.Client
}

// PositionStatusSyncResult 表示云端岗位状态同步结果。
// Status 为云端确认的状态，NoticeSent 表示岗位完成邮件已经发送或此前已经确认发送，
// RunID 为本次岗位运行对应的云端执行任务记录 ID（旧版云端可能为空）。
type PositionStatusSyncResult struct {
	Status     string
	NoticeSent bool
	RunID      string
}

// AuthExpiredError 表示云端登录态已经失效。
type AuthExpiredError struct {
	Message string
}

// Error 返回登录态失效的提示。
func (e AuthExpiredError) Error() string {
	if strings.TrimSpace(e.Message) == "" {
		return "账号已在其他地方登录，请重新登录"
	}
	return e.Message
}

// PlatformConfig 表示从云端读取到的平台配置。
type PlatformConfig map[string]any

// New 创建云端接口客户端。
// baseURL 为云端 HTTP API 基础地址。
func New(baseURL string) *Client {
	return &Client{
		BaseURL: strings.TrimSpace(baseURL),
		HTTPClient: &http.Client{
			Timeout: 15 * time.Second,
		},
	}
}

// FetchLocalAgentConsoleURL 从云端公共接口读取本地程序启动后要打开的控制台地址。
// ctx 为请求上下文。
func (c *Client) FetchLocalAgentConsoleURL(ctx context.Context) (string, error) {
	baseURL, err := c.safeBaseURL()
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/api/system/local-agent-console-url", nil)
	if err != nil {
		return "", fmt.Errorf("创建控制台地址请求失败：%w", err)
	}
	payload, status, err := c.doJSON(req)
	if err != nil {
		return "", fmt.Errorf("读取控制台地址失败：%w", err)
	}
	if status >= 400 {
		return "", fmt.Errorf("%s", cloudMessage(payload, "读取控制台地址失败"))
	}
	rawURL, _ := payload["url"].(string)
	return strings.TrimSpace(rawURL), nil
}

// FetchPlatformConfig 从云端公开接口读取指定平台配置。
// ctx 为请求上下文，platformID 为平台 ID。
func (c *Client) FetchPlatformConfig(ctx context.Context, platformID string) (PlatformConfig, error) {
	baseURL, err := c.safeBaseURL()
	if err != nil {
		return nil, err
	}
	safePlatform := strings.ToLower(strings.TrimSpace(platformID))
	if safePlatform == "" {
		return nil, fmt.Errorf("平台 ID 不能为空，无法读取平台配置")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/api/platforms/config/", nil)
	if err != nil {
		return nil, fmt.Errorf("创建平台配置请求失败：%w", err)
	}
	payload, status, err := c.doJSON(req)
	if err != nil {
		return nil, fmt.Errorf("读取云端平台配置失败：%w", err)
	}
	if status >= 400 {
		return nil, fmt.Errorf("%s", cloudMessage(payload, "读取云端平台配置失败"))
	}
	configs, ok := configList(payload["configs"])
	if !ok {
		if data, ok := payload["data"].(map[string]any); ok {
			configs, ok = configList(data["configs"])
		}
	}
	if !ok {
		return nil, fmt.Errorf("云端平台配置返回格式不正确")
	}
	targetKey := "platform." + safePlatform
	for _, item := range configs {
		key := strings.ToLower(strings.TrimSpace(stringFromMap(item, "config_key")))
		if key != targetKey {
			continue
		}
		config, err := decodeConfigValue(item["config_value"])
		if err != nil {
			return nil, err
		}
		if _, ok := config["id"]; !ok {
			config["id"] = safePlatform
		}
		return config, nil
	}
	return nil, fmt.Errorf("云端没有找到平台配置：%s", safePlatform)
}

// FetchSubscription 读取云端会员状态。
// ctx 为请求上下文，token 为登录令牌。
func (c *Client) FetchSubscription(ctx context.Context, token string) (map[string]any, error) {
	payload, status, err := c.getAuthed(ctx, token, "/api/subscription/status")
	if err != nil {
		return nil, fmt.Errorf("会员校验失败：%w", err)
	}
	if status >= 400 {
		return nil, fmt.Errorf("%s", cloudMessage(payload, "会员校验失败"))
	}
	subscription, ok := payload["subscription"].(map[string]any)
	if !ok {
		if data, ok := payload["data"].(map[string]any); ok {
			subscription, ok = data["subscription"].(map[string]any)
		}
	}
	if !ok {
		return nil, fmt.Errorf("会员校验返回格式错误")
	}
	return subscription, nil
}

// ValidateSession 验证当前云端登录态是否仍然有效。
// ctx 为请求上下文，token 为登录令牌。
func (c *Client) ValidateSession(ctx context.Context, token string) error {
	_, err := c.sessionPayload(ctx, token)
	return err
}

// SessionOwner 复用登录态验证接口读取服务端确认的所有者，不解析客户端令牌或相信前端提交的姓名。
func (c *Client) SessionOwner(ctx context.Context, token string) (string, error) {
	identity, err := c.SessionIdentity(ctx, token)
	return identity.UserEmail, err
}

// CloudSessionIdentity 仅包含服务端确认的身份，不含令牌或客户端声明。
type CloudSessionIdentity struct{ UserEmail, TenantID string }

// SessionIdentity 复用同一登录验证响应读取用户和团队，旧服务端缺少团队时不自行猜测。
func (c *Client) SessionIdentity(ctx context.Context, token string) (CloudSessionIdentity, error) {
	payload, err := c.sessionPayload(ctx, token)
	if err != nil {
		return CloudSessionIdentity{}, err
	}
	if data, ok := payload["data"].(map[string]any); ok {
		payload = data
	}
	user, _ := payload["user"].(map[string]any)
	email, _ := user["email"].(string)
	email = strings.ToLower(strings.TrimSpace(email))
	if email == "" || !strings.Contains(email, "@") {
		return CloudSessionIdentity{}, fmt.Errorf("登录校验未返回账号所有者，暂不能恢复补传")
	}
	tenantID, _ := user["tenant_id"].(string)
	return CloudSessionIdentity{UserEmail: email, TenantID: tenantID}, nil
}

// sessionPayload 统一验证登录状态并返回已验证响应，供原校验和账号作用域读取复用。
func (c *Client) sessionPayload(ctx context.Context, token string) (map[string]any, error) {
	payload, status, err := c.getAuthed(ctx, token, "/api/auth/me")
	if err != nil {
		return nil, fmt.Errorf("验证账号登录态失败：%w", err)
	}
	if status == http.StatusUnauthorized || status == http.StatusForbidden {
		return nil, AuthExpiredError{Message: cloudMessage(payload, "账号已在其他地方登录，请重新登录")}
	}
	if status >= 400 {
		return nil, fmt.Errorf("%s", cloudMessage(payload, "验证账号登录态失败"))
	}
	return payload, nil
}

// FetchPosition 读取云端岗位运行详情。
// ctx 为请求上下文，token 为登录令牌，positionID 为云端岗位运行 ID。
func (c *Client) FetchPosition(ctx context.Context, token string, positionID string) (map[string]any, error) {
	positionID = strings.TrimSpace(positionID)
	if positionID == "" {
		return nil, fmt.Errorf("岗位运行 ID 不能为空")
	}
	payload, status, err := c.getAuthed(ctx, token, "/api/positions/"+url.PathEscape(positionID))
	if err != nil {
		return nil, fmt.Errorf("读取云端岗位运行失败：%w", err)
	}
	if status >= 400 {
		return nil, fmt.Errorf("%s", cloudMessage(payload, "读取云端岗位运行失败"))
	}
	position, ok := payload["position"].(map[string]any)
	if !ok {
		if data, ok := payload["data"].(map[string]any); ok {
			position, ok = data["position"].(map[string]any)
		}
	}
	if !ok {
		return nil, fmt.Errorf("云端岗位运行返回格式错误")
	}
	return position, nil
}

// FetchEffectiveAIConfig 读取云端当前用户最终生效的 AI 配置。
// ctx 为请求上下文，token 为登录令牌，返回包含明文 API Key 的配置。
func (c *Client) FetchEffectiveAIConfig(ctx context.Context, token string) (map[string]any, error) {
	payload, status, err := c.getAuthed(ctx, token, "/api/config/effective-ai?reveal_api_key=1")
	if err != nil {
		return nil, fmt.Errorf("读取云端 AI 配置失败：%w", err)
	}
	if status >= 400 {
		return nil, fmt.Errorf("%s", cloudMessage(payload, "读取云端 AI 配置失败"))
	}
	config, ok := payload["config"].(map[string]any)
	if !ok {
		if data, ok := payload["data"].(map[string]any); ok {
			config, ok = data["config"].(map[string]any)
		}
	}
	if !ok || config == nil {
		return nil, fmt.Errorf("请先在个人配置里填写云端 AI 接口")
	}
	return config, nil
}

// FetchUserPreferences 读取云端当前用户个人运行配置。
// ctx 为请求上下文，token 为登录令牌。
func (c *Client) FetchUserPreferences(ctx context.Context, token string) (map[string]any, error) {
	payload, status, err := c.getAuthed(ctx, token, "/api/config/user-preferences")
	if err != nil {
		return nil, fmt.Errorf("读取云端个人配置失败：%w", err)
	}
	if status >= 400 {
		return nil, fmt.Errorf("%s", cloudMessage(payload, "读取云端个人配置失败"))
	}
	config, ok := payload["config"].(map[string]any)
	if !ok {
		if data, ok := payload["data"].(map[string]any); ok {
			config, ok = data["config"].(map[string]any)
		}
	}
	if !ok || config == nil {
		return map[string]any{}, nil
	}
	return config, nil
}

// SavePositionCandidate 将本地候选人结果保存到云端简历库。
// ctx 为请求上下文，token 为登录令牌，positionID 为云端岗位运行 ID，candidate 为候选人 JSON。
func (c *Client) SavePositionCandidate(ctx context.Context, token string, positionID string, candidate map[string]any) error {
	positionID = strings.TrimSpace(positionID)
	if positionID == "" {
		return fmt.Errorf("岗位运行 ID 不能为空")
	}
	payload, status, err := c.postAuthed(ctx, token, "/api/positions/"+url.PathEscape(positionID)+"/candidates", candidate)
	if err != nil {
		return fmt.Errorf("保存候选人到云端失败：%w", err)
	}
	if status >= 400 {
		return fmt.Errorf("%s", cloudMessage(payload, "保存候选人到云端失败"))
	}
	return nil
}

// AddProcessedResumes 上报本地岗位运行本次去重后新增的已处理简历数量。
// ctx 为请求上下文，token 为登录令牌，positionID 为云端岗位运行 ID，count 为新增数量。
func (c *Client) AddProcessedResumes(ctx context.Context, token string, positionID string, count int) error {
	positionID = strings.TrimSpace(positionID)
	if positionID == "" {
		return fmt.Errorf("岗位运行 ID 不能为空")
	}
	if count <= 0 {
		return nil
	}
	payload, status, err := c.postAuthed(ctx, token, "/api/positions/"+url.PathEscape(positionID)+"/processed-resumes", map[string]any{
		"count": count,
	})
	if err != nil {
		return fmt.Errorf("同步已处理简历数失败：%w", err)
	}
	if status >= 400 {
		return fmt.Errorf("%s", cloudMessage(payload, "同步已处理简历数失败"))
	}
	return nil
}

// SyncPositionCounts 将本地岗位运行累计统计同步到云端岗位运行记录。
// ctx 为请求上下文，token 为登录令牌，positionID 为云端岗位运行 ID，counts 为统计字段。
func (c *Client) SyncPositionCounts(ctx context.Context, token string, positionID string, counts map[string]any) error {
	positionID = strings.TrimSpace(positionID)
	if positionID == "" {
		return fmt.Errorf("岗位运行 ID 不能为空")
	}
	payload, status, err := c.postAuthed(ctx, token, "/api/positions/"+url.PathEscape(positionID)+"/counts", counts)
	if err != nil {
		return fmt.Errorf("同步岗位运行统计失败：%w", err)
	}
	if status >= 400 {
		return fmt.Errorf("%s", cloudMessage(payload, "同步岗位运行统计失败"))
	}
	return nil
}

// StopPosition 通知云端岗位运行已经停止。
// ctx 为请求上下文，token 为登录令牌，positionID 为云端岗位运行 ID。
func (c *Client) StopPosition(ctx context.Context, token string, positionID string, identity ...map[string]any) error {
	positionID = strings.TrimSpace(positionID)
	if positionID == "" {
		return fmt.Errorf("岗位运行 ID 不能为空")
	}
	request := map[string]any{}
	if len(identity) > 0 {
		request = identity[0]
	}
	payload, status, err := c.postAuthed(ctx, token, "/api/positions/"+url.PathEscape(positionID)+"/stop", request)
	if err != nil {
		return fmt.Errorf("通知云端停止岗位运行失败：%w", err)
	}
	if status >= 400 {
		return fmt.Errorf("%s", cloudMessage(payload, "通知云端停止岗位运行失败"))
	}
	return nil
}

// SyncPositionStatus 通知云端岗位运行当前状态并返回邮件提醒结果。
// ctx 为请求上下文，token 为登录令牌，positionID 为云端岗位运行 ID，status 为 completed、stopped 或 running，machineID 为本机设备机器码。
func (c *Client) SyncPositionStatus(ctx context.Context, token string, positionID string, status string, machineID string) (PositionStatusSyncResult, error) {
	return c.SyncPositionStatusWithCounts(ctx, token, positionID, status, machineID, 0, 0)
}

// SyncPositionStatusWithCounts 通知云端岗位状态并携带本次打招呼和跳过数量。
// ctx 为请求上下文，其余参数为登录信息、岗位状态、本机设备机器码和本次统计。
func (c *Client) SyncPositionStatusWithCounts(ctx context.Context, token string, positionID string, status string, machineID string, greeted, skipped int) (PositionStatusSyncResult, error) {
	return c.SyncTaskStatus(ctx, token, positionID, TaskStatusRequest{Status: status, MachineID: machineID, Greeted: greeted, Skipped: skipped})
}

// TaskStatusRequest 只上传任务类型、所有权和统计，不包含聊天或回复正文。
type TaskStatusRequest struct {
	Status    string `json:"status"`
	TaskType  string `json:"task_type,omitempty"`
	RunID     string `json:"run_id,omitempty"`
	MachineID string `json:"machine_id"`
	Greeted   int    `json:"run_greeted_count"`
	Skipped   int    `json:"run_skipped_count"`
}

// SyncTaskStatus 复用现有状态接口，自动回复要求云端明确许可和本次运行 ID。
func (c *Client) SyncTaskStatus(ctx context.Context, token, positionID string, request TaskStatusRequest) (PositionStatusSyncResult, error) {
	status := request.Status
	positionID = strings.TrimSpace(positionID)
	if positionID == "" {
		return PositionStatusSyncResult{}, fmt.Errorf("岗位运行 ID 不能为空")
	}
	status = strings.TrimSpace(status)
	if status == "" {
		return PositionStatusSyncResult{}, fmt.Errorf("岗位运行状态不能为空")
	}
	// machine_id 必须上报：云端在运行中状态会校验设备绑定，缺失会被拒绝并导致执行任务记录无法创建。
	request.Status = status
	request.MachineID = strings.TrimSpace(request.MachineID)
	request.Greeted = max(0, request.Greeted)
	request.Skipped = max(0, request.Skipped)
	messageTask, greetingTask := false, false
	for _, task := range strings.Split(request.TaskType, ",") {
		task = strings.TrimSpace(task)
		messageTask = messageTask || task == "auto_reply" || task == "re_greet"
		greetingTask = greetingTask || task == "greeting"
	}
	if messageTask && !greetingTask {
		request.Greeted = 0
	}
	payload, code, err := c.postAuthed(ctx, token, "/api/positions/"+url.PathEscape(positionID)+"/status", request)
	if err != nil {
		return PositionStatusSyncResult{}, fmt.Errorf("同步云端岗位运行状态失败：%w", err)
	}
	if code >= 400 {
		return PositionStatusSyncResult{}, fmt.Errorf("%s", cloudMessage(payload, "同步云端岗位运行状态失败"))
	}
	if messageTask {
		allowed, _ := payload["ok"].(bool)
		runID := strings.TrimSpace(stringFromMap(payload, "run_id"))
		if !allowed || stringFromMap(payload, "status") != status || runID == "" || (request.RunID != "" && runID != request.RunID) {
			taskLabel := "自动回复"
			if request.TaskType == "re_greet" {
				taskLabel = "复打招呼"
			}
			return PositionStatusSyncResult{}, fmt.Errorf("云端未确认本次%s许可或运行记录", taskLabel)
		}
	}
	noticeSent, _ := payload["notice_sent"].(bool)
	return PositionStatusSyncResult{
		Status:     stringFromMap(payload, "status"),
		NoticeSent: noticeSent,
		RunID:      stringFromMap(payload, "run_id"),
	}, nil
}

// NotifyResumeRequested 把岗位收尾时完成的"求简历"结果补报到云端，供简历库时间线和执行任务名单展示。
// ctx 为请求上下文，token 为登录令牌，positionID 为云端岗位 ID，runID 为执行任务记录 ID，names 为本轮完成索要的候选人姓名。
func (c *Client) NotifyResumeRequested(ctx context.Context, token string, positionID string, runID string, names []string) error {
	positionID = strings.TrimSpace(positionID)
	if positionID == "" {
		return fmt.Errorf("岗位 ID 不能为空")
	}
	cleaned := make([]string, 0, len(names))
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name != "" {
			cleaned = append(cleaned, name)
		}
	}
	if len(cleaned) == 0 {
		return fmt.Errorf("候选人名单不能为空")
	}
	payload, code, err := c.postAuthed(ctx, token, "/api/positions/"+url.PathEscape(positionID)+"/resume-requests", map[string]any{
		"run_id": strings.TrimSpace(runID), "names": cleaned,
	})
	if err != nil {
		return fmt.Errorf("补报索要简历结果失败：%w", err)
	}
	if code >= 400 {
		return fmt.Errorf("%s", cloudMessage(payload, "补报索要简历结果失败"))
	}
	return nil
}

// SyncResumeTracking 复用简历补报入口同步单个意向候选人的进度，不累加岗位统计。
func (c *Client) SyncResumeTracking(ctx context.Context, token, positionID string, candidate map[string]any) error {
	if strings.TrimSpace(positionID) == "" {
		return fmt.Errorf("岗位 ID 不能为空")
	}
	payload, code, err := c.postAuthed(ctx, token, "/api/positions/"+url.PathEscape(positionID)+"/resume-requests", map[string]any{"candidate": candidate})
	if err != nil {
		return err
	}
	if code >= 400 {
		return fmt.Errorf("%s", cloudMessage(payload, "简历进度同步失败"))
	}
	if ok, _ := payload["ok"].(bool); !ok {
		return fmt.Errorf("云端尚未确认简历进度")
	}
	return nil
}

// ScreeningRecord 表示上报给云端的候选人扫描记录。
type ScreeningRecord struct {
	Platform            string `json:"platform"`
	PlatformCandidateID string `json:"platform_candidate_id"`
	CandidateName       string `json:"candidate_name"`
	Score               int    `json:"score"`
	Status              string `json:"status"`
	ResumeStatus        string `json:"resume_status,omitempty"`
	Source              string `json:"source"`
	ContactObserved     bool   `json:"contact_observed,omitempty"`
}

// ScreeningResult 表示云端返回的扫描记录。
type ScreeningResult struct {
	ID                  string `json:"id"`
	PositionID          string `json:"position_id"`
	Platform            string `json:"platform"`
	PlatformCandidateID string `json:"platform_candidate_id"`
	CandidateName       string `json:"candidate_name"`
	Score               int    `json:"score"`
	Status              string `json:"status"`
	ResumeStatus        string `json:"resume_status"`
	Source              string `json:"source"`
	ContactObserved     bool   `json:"contact_observed"`
}

// boolFromMap 读取云端 JSON 中的布尔事实，缺失或类型不正确时不视为已确认。
func boolFromMap(data map[string]any, key string) bool {
	value, _ := data[key].(bool)
	return value
}

// ReportScreenings 批量上报候选人扫描记录到云端。
// 上报失败不阻塞业务流程，调用方可选择记日志后继续。
func (c *Client) ReportScreenings(ctx context.Context, token string, positionID string, records []ScreeningRecord) error {
	positionID = strings.TrimSpace(positionID)
	if positionID == "" {
		return fmt.Errorf("岗位 ID 不能为空")
	}
	if len(records) == 0 {
		return nil
	}
	payload, code, err := c.postAuthed(ctx, token, "/api/positions/"+url.PathEscape(positionID)+"/screenings", map[string]any{
		"items": records,
	})
	if err != nil {
		return fmt.Errorf("上报扫描记录失败：%w", err)
	}
	if code >= 400 {
		return fmt.Errorf("%s", cloudMessage(payload, "上报扫描记录失败"))
	}
	return nil
}

// FindScreening 查询单个候选人的扫描记录，找不到时返回空 result 和 nil error。
func (c *Client) FindScreening(ctx context.Context, token string, positionID string, platform string, candidateID string) (*ScreeningResult, error) {
	positionID = strings.TrimSpace(positionID)
	if positionID == "" {
		return nil, fmt.Errorf("岗位 ID 不能为空")
	}
	path := fmt.Sprintf("/api/positions/%s/screenings/find?platform=%s&candidate_id=%s",
		url.PathEscape(positionID), url.QueryEscape(platform), url.QueryEscape(candidateID))
	payload, code, err := c.getAuthed(ctx, token, path)
	if err != nil {
		return nil, fmt.Errorf("查询扫描记录失败：%w", err)
	}
	if code >= 400 {
		return nil, fmt.Errorf("%s", cloudMessage(payload, "查询扫描记录失败"))
	}
	data, _ := payload["item"].(map[string]any)
	if data == nil {
		return nil, nil
	}
	result := &ScreeningResult{
		ID:                  stringFromMap(data, "id"),
		PositionID:          stringFromMap(data, "position_id"),
		Platform:            stringFromMap(data, "platform"),
		PlatformCandidateID: stringFromMap(data, "platform_candidate_id"),
		CandidateName:       stringFromMap(data, "candidate_name"),
		Score:               intFromMap(data, "score"),
		Status:              stringFromMap(data, "status"),
		ResumeStatus:        stringFromMap(data, "resume_status"),
		Source:              stringFromMap(data, "source"),
		ContactObserved:     boolFromMap(data, "contact_observed"),
	}
	return result, nil
}

// FindScreeningByName 按候选人姓名查询扫描记录，找不到时返回空 result 和 nil error。
func (c *Client) FindScreeningByName(ctx context.Context, token string, positionID string, platform string, candidateName string) (*ScreeningResult, error) {
	positionID = strings.TrimSpace(positionID)
	if positionID == "" {
		return nil, fmt.Errorf("岗位 ID 不能为空")
	}
	path := fmt.Sprintf("/api/positions/%s/screenings/find?platform=%s&name=%s",
		url.PathEscape(positionID), url.QueryEscape(platform), url.QueryEscape(candidateName))
	payload, code, err := c.getAuthed(ctx, token, path)
	if err != nil {
		return nil, fmt.Errorf("查询扫描记录失败：%w", err)
	}
	if code >= 400 {
		return nil, fmt.Errorf("%s", cloudMessage(payload, "查询扫描记录失败"))
	}
	data, _ := payload["item"].(map[string]any)
	if data == nil {
		return nil, nil
	}
	result := &ScreeningResult{
		ID:                  stringFromMap(data, "id"),
		PositionID:          stringFromMap(data, "position_id"),
		Platform:            stringFromMap(data, "platform"),
		PlatformCandidateID: stringFromMap(data, "platform_candidate_id"),
		CandidateName:       stringFromMap(data, "candidate_name"),
		Score:               intFromMap(data, "score"),
		Status:              stringFromMap(data, "status"),
		ResumeStatus:        stringFromMap(data, "resume_status"),
		Source:              stringFromMap(data, "source"),
		ContactObserved:     boolFromMap(data, "contact_observed"),
	}
	return result, nil
}

// ReGreetCandidate 表示云端返回的复打招呼候选人。
type ReGreetCandidate struct {
	GreetedAt           string `json:"greeted_at"`
	LastReGreetedAt     string `json:"last_re_greeted_at"`
	ID                  string `json:"id"`
	PositionID          string `json:"position_id"`
	Platform            string `json:"platform"`
	PlatformCandidateID string `json:"platform_candidate_id"`
	CandidateName       string `json:"candidate_name"`
	ReGreetCount        int    `json:"re_greet_count"`
}

// FetchReGreetCandidates 从云端拉取当前岗位的复打招呼候选名单。
// timeRangeDays 为复打时间范围（天），intervalMinMinutes 为最小间隔（分钟），maxCount 为复打次数上限。
func (c *Client) FetchReGreetCandidates(ctx context.Context, token string, positionID string, platform string, timeRangeDays int, intervalMinMinutes int, maxCount int, requireReceipt ...bool) ([]ReGreetCandidate, error) {
	positionID = strings.TrimSpace(positionID)
	if positionID == "" {
		return nil, fmt.Errorf("岗位 ID 不能为空")
	}
	if platform == "" {
		platform = "boss"
	}
	body := map[string]any{
		"platform":             platform,
		"time_range_days":      timeRangeDays,
		"interval_min_minutes": intervalMinMinutes,
		"max_count":            maxCount,
	}
	payload, code, err := c.postAuthed(ctx, token, "/api/positions/"+url.PathEscape(positionID)+"/re-greet-candidates", body)
	if err != nil {
		return nil, fmt.Errorf("拉取复打名单失败：%w", err)
	}
	if code >= 400 {
		return nil, fmt.Errorf("%s", cloudMessage(payload, "拉取复打名单失败"))
	}
	rawItems, _ := payload["items"].([]any)
	if len(requireReceipt) > 0 && requireReceipt[0] && payload["re_greet_receipts"] != true {
		return nil, fmt.Errorf("云端版本尚不支持可靠复打收据，请更新后重试")
	}
	items := make([]ReGreetCandidate, 0, len(rawItems))
	for _, raw := range rawItems {
		data, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		items = append(items, ReGreetCandidate{
			GreetedAt:           stringFromMap(data, "greeted_at"),
			LastReGreetedAt:     stringFromMap(data, "last_re_greeted_at"),
			ID:                  stringFromMap(data, "id"),
			PositionID:          stringFromMap(data, "position_id"),
			Platform:            stringFromMap(data, "platform"),
			PlatformCandidateID: stringFromMap(data, "platform_candidate_id"),
			CandidateName:       stringFromMap(data, "candidate_name"),
			ReGreetCount:        intFromMap(data, "re_greet_count"),
		})
	}
	return items, nil
}

// ReportReGreetResult 上报单个候选人的复打招呼结果。
// success 为 true 时云端会将 last_re_greeted_at 更新为 now() 并对 re_greet_count +1。
func (c *Client) ReportReGreetResult(ctx context.Context, token string, positionID string, platform string, candidateID string, candidateName string, success bool, reason string, details ...ReGreetReportDetails) error {
	positionID = strings.TrimSpace(positionID)
	if positionID == "" {
		return fmt.Errorf("岗位 ID 不能为空")
	}
	candidateID = strings.TrimSpace(candidateID)
	if candidateID == "" {
		return fmt.Errorf("候选人 ID 不能为空")
	}
	if platform == "" {
		platform = "boss"
	}
	body := map[string]any{
		"platform":              platform,
		"platform_candidate_id": candidateID,
		"candidate_name":        candidateName,
		"success":               success,
	}
	if reason != "" {
		body["reason"] = reason
	}
	if len(details) > 0 {
		body["message_text"] = details[0].MessageText
		body["run_id"] = details[0].RunID
	}
	payload, code, err := c.postAuthed(ctx, token, "/api/positions/"+url.PathEscape(positionID)+"/re-greet-report", body)
	if err != nil {
		return fmt.Errorf("上报复打结果失败：%w", err)
	}
	if code >= 400 {
		return fmt.Errorf("%s", cloudMessage(payload, "上报复打结果失败"))
	}
	return nil
}

// ReGreetReportDetails 保存复打发送内容与本次云端许可记录，兼容旧客户端的简要上报。
type ReGreetReportDetails struct {
	MessageText string
	RunID       string
}

// CandidateInfoFeedback 上报独立索要结果，只有身份摘要、动作和状态，不包含电话或微信号。
type CandidateInfoFeedback struct {
	RequestID     string `json:"request_id"`
	CandidateID   string `json:"candidate_id"`
	CandidateName string `json:"candidate_name"`
	Action        string `json:"action"`
	State         string `json:"state"`
}

// NotifyCandidateInfoResults 复用索要结果接口，未匹配档案时保持本地待同步，不重新执行页面动作。
func (c *Client) NotifyCandidateInfoResults(ctx context.Context, token, positionID, runID string, items []CandidateInfoFeedback) error {
	payload, code, err := c.postAuthed(ctx, token, "/api/positions/"+url.PathEscape(positionID)+"/resume-requests", map[string]any{"run_id": runID, "info_results": items})
	if err != nil {
		return err
	}
	if code >= 400 {
		return fmt.Errorf("%s", cloudMessage(payload, "索要结果暂未同步"))
	}
	if ok, _ := payload["ok"].(bool); !ok || intFromMap(payload, "saved") != len(items) {
		return fmt.Errorf("索要结果的候选人档案尚未匹配，保留待补报")
	}
	return nil
}

// getAuthed 使用 Bearer Token 请求云端接口。
// ctx 为请求上下文，token 为登录令牌，path 为以 / 开头的云端路径。
func (c *Client) getAuthed(ctx context.Context, token string, path string) (map[string]any, int, error) {
	baseURL, err := c.safeBaseURL()
	if err != nil {
		return nil, 0, err
	}
	token = strings.TrimSpace(token)
	if token == "" {
		return nil, 0, fmt.Errorf("请先登录后再操作")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+path, nil)
	if err != nil {
		return nil, 0, fmt.Errorf("创建云端请求失败：%w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	return c.doJSON(req)
}

// postAuthed 使用 Bearer Token 向云端提交 JSON。
// ctx 为请求上下文，token 为登录令牌，path 为云端路径，body 为请求体。
func (c *Client) postAuthed(ctx context.Context, token string, path string, body any) (map[string]any, int, error) {
	baseURL, err := c.safeBaseURL()
	if err != nil {
		return nil, 0, err
	}
	token = strings.TrimSpace(token)
	if token == "" {
		return nil, 0, fmt.Errorf("请先登录后再操作")
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, 0, fmt.Errorf("请求内容不是有效 JSON：%w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+path, bytes.NewReader(payload))
	if err != nil {
		return nil, 0, fmt.Errorf("创建云端请求失败：%w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	return c.doJSON(req)
}

// safeBaseURL 校验并规范化云端接口地址。
// 返回值不包含末尾斜杠。
func (c *Client) safeBaseURL() (string, error) {
	raw := strings.TrimSpace(c.BaseURL)
	if raw == "" {
		return "", fmt.Errorf("云端接口地址不能为空")
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", fmt.Errorf("云端接口地址格式不正确")
	}
	return strings.TrimRight(raw, "/"), nil
}

// doJSON 执行请求并解析 JSON 响应。
// req 为 HTTP 请求，返回响应体和状态码。
func (c *Client) doJSON(req *http.Request) (map[string]any, int, error) {
	client := c.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, resp.StatusCode, err
	}
	if len(body) == 0 {
		return map[string]any{}, resp.StatusCode, nil
	}
	payload := map[string]any{}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, resp.StatusCode, fmt.Errorf("云端返回格式不是 JSON")
	}
	return payload, resp.StatusCode, nil
}

// configList 将原始值转换为平台配置列表。
// value 为响应里的 configs 字段。
func configList(value any) ([]map[string]any, bool) {
	items, ok := value.([]any)
	if !ok {
		return nil, false
	}
	result := make([]map[string]any, 0, len(items))
	for _, item := range items {
		if config, ok := item.(map[string]any); ok {
			result = append(result, config)
		}
	}
	return result, true
}

// decodeConfigValue 解码 system_configs.config_value。
// value 可以是 JSON 字符串，也可以是对象。
func decodeConfigValue(value any) (PlatformConfig, error) {
	if config, ok := value.(map[string]any); ok {
		return config, nil
	}
	text, ok := value.(string)
	if !ok || strings.TrimSpace(text) == "" {
		return nil, fmt.Errorf("云端平台配置内容不是有效对象")
	}
	config := map[string]any{}
	if err := json.Unmarshal([]byte(text), &config); err != nil {
		return nil, fmt.Errorf("云端平台配置 JSON 格式不正确")
	}
	return config, nil
}

// cloudMessage 提取云端错误消息。
// payload 为云端返回体，fallback 为默认中文错误。
func cloudMessage(payload map[string]any, fallback string) string {
	for _, key := range []string{"msg", "message", "error"} {
		if text := stringFromMap(payload, key); text != "" {
			return translateKnownMessage(text)
		}
	}
	return fallback
}

// ErrorMessage 提取云端响应中的用户可见错误消息。
// payload 为云端返回体，fallback 为默认中文错误。
func ErrorMessage(payload map[string]any, fallback string) string {
	return cloudMessage(payload, fallback)
}

// stringFromMap 从 map 中读取字符串字段。
// item 为原始字典，key 为字段名。
func stringFromMap(item map[string]any, key string) string {
	if item == nil {
		return ""
	}
	if value, ok := item[key].(string); ok {
		return strings.TrimSpace(value)
	}
	return ""
}

// intFromMap 安全提取 map 中的数字字段，类型不匹配时返回 0。
func intFromMap(item map[string]any, key string) int {
	if item == nil {
		return 0
	}
	switch v := item[key].(type) {
	case float64:
		return int(v)
	case int:
		return v
	case int64:
		return int(v)
	}
	return 0
}

// translateKnownMessage 把常见英文错误改成中文。
// text 为云端或底层返回的原始错误。
func translateKnownMessage(text string) string {
	switch strings.TrimSpace(text) {
	case "session is invalid or expired":
		return "登录已过期，请重新登录"
	case "subscription_expired":
		return "会员已过期，请先续费"
	case "failed to load subscription":
		return "读取会员状态失败"
	case "failed to load system configs":
		return "读取平台配置失败"
	default:
		return text
	}
}

// SendPositionFailNotice 通知云端岗位运行失败，由云端按登录用户发送邮件。
// ctx 为请求上下文，其余参数为登录信息、岗位、失败原因和本次打招呼数量。
func (c *Client) SendPositionFailNotice(ctx context.Context, token string, positionID string, errorMsg string, runGreetedCount int) error {
	baseURL, err := c.safeBaseURL()
	if err != nil {
		log.Printf("[失败邮件] 获取云端地址失败：%v", err)
		return err
	}
	token = strings.TrimSpace(token)
	if token == "" {
		return fmt.Errorf("登录已过期，无法发送失败邮件通知")
	}
	apiURL := strings.TrimSuffix(baseURL, "/") + "/api/fail-notice"
	body := map[string]any{
		"position_id":       positionID,
		"error_message":     errorMsg,
		"run_greeted_count": max(0, runGreetedCount),
	}
	payload, err := json.Marshal(body)
	if err != nil {
		log.Printf("[失败邮件] JSON 序列化失败：%v", err)
		return err
	}
	log.Printf("[失败邮件] 请求地址：%s", apiURL)
	log.Printf("[失败邮件] 请求参数：%s", string(payload))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, bytes.NewReader(payload))
	if err != nil {
		log.Printf("[失败邮件] 创建请求失败：%v", err)
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	resp, code, err := c.doJSON(req)
	if err != nil {
		log.Printf("[失败邮件] 请求失败：%v", err)
		return err
	}
	log.Printf("[失败邮件] 响应状态码：%d", code)
	log.Printf("[失败邮件] 响应数据：%v", resp)
	if code != http.StatusOK && code != http.StatusAccepted {
		return fmt.Errorf("云端返回非预期状态码：%d，原因：%s", code, cloudMessage(resp, "未返回具体原因"))
	}
	return nil
}

// BindDevice 把当前设备绑定到云端账号。
// ctx 为请求上下文，token 为登录令牌，machineID 为设备 ID，agentVersion 为本地程序版本，localPort 为本地端口。
// 返回响应体、HTTP 状态码和错误。调用方可通过状态码判断冲突（409）等业务场景。
func (c *Client) BindDevice(ctx context.Context, token string, machineID string, agentVersion string, localPort int) (map[string]any, int, error) {
	baseURL, err := c.safeBaseURL()
	if err != nil {
		return nil, 0, err
	}
	token = strings.TrimSpace(token)
	if token == "" {
		return nil, 0, fmt.Errorf("登录已过期，请重新登录")
	}
	machineID = strings.TrimSpace(machineID)
	if machineID == "" {
		return nil, 0, fmt.Errorf("设备 ID 不能为空")
	}
	apiURL := strings.TrimSuffix(baseURL, "/") + "/api/agents/bind"
	body := map[string]any{
		"machine_id":    machineID,
		"agent_version": agentVersion,
		"local_port":    localPort,
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, 0, fmt.Errorf("请求内容不是有效 JSON：%w", err)
	}
	log.Printf("[设备绑定] 请求地址：%s", apiURL)
	log.Printf("[设备绑定] 请求参数：%s", string(payload))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, bytes.NewReader(payload))
	if err != nil {
		return nil, 0, fmt.Errorf("创建云端请求失败：%w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	resp, code, err := c.doJSON(req)
	if err != nil {
		log.Printf("[设备绑定] 请求失败：%v", err)
		return nil, 0, err
	}
	log.Printf("[设备绑定] 响应状态码：%d", code)
	log.Printf("[设备绑定] 响应数据：%v", resp)
	return resp, code, nil
}
