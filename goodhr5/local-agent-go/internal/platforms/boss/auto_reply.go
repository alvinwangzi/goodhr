// 本文件实现 Boss 可选会话能力；仅使用本地内嵌配置和通用 Locator 动作。
package boss

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"
	"unicode/utf8"

	"goodhr5/local-agent-go/internal/localai"
	"goodhr5/local-agent-go/internal/platformcore"
)

//go:embed config.json
var replyConfigJSON []byte

// replyPageConfig 保存经过页面验证的消息配置，不接受云端选择器覆盖。
type replyPageConfig struct {
	Jobs                platformcore.SelectorSpec             `json:"jobs"`
	JobFields           map[string]platformcore.SelectorField `json:"job_fields"`
	Verified            bool                                  `json:"verified"`
	UnavailableReason   string                                `json:"unavailable_reason"`
	MessagesURL         string                                `json:"messages_url"`
	Unread              platformcore.SelectorSpec             `json:"unread"`
	Conversation        platformcore.SelectorSpec             `json:"conversation"`
	Active              platformcore.SelectorSpec             `json:"active"`
	Messages            platformcore.SelectorSpec             `json:"messages"`
	Input               platformcore.SelectorSpec             `json:"input"`
	Send                platformcore.SelectorSpec             `json:"send"`
	IdentityAttribute   string                                `json:"identity_attribute"`
	ConversationFields  map[string]platformcore.SelectorField `json:"conversation_fields"`
	MessageFields       map[string]platformcore.SelectorField `json:"message_fields"`
	Directions          map[string]string                     `json:"directions"`
	Kinds               map[string]string                     `json:"kinds"`
	ResumeReceived      platformcore.SelectorSpec             `json:"resume_received"`
	ResumeRequestedText string                                `json:"resume_requested_text"`
	PendingResumeAccept platformcore.SelectorSpec             `json:"pending_resume_accept"`
	ResumeButton        platformcore.SelectorSpec             `json:"resume_button"`
	OnlineResumeButton  platformcore.SelectorSpec             `json:"online_resume_button"`
	OnlineResumeOverlay platformcore.SelectorSpec             `json:"online_resume_overlay"`
	OnlineResumeClose   platformcore.SelectorSpec             `json:"online_resume_close"`
	PanelInfo           platformcore.SelectorSpec             `json:"panel_info"`
}

// replyPageSettings 读取运行时的本地配置，配置错误保守地保持不可用。
func (r *Runtime) replyPageSettings() replyPageConfig {
	if r.replyConfig != nil {
		return *r.replyConfig
	}
	var cfg replyPageConfig
	_ = json.Unmarshal(replyConfigJSON, &cfg)
	return cfg
}

// AutoReplyAvailable 在任何页面动作前检查真实页面配置是否经过验证。
func (r *Runtime) AutoReplyAvailable() error {
	cfg := r.replyPageSettings()
	if !cfg.Verified {
		if cfg.UnavailableReason != "" {
			return fmt.Errorf("%s", cfg.UnavailableReason)
		}
		return fmt.Errorf("Boss 自动回复页面配置尚未验证")
	}
	if cfg.MessagesURL == "" || cfg.IdentityAttribute == "" || len(cfg.Directions) == 0 || len(cfg.Kinds) == 0 {
		return fmt.Errorf("Boss 自动回复字段配置不完整")
	}
	for _, spec := range []platformcore.SelectorSpec{cfg.Jobs, cfg.Unread, cfg.Conversation, cfg.Active, cfg.Messages, cfg.Input, cfg.Send} {
		if len(spec.Selectors) == 0 {
			return fmt.Errorf("Boss 自动回复选择器不完整")
		}
	}
	for _, key := range []string{"id", "position_id", "position_name", "name"} {
		if _, ok := cfg.ConversationFields[key]; !ok {
			return fmt.Errorf("Boss 会话字段不完整")
		}
	}
	for _, key := range []string{"id", "name"} {
		if _, ok := cfg.JobFields[key]; !ok {
			return fmt.Errorf("Boss 岗位识别字段不完整")
		}
	}
	for _, key := range []string{"id", "direction", "kind", "timestamp", "text"} {
		if _, ok := cfg.MessageFields[key]; !ok {
			return fmt.Errorf("Boss 消息字段不完整")
		}
	}
	return nil
}

