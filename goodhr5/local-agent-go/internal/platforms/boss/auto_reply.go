// 本文件实现 Boss 可选会话能力；仅使用本地内嵌配置和通用 Locator 动作。
package boss

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	"goodhr5/local-agent-go/internal/platformcore"
)

//go:embed config.json
var replyConfigJSON []byte

// replyPageConfig 保存经过页面验证的消息配置，不接受云端选择器覆盖。
type replyPageConfig struct {
	Jobs               platformcore.SelectorSpec             `json:"jobs"`
	JobFields          map[string]platformcore.SelectorField `json:"job_fields"`
	Verified           bool                                  `json:"verified"`
	UnavailableReason  string                                `json:"unavailable_reason"`
	MessagesURL        string                                `json:"messages_url"`
	Unread             platformcore.SelectorSpec             `json:"unread"`
	Conversation       platformcore.SelectorSpec             `json:"conversation"`
	Active             platformcore.SelectorSpec             `json:"active"`
	Messages           platformcore.SelectorSpec             `json:"messages"`
	Input              platformcore.SelectorSpec             `json:"input"`
	Send               platformcore.SelectorSpec             `json:"send"`
	IdentityAttribute  string                                `json:"identity_attribute"`
	ConversationFields map[string]platformcore.SelectorField `json:"conversation_fields"`
	MessageFields      map[string]platformcore.SelectorField `json:"message_fields"`
	Directions         map[string]string                     `json:"directions"`
	Kinds              map[string]string                     `json:"kinds"`
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
func (r *Runtime) PrepareReplyPage(ctx context.Context, exec platformcore.Executor) error {
	if err := r.AutoReplyAvailable(); err != nil {
		return err
	}
	_, err := exec.Post(ctx, "/api/v1/page/open", struct {
		URL      string `json:"url"`
		NoScript bool   `json:"no_script"`
	}{r.replyPageSettings().MessagesURL, true})
	return err
}

// ResolveReplyTarget 从经过验证的完整岗位列表匹配唯一名称，不把云端岗位 UUID 当作平台岗位 ID。
func (r *Runtime) ResolveReplyTarget(ctx context.Context, exec platformcore.Executor, name string) (platformcore.ReplyTarget, error) {
	if err := r.AutoReplyAvailable(); err != nil { return platformcore.ReplyTarget{}, err }
	cfg := r.replyPageSettings()
	fields, err := replyFields(ctx, exec, platformcore.LocatorRequest{Selector: cfg.Jobs, Fields: cfg.JobFields, MaxItems: 1000})
	if err != nil { return platformcore.ReplyTarget{}, err }
	if name == "" || len(fields) >= 1000 { return platformcore.ReplyTarget{}, platformcore.ErrReplyUnsafe }
	matches := 0
	target := platformcore.ReplyTarget{}
	for _, field := range fields {
		if field["name"] == name {
			matches++
			target = platformcore.ReplyTarget{PositionID: field["id"], PositionName: name, NameUnique: true}
		}
	}
	if matches != 1 { return platformcore.ReplyTarget{}, platformcore.ErrReplyUnsafe }
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
	return r.readCurrentReply(ctx, exec, target, conversation)
}

// readCurrentReply 读取已打开的面板，不在复核时自动切回原会话。
func (r *Runtime) readCurrentReply(ctx context.Context, exec platformcore.Executor, target platformcore.ReplyTarget, conversation platformcore.ReplyConversation) (platformcore.ReplyContext, error) {
	if err := r.AutoReplyAvailable(); err != nil {
		return platformcore.ReplyContext{}, err
	}
	cfg := r.replyPageSettings()
	identities, err := replyFields(ctx, exec, platformcore.LocatorRequest{Selector: cfg.Active, Fields: cfg.ConversationFields, MaxItems: 2})
	if err != nil {
		return platformcore.ReplyContext{}, err
	}
	if len(identities) != 1 {
		return platformcore.ReplyContext{}, platformcore.ErrReplyUnsafe
	}
	current := replyConversationFromFields(identities[0])
	if current.ID != conversation.ID || !platformcore.ReplyPositionMatches(current, target) {
		return platformcore.ReplyContext{}, platformcore.ErrReplyUnsafe
	}
	parent := replyIdentitySelector(cfg.Active, cfg.IdentityAttribute, current.ID)
	fields, err := replyFields(ctx, exec, platformcore.LocatorRequest{Selector: replyChildSelector(cfg.Messages, parent), Fields: cfg.MessageFields, MaxItems: 1000})
	if err != nil {
		return platformcore.ReplyContext{}, err
	}
	if len(fields) >= 1000 {
		return platformcore.ReplyContext{}, platformcore.ErrReplyUnsafe
	}
	value := platformcore.ReplyContext{Conversation: current}
	for _, field := range fields {
		value.Messages = append(value.Messages, platformcore.ReplyMessage{ID: field["id"], Direction: cfg.Directions[field["direction"]], Kind: cfg.Kinds[field["kind"]], Timestamp: field["timestamp"], Text: field["text"]})
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
	parent := replyIdentitySelector(cfg.Active, cfg.IdentityAttribute, expected.Conversation.ID)
	_, err = exec.Post(ctx, "/api/v1/page/type", platformcore.LocatorRequest{Selector: replyChildSelector(cfg.Input, parent), Text: text})
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
	parent := replyIdentitySelector(cfg.Active, cfg.IdentityAttribute, expected.Conversation.ID)
	_, err = exec.Post(ctx, "/api/v1/page/click", platformcore.LocatorRequest{Selector: replyChildSelector(cfg.Send, parent)})
	return true, err
}

// ConfirmReply 仅认可目标入站消息之后新增的我方文本，不以点击成功或输入框清空作依据。
func (r *Runtime) ConfirmReply(ctx context.Context, exec platformcore.Executor, target platformcore.ReplyTarget, conversation platformcore.ReplyConversation, inbound, replyHash string) (bool, error) {
	current, err := r.readCurrentReply(ctx, exec, target, conversation)
	if err != nil {
		return false, err
	}
	found := false
	for _, message := range current.Messages {
		if platformcore.ReplyMessageFingerprint(conversation.ID, message) == inbound {
			found = true
			continue
		}
		if !found {
			continue
		}
		if message.Direction == "inbound" {
			return false, nil
		}
		if message.Direction == "outbound" && message.Kind == "text" && platformcore.ReplyHash(strings.TrimSpace(message.Text)) == replyHash {
			return true, nil
		}
	}
	return false, nil
}

var _ platformcore.AutoReplyRuntime = (*Runtime)(nil)