// PrepareReplyPage 打开配置中的消息页，不默认打开任何未读会话。
// 导航后等待岗位下拉列表渲染完成，避免异步加载导致读取为空。
func (r *Runtime) PrepareReplyPage(ctx context.Context, exec platformcore.Executor) error {
	if err := r.AutoReplyAvailable(); err != nil {
		return err
	}
	_, err := exec.Post(ctx, "/api/v1/page/open", struct {
		URL      string `json:"url"`
		NoScript bool   `json:"no_script"`
	}{r.replyPageSettings().MessagesURL, true})
	if err != nil {
		return err
	}
	// 岗位下拉列表是异步加载的，等待渲染完成。
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(2 * time.Second):
	}
	return nil
}

// ResolveReplyTarget 从经过验证的完整岗位列表匹配唯一名称，不把云端岗位 UUID 当作平台岗位 ID。
// 先点击岗位下拉容器使其展开，读取选项列表进行匹配，然后点击选中目标岗位以过滤会话列表。
func (r *Runtime) ResolveReplyTarget(ctx context.Context, exec platformcore.Executor, name string) (platformcore.ReplyTarget, error) {
	if err := r.AutoReplyAvailable(); err != nil {
		return platformcore.ReplyTarget{}, err
	}
	cfg := r.replyPageSettings()
	// 点击岗位下拉容器，展开选项列表。
	_, _ = exec.Post(ctx, "/api/v1/page/click", platformcore.LocatorRequest{
		Selector: platformcore.SelectorSpec{Selectors: []string{".job-select"}},
	})
	// 等待下拉选项渲染。
	select {
	case <-ctx.Done():
		return platformcore.ReplyTarget{}, ctx.Err()
	case <-time.After(1 * time.Second):
	}
	fields, err := replyFields(ctx, exec, platformcore.LocatorRequest{Selector: cfg.Jobs, Fields: cfg.JobFields, MaxItems: 1000})
	if err != nil {
		return platformcore.ReplyTarget{}, err
	}
	if name == "" || len(fields) >= 1000 {
		return platformcore.ReplyTarget{}, platformcore.ErrReplyUnsafe
	}
	matches := 0
	target := platformcore.ReplyTarget{}
	matchIndex := -1
	for i, field := range fields {
		// 下拉选项文本可能包含城市和薪资后缀（如 "Java开发工程师 _ 南京 8-12K"），
		// 只要包含目标岗位名即视为匹配。
		if field["name"] == name || strings.Contains(field["name"], name) {
			matches++
			target = platformcore.ReplyTarget{PositionID: field["id"], PositionName: name, NameUnique: true}
			matchIndex = i
		}
	}
	if matches != 1 {
		return platformcore.ReplyTarget{}, platformcore.ErrReplyUnsafe
	}
	// 点击选中的岗位选项，过滤会话列表。
	nth := matchIndex
	_, _ = exec.Post(ctx, "/api/v1/page/click", platformcore.LocatorRequest{
		Selector: platformcore.SelectorSpec{Selectors: cfg.Jobs.Selectors, Nth: &nth},
	})
	// 等待会话列表刷新。
	select {
	case <-ctx.Done():
		return platformcore.ReplyTarget{}, ctx.Err()
	case <-time.After(1 * time.Second):
	}
	return target, nil
}

// replyFields 仅在旧 Executor 协议边界解码动态数据。
func replyFields(ctx context.Context, exec platformcore.Executor, request platformcore.LocatorRequest) ([]map[string]string, error) {
	result, err := exec.Post(ctx, "/api/v1/page/find-elements", request)
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal(workerDataMap(result))
	if err != nil {
		return nil, err
	}
	var response struct {
		Items []struct {
			Fields map[string]string `json:"fields"`
		} `json:"items"`
	}
	if err := json.Unmarshal(raw, &response); err != nil {
		return nil, fmt.Errorf("会话页面字段格式错误")
	}
	fields := make([]map[string]string, 0, len(response.Items))
	for _, item := range response.Items {
		fields = append(fields, item.Fields)
	}
	return fields, nil
}

// replyConversationFromFields 把页面字段转成稳定会话模型。
func replyConversationFromFields(fields map[string]string) platformcore.ReplyConversation {
	return platformcore.ReplyConversation{ID: fields["id"], PositionID: fields["position_id"], PositionName: fields["position_name"], Name: fields["name"]}
}

// replyIdentitySelector 添加精确属性约束，不拼接动态姓名或依赖扫描下标。
func replyIdentitySelector(spec platformcore.SelectorSpec, attribute, id string) platformcore.SelectorSpec {
	attributes := make(map[string]string, len(spec.Attributes)+1)
	for key, value := range spec.Attributes {
		attributes[key] = value
	}
	attributes[attribute] = id
	spec.Attributes = attributes
	return spec
}

// replyChildSelector 把消息、输入和发送按钮限定在当前稳定会话内。
func replyChildSelector(child, parent platformcore.SelectorSpec) platformcore.SelectorSpec {
	child.Parent = &parent
	return child
}

// ScanUnreadReplies 只把确认属于当前岗位的稳定会话加入队列，重复身份不入队。
func (r *Runtime) ScanUnreadReplies(ctx context.Context, exec platformcore.Executor, target platformcore.ReplyTarget, limit int) ([]platformcore.ReplyConversation, error) {
	if err := r.AutoReplyAvailable(); err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 100 {
		limit = 100
	}
	cfg := r.replyPageSettings()
	fields, err := replyFields(ctx, exec, platformcore.LocatorRequest{Selector: cfg.Unread, Fields: cfg.ConversationFields, MaxItems: limit})
	if err != nil {
		return nil, err
	}
	counts := map[string]int{}
	for _, item := range fields {
		counts[item["id"]]++
	}
	result := []platformcore.ReplyConversation{}
	for _, item := range fields {
		conversation := replyConversationFromFields(item)
		if counts[conversation.ID] == 1 && platformcore.ReplyPositionMatches(conversation, target) {
			result = append(result, conversation)
		}
	}
	return result, nil
}

// ReadReplyContext 通过稳定标识重新定位会话，点击后再读取当前面板身份。
func (r *Runtime) ReadReplyContext(ctx context.Context, exec platformcore.Executor, target platformcore.ReplyTarget, conversation platformcore.ReplyConversation) (platformcore.ReplyContext, error) {
	if err := r.AutoReplyAvailable(); err != nil {
		return platformcore.ReplyContext{}, err
	}
	if !platformcore.ReplyPositionMatches(conversation, target) {
		return platformcore.ReplyContext{}, platformcore.ErrReplyUnsafe
	}
	cfg := r.replyPageSettings()
	_, err := exec.Post(ctx, "/api/v1/page/click", platformcore.LocatorRequest{Selector: replyIdentitySelector(cfg.Conversation, cfg.IdentityAttribute, conversation.ID)})
	if err != nil {
		return platformcore.ReplyContext{}, err
	}
	// 点击会话后等待面板切换完成。
	select {
	case <-ctx.Done():
		return platformcore.ReplyContext{}, ctx.Err()
	case <-time.After(1500 * time.Millisecond):
	}
	return r.readCurrentReply(ctx, exec, target, conversation)
}

// readCurrentReply 读取已打开的面板，不在复核时自动切回原会话。
// Boss 的展开面板 (.chat-conversation) 没有 data-id 属性，
// 因此直接用面板专属选择器读取姓名和岗位来验证身份。
func (r *Runtime) readCurrentReply(ctx context.Context, exec platformcore.Executor, target platformcore.ReplyTarget, conversation platformcore.ReplyConversation) (platformcore.ReplyContext, error) {
	if err := r.AutoReplyAvailable(); err != nil {
		return platformcore.ReplyContext{}, err
	}
	cfg := r.replyPageSettings()

	// 从展开面板读取姓名和岗位验证身份
	panelFields := map[string]platformcore.SelectorField{
		"name":          {Selector: &platformcore.SelectorSpec{Selectors: []string{".base-name"}}},
		"position_name": {Selector: &platformcore.SelectorSpec{Selectors: []string{".source-job"}}},
	}
	identities, err := replyFields(ctx, exec, platformcore.LocatorRequest{Selector: cfg.Active, Fields: panelFields, MaxItems: 2})
	if err != nil {
		return platformcore.ReplyContext{}, fmt.Errorf("面板字段读取失败: %w", err)
	}
	if len(identities) != 1 {
		return platformcore.ReplyContext{}, fmt.Errorf("面板身份读取异常：identities数量=%d，期望=1，候选人=%s", len(identities), conversation.Name)
	}
	panelName := strings.TrimSpace(identities[0]["name"])
	panelPosition := strings.TrimSpace(identities[0]["position_name"])
	if panelName == "" || panelName != conversation.Name {
		return platformcore.ReplyContext{}, fmt.Errorf("面板姓名不匹配：面板=%q，期望=%q", panelName, conversation.Name)
	}
	if panelPosition != "" && target.PositionName != "" && panelPosition != target.PositionName {
		return platformcore.ReplyContext{}, fmt.Errorf("面板岗位不匹配：面板=%q，期望=%q", panelPosition, target.PositionName)
	}
	current := platformcore.ReplyConversation{
		ID:           conversation.ID,
		PositionID:   target.PositionID,
		PositionName: panelPosition,
		Name:         panelName,
	}

	// 展开面板作为父元素，限定消息/输入/发送在面板内查找
	parent := cfg.Active
	fields, err := replyFields(ctx, exec, platformcore.LocatorRequest{Selector: replyChildSelector(cfg.Messages, parent), Fields: cfg.MessageFields, MaxItems: 1000})
	if err != nil {
		return platformcore.ReplyContext{}, err
	}
	if len(fields) >= 1000 {
		return platformcore.ReplyContext{}, platformcore.ErrReplyUnsafe
	}
	value := platformcore.ReplyContext{Conversation: current}
	for _, field := range fields {
		kind := mapClassToValue(cfg.Kinds, field["kind"])
		direction := mapClassToValue(cfg.Directions, field["direction"])
		if kind == "system" {
			direction = "system"
		}
		// 日期分隔线等非消息元素不含任何消息子节点，方向与类型双双映射为空；
		// 这类条目不能进入消息列表，否则下游方向白名单校验会把整个会话判为不安全。
		if direction == "" && kind == "" {
			continue
		}
		value.Messages = append(value.Messages, platformcore.ReplyMessage{
			ID:        field["id"],
			Direction: direction,
			Kind:      kind,
			Timestamp: field["timestamp"],
			Text:      field["text"],
		})
	}
	draft, err := exec.Post(ctx, "/api/v1/page/extract-text", platformcore.LocatorRequest{Selector: replyChildSelector(cfg.Input, parent), Editable: true})
	if err != nil {
		return platformcore.ReplyContext{}, err
	}
	text, ok := workerDataMap(draft)["text"].(string)
	if !ok {
		return platformcore.ReplyContext{}, platformcore.ErrReplyUnsafe
	}
	value.Draft = text
	value.ResumeStatus = "unknown"
	if len(cfg.ResumeReceived.Selectors) > 0 {
		received, readErr := r.hasElement(ctx, exec, replyChildSelector(cfg.ResumeReceived, cfg.Active))
		if readErr == nil {
			if received {
				value.ResumeStatus = "received"
			} else {
				requested, requestErr := r.hasTextOnPage(ctx, exec, cfg.ResumeRequestedText)
				if requestErr == nil {
					value.ResumeStatus = "none"
					if requested {
						value.ResumeStatus = "requested"
					}
				}
			}
		}
	}
	return value, nil
}

// RecheckReplyContext 核对当前身份、岗位和完整上下文，不允许复用过期答案。
func (r *Runtime) RecheckReplyContext(ctx context.Context, exec platformcore.Executor, target platformcore.ReplyTarget, expected platformcore.ReplyContext) (platformcore.ReplyContext, error) {
	current, err := r.readCurrentReply(ctx, exec, target, expected.Conversation)
	if err != nil {
		return platformcore.ReplyContext{}, err
	}
	current, err = platformcore.ValidateReplyContext(current, target)
	if err != nil {
		return platformcore.ReplyContext{}, err
	}
	if expected.Fingerprint == "" || current.Fingerprint != expected.Fingerprint {
		return platformcore.ReplyContext{}, platformcore.ErrReplyUnsafe
	}
	return current, nil
}

// StageReply 输入前复核上下文和人工草稿；输入动作自身也必须验证空白草稿。
func (r *Runtime) StageReply(ctx context.Context, exec platformcore.Executor, target platformcore.ReplyTarget, expected platformcore.ReplyContext, text string) error {
	current, err := r.RecheckReplyContext(ctx, exec, target, expected)
	if err != nil {
		return err
	}
	if strings.TrimSpace(current.Draft) != "" || strings.TrimSpace(text) == "" || utf8.RuneCountInString(text) > 1000 {
		return platformcore.ErrReplyUnsafe
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	cfg := r.replyPageSettings()
	_, err = exec.Post(ctx, "/api/v1/page/type", platformcore.LocatorRequest{Selector: replyChildSelector(cfg.Input, cfg.Active), Text: text})
	return err
}

// SendReply 输入后再次复核，返回 attempted 表示发送请求是否已经交给 Worker。
func (r *Runtime) SendReply(ctx context.Context, exec platformcore.Executor, target platformcore.ReplyTarget, expected platformcore.ReplyContext, text string) (bool, error) {
	current, err := r.RecheckReplyContext(ctx, exec, target, expected)
	if err != nil {
		return false, err
	}
	if strings.TrimSpace(text) == "" || strings.TrimSpace(current.Draft) != strings.TrimSpace(text) {
		return false, platformcore.ErrReplyUnsafe
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	cfg := r.replyPageSettings()
	_, err = exec.Post(ctx, "/api/v1/page/click", platformcore.LocatorRequest{Selector: replyChildSelector(cfg.Send, cfg.Active)})
	return true, err
}

// ConfirmReply 仅认可目标入站消息之后新增的我方文本，不以点击成功或输入框清空作依据。
// 先用指纹定位入站消息，再检查其后是否有匹配的出站文字；
// 无法定位本次入站消息时不确认成功，不能用历史同文作为发送证据。
func (r *Runtime) ConfirmReply(ctx context.Context, exec platformcore.Executor, target platformcore.ReplyTarget, conversation platformcore.ReplyConversation, inbound, replyHash string) (bool, error) {
	current, err := r.readCurrentReply(ctx, exec, target, conversation)
	if err != nil {
		return false, err
	}
	// 只认可本次候选人消息之后的回复，历史同文不能证明本次发送成功。
	found := false
	for i, message := range current.Messages {
		if message.Direction == "inbound" {
			if found {
				return false, nil
			}
			found = inbound != "" && platformcore.ReplyInboundFingerprint(conversation.ID, current.Messages[:i+1]) == inbound
			continue
		}
		if found && message.Direction == "outbound" && message.Kind == "text" {
			return platformcore.ReplyHash(strings.TrimSpace(message.Text)) == replyHash, nil
		}
	}
	return false, nil
}

// mapClassToValue 把页面提取的 class 属性值（可能包含多个空格分隔的类名）映射到配置中的目标值。
// 先尝试精确匹配，再逐个拆分匹配，支持 "item-myself clearfix" 这样的多类名字符串。
func mapClassToValue(mapping map[string]string, raw string) string {
	if v, ok := mapping[raw]; ok {
		return v
	}
	for _, part := range strings.Fields(raw) {
		if v, ok := mapping[part]; ok {
			return v
		}
	}
	return ""
}

// ResumeAfterReply 在自动回复成功后判断是否索要简历。
// 根据 ReviewProfileBeforeReply 返回的详细简历评分与岗位阈值比较：
// 评分 >= 阈值时点击"求简历"按钮，否则跳过。
func (r *Runtime) ResumeAfterReply(ctx context.Context, exec platformcore.Executor, conversation platformcore.ReplyConversation, positionSnapshot map[string]any, reviewScore int, threshold float64) (string, error) {
	cfg := r.replyPageSettings()

	// 1. 检查简历是否已收到（右上角"附件简历"按钮存在）
	received, _ := r.hasElement(ctx, exec, replyChildSelector(cfg.ResumeReceived, cfg.Active))
	if received {
		exec.Log("info", fmt.Sprintf("索要简历：候选人=%s，已收到简历，跳过", conversation.Name))
		return "skipped", nil
	}

	// 2. 检查是否已发送过索要请求
	requested, _ := r.hasTextOnPage(ctx, exec, cfg.ResumeRequestedText)
	if requested {
		exec.Log("info", fmt.Sprintf("索要简历：候选人=%s，已发送过索要请求，跳过", conversation.Name))
		return "skipped", nil
	}

	// 3. 检查候选人是否已回复（未回复时"求简历"按钮处于 disabled 状态）
	hasInbound := false
	current, err := r.readCurrentReply(ctx, exec, platformcore.ReplyTarget{PositionName: conversation.PositionName}, conversation)
	if err == nil {
		for _, msg := range current.Messages {
			if msg.Direction == "inbound" {
				hasInbound = true
				break
			}
		}
	}
	if !hasInbound {
		exec.Log("info", fmt.Sprintf("索要简历：候选人=%s，候选人未回复，跳过", conversation.Name))
		return "skipped", nil
	}

	// 4. 根据详细简历评分决定是否索要
	if float64(reviewScore) >= threshold {
		if err := r.clickResumeButton(ctx, exec, cfg, conversation.Name); err != nil {
			return "failed", err
		}
		exec.Log("info", fmt.Sprintf("索要简历：候选人=%s，评分=%d，阈值=%.0f，已点击求简历", conversation.Name, reviewScore, threshold))
		return "requested", nil
	}

	exec.Log("info", fmt.Sprintf("索要简历：候选人=%s，评分=%d，阈值=%.0f，评分不通过，跳过", conversation.Name, reviewScore, threshold))
	return "skipped", nil
}

// hasElement 检查选择器是否能在父元素内找到至少一个元素。
func (r *Runtime) hasElement(ctx context.Context, exec platformcore.Executor, selector platformcore.SelectorSpec) (bool, error) {
	result, err := exec.Post(ctx, "/api/v1/page/find-elements", platformcore.LocatorRequest{
		Selector: selector,
		MaxItems: 1,
	})
	if err != nil {
		return false, err
	}
	data := workerDataMap(result)
	items, ok := data["items"].([]any)
	if !ok {
		return false, platformcore.ErrReplyUnsafe
	}
	return len(items) > 0, nil
}

// hasTextOnPage 检查页面是否包含指定文本。
func (r *Runtime) hasTextOnPage(ctx context.Context, exec platformcore.Executor, text string) (bool, error) {
	if text == "" {
		return false, nil
	}
	result, err := exec.Post(ctx, "/api/v1/page/find-elements", platformcore.LocatorRequest{
		Selector: r.replyPageSettings().Active,
		Fields: map[string]platformcore.SelectorField{
			"text": {},
		},
		MaxItems: 1,
	})
	if err != nil {
		return false, err
	}
	data := workerDataMap(result)
	items, ok := data["items"].([]any)
	if !ok || len(items) != 1 {
		return false, platformcore.ErrReplyUnsafe
	}
	m, ok := items[0].(map[string]any)
	if !ok {
		return false, platformcore.ErrReplyUnsafe
	}
	fields, ok := m["fields"].(map[string]any)
	if !ok {
		return false, platformcore.ErrReplyUnsafe
	}
	pageText, ok := fields["text"].(string)
	if !ok {
		return false, platformcore.ErrReplyUnsafe
	}
	return strings.Contains(pageText, text), nil
}

// clickResumeButton 点击"求简历"按钮并处理确认弹窗。
func (r *Runtime) clickResumeButton(ctx context.Context, exec platformcore.Executor, cfg replyPageConfig, name string) error {
	_, err := exec.Post(ctx, "/api/v1/page/click", platformcore.LocatorRequest{
		Selector: replyChildSelector(cfg.ResumeButton, cfg.Active),
		Text:     "求简历",
	})
	if err != nil {
		return fmt.Errorf("点击求简历按钮失败：%w", err)
	}
	// 等待并点击确认弹窗
	_ = exec.Delay(ctx, "等待求简历确认弹窗", 0.5)
	_, _ = exec.Post(ctx, "/api/v1/page/click", platformcore.LocatorRequest{
		Selector: platformcore.SelectorSpec{Selectors: []string{".boss-btn-primary"}},
	})
	exec.Log("info", fmt.Sprintf("自动回复索要简历：候选人=%s，求简历点击完成", name))
	return nil
}

// evaluateResumeWithAI 打开在线简历弹层，截图后交给 AI 评分，返回是否超过阈值。
func (r *Runtime) evaluateResumeWithAI(ctx context.Context, exec platformcore.Executor, cfg replyPageConfig, name string, positionSnapshot map[string]any, client *localai.Client, screenshotsDir string) (bool, error) {
	// 点击"在线简历"打开弹层
	_, err := exec.Post(ctx, "/api/v1/page/click", platformcore.LocatorRequest{
		Selector: replyChildSelector(cfg.OnlineResumeButton, cfg.Active),
	})
	if err != nil {
		return false, fmt.Errorf("点击在线简历按钮失败：%w", err)
	}
	_ = exec.Delay(ctx, "等待在线简历弹层打开", 1.5)

	// 截图在线简历弹层
	screenshotResult, err := exec.Post(ctx, "/api/v1/page/screenshot", map[string]any{
		"selector":    ".resume-detail-wrap",
		"scroll_full": true,
		"filename":    fmt.Sprintf("resume-eval-%s.png", name),
		"directory":   screenshotsDir,
	})
	if err != nil {
		r.closeOnlineResume(ctx, exec, cfg)
		return false, fmt.Errorf("在线简历截图失败：%w", err)
	}

	// 关闭弹层
	r.closeOnlineResume(ctx, exec, cfg)

	// 读取截图文件
	screenshotData := workerDataMap(screenshotResult)
	filePath := stringFromMap(screenshotData, "file_path")
	if filePath == "" {
		filePath = stringFromMap(screenshotData, "path")
	}
	if filePath == "" {
		return false, fmt.Errorf("在线简历截图路径为空")
	}
	imageBytes, err := os.ReadFile(filePath)
	if err != nil {
		return false, fmt.Errorf("读取在线简历截图失败：%w", err)
	}

	// AI 视觉评分（复用 ScoreVisionForGreet，它已支持图片识别+评分）
	candidate := map[string]any{"candidate_name": name}
	decision, err := client.ScoreVisionForGreet(ctx, positionSnapshot, candidate, imageBytes)
	if err != nil {
		return false, fmt.Errorf("AI 简历评分失败：%w", err)
	}
	exec.Log("info", fmt.Sprintf("自动回复索要简历：候选人=%s，AI简历评分=%.1f，阈值=%.1f，原因=%s", name, decision.Score, decision.Threshold, decision.Reason))
	return decision.ShouldGreet, nil
}

// closeOnlineResume 关闭在线简历弹层。
func (r *Runtime) closeOnlineResume(ctx context.Context, exec platformcore.Executor, cfg replyPageConfig) {
	_, _ = exec.Post(ctx, "/api/v1/page/click", platformcore.LocatorRequest{
		Selector: cfg.OnlineResumeClose,
	})
	_ = exec.Delay(ctx, "等待在线简历弹层关闭", 0.3)
}

// ReviewProfileBeforeReply 在生成回复前评估候选人是否值得回复。
// 分两步：
//  1. 面板初筛：读面板基本信息（学历、工作年限等），让 AI 判断岗位硬性条件是否满足。
//     不满足则直接返回低分拒绝，不打开详细简历。
//  2. 详细简历评分：面板通过后打开在线简历弹层截图，交 AI 视觉评分，返回最终分数。
//
// 最终分数由调用方与岗位阈值比较决定是否要简历。
func (r *Runtime) ReviewProfileBeforeReply(ctx context.Context, exec platformcore.Executor, conversation platformcore.ReplyConversation, positionSnapshot map[string]any, aiClient any, screenshotsDir string) (int, string, error) {
	cfg := r.replyPageSettings()

	// 检查简历是否已收到（已有附件简历说明简历在手）
	received, _ := r.hasElement(ctx, exec, replyChildSelector(cfg.ResumeReceived, cfg.Active))
	if received {
		return 100, "简历已收到", nil
	}

	// AI 客户端检查
	client, ok := aiClient.(*localai.Client)
	if !ok || client == nil {
		return -1, "AI 客户端不可用", nil
	}

	// ── 第1步：面板初筛（硬性条件快筛） ──
	panelText := r.readPanelInfoText(ctx, exec, cfg)
	if panelText != "" {
		candidate := map[string]any{
			"candidate_name": conversation.Name,
			"raw_text":       panelText,
		}
		decision, err := client.ScoreForGreet(ctx, positionSnapshot, candidate)
		if err == nil {
			exec.Log("info", fmt.Sprintf("面板初筛：候选人=%s，面板文本=%q，评分=%.1f，阈值=%.1f，原因=%s",
				conversation.Name, panelText, decision.Score, decision.Threshold, decision.Reason))
			if !decision.ShouldGreet {
				// 硬性条件不满足，直接拒绝，不打开详细简历
				return int(decision.Score), decision.Reason, nil
			}
			exec.Log("info", fmt.Sprintf("面板初筛通过，打开详细简历评分：候选人=%s", conversation.Name))
		}
	}

	// ── 第2步：详细简历评分（打开在线简历弹层截图） ──
	hasButton, _ := r.hasElement(ctx, exec, replyChildSelector(cfg.OnlineResumeButton, cfg.Active))
	if !hasButton {
		return -1, "在线简历按钮不存在", nil
	}

	_, err := exec.Post(ctx, "/api/v1/page/click", platformcore.LocatorRequest{
		Selector: replyChildSelector(cfg.OnlineResumeButton, cfg.Active),
	})
	if err != nil {
		return -1, "", fmt.Errorf("点击在线简历按钮失败：%w", err)
	}
	_ = exec.Delay(ctx, "等待在线简历弹层打开", 1.5)

	screenshotResult, err := exec.Post(ctx, "/api/v1/page/screenshot", map[string]any{
		"selector":    ".resume-detail-wrap",
		"scroll_full": true,
		"filename":    fmt.Sprintf("pre-reply-review-%s.png", conversation.Name),
		"directory":   screenshotsDir,
	})
	if err != nil {
		r.closeOnlineResume(ctx, exec, cfg)
		return -1, "", fmt.Errorf("在线简历截图失败：%w", err)
	}

	r.closeOnlineResume(ctx, exec, cfg)

	screenshotData := workerDataMap(screenshotResult)
	filePath := stringFromMap(screenshotData, "file_path")
	if filePath == "" {
		filePath = stringFromMap(screenshotData, "path")
	}
	if filePath == "" {
		return -1, "", fmt.Errorf("在线简历截图路径为空")
	}
	imageBytes, err := os.ReadFile(filePath)
	if err != nil {
		return -1, "", fmt.Errorf("读取在线简历截图失败：%w", err)
	}

	candidate := map[string]any{"candidate_name": conversation.Name}
	decision, err := client.ScoreVisionForGreet(ctx, positionSnapshot, candidate, imageBytes)
	if err != nil {
		return -1, "", fmt.Errorf("AI 简历评分失败：%w", err)
	}
	exec.Log("info", fmt.Sprintf("详细简历评分：候选人=%s，评分=%.1f，阈值=%.1f，原因=%s", conversation.Name, decision.Score, decision.Threshold, decision.Reason))
	return int(decision.Score), decision.Reason, nil
}

// readPanelInfoText 从聊天面板读取候选人基本信息文本（姓名、年龄、学历、工作年限等）。
func (r *Runtime) readPanelInfoText(ctx context.Context, exec platformcore.Executor, cfg replyPageConfig) string {
	if len(cfg.PanelInfo.Selectors) == 0 {
		exec.Log("info", "面板初筛：panel_info 选择器为空，跳过面板读取")
		return ""
	}
	result, err := exec.Post(ctx, "/api/v1/page/find-elements", platformcore.LocatorRequest{
		Selector: replyChildSelector(cfg.PanelInfo, cfg.Active),
		Fields: map[string]platformcore.SelectorField{
			"text": {},
		},
		MaxItems: 1,
	})
	if err != nil {
		exec.Log("warning", fmt.Sprintf("面板初筛：读取面板信息失败，错误=%s", err.Error()))
		return ""
	}
	data := workerDataMap(result)
	items, ok := data["items"].([]any)
	if !ok || len(items) == 0 {
		exec.Log("info", "面板初筛：面板信息元素未找到")
		return ""
	}
	item, ok := items[0].(map[string]any)
	if !ok {
		return ""
	}
	fields, ok := item["fields"].(map[string]any)
	if !ok {
		return ""
	}
	text, _ := fields["text"].(string)
	text = strings.TrimSpace(text)
	exec.Log("info", fmt.Sprintf("面板初筛：读取到面板文本=%q", text))
	return text
}

// HasPendingResumeOffer 检测聊天面板中是否存在"对方想发送附件简历给您"的待接受提示。
// 返回 true 表示候选人已主动发起简历发送请求，等待我方点击"同意"。
// 检测失败时返回 (false, nil)，不中断主流程。
func (r *Runtime) HasPendingResumeOffer(ctx context.Context, exec platformcore.Executor) (bool, error) {
	cfg := r.replyPageSettings()
	pending, err := r.hasElement(ctx, exec, replyChildSelector(cfg.PendingResumeAccept, cfg.Active))
	if err != nil {
		// 检测失败不中断流程，当作无待接受简历处理
		exec.Log("warning", fmt.Sprintf("待接受简历检测失败：错误=%s", err.Error()))
		return false, nil
	}
	exec.Log("info", fmt.Sprintf("待接受简历检测：当前提示条同意按钮存在=%t", pending))
	return pending, nil
}

// AcceptPendingResumeOffer 点击聊天面板中"对方想发送附件简历给您"提示旁边的"同意"按钮。
func (r *Runtime) AcceptPendingResumeOffer(ctx context.Context, exec platformcore.Executor) error {
	cfg := r.replyPageSettings()
	// 点击"同意"按钮（.btn 文本为"同意"）
	_, err := exec.Post(ctx, "/api/v1/page/click", platformcore.LocatorRequest{
		Selector: replyChildSelector(cfg.PendingResumeAccept, cfg.Active),
	})
	if err != nil {
		return fmt.Errorf("点击同意接收简历按钮失败：%w", err)
	}
	exec.Log("info", "已点击同意接收候选人附件简历")
	return nil
}

var _ platformcore.AutoReplyRuntime = (*Runtime)(nil)
