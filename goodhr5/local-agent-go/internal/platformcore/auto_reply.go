// 本文件定义可选自动回复能力、强类型浏览器协议和消息安全校验，不包含平台分支。
package platformcore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

// ErrReplyUnsafe 表示身份、岗位、消息或人工草稿无法安全确认。
var ErrReplyUnsafe = errors.New("会话、岗位或消息已变化，已跳过回复")

// SelectorSpec 定义标准 Locator 的选择器、范围、精确文本和属性约束。
type SelectorSpec struct {
	Selectors  []string          `json:"selectors"`
	Parent     *SelectorSpec     `json:"parent,omitempty"`
	Frame      string            `json:"frame,omitempty"`
	Nth        *int              `json:"nth,omitempty"`
	Text       string            `json:"text,omitempty"`
	Attributes map[string]string `json:"attributes,omitempty"`
}

// SelectorField 定义从元素自身或唯一子元素读取的字段。
type SelectorField struct {
	Selector  *SelectorSpec `json:"selector,omitempty"`
	Attribute string        `json:"attribute,omitempty"`
	Editable  bool          `json:"editable,omitempty"`
}

// LocatorRequest 为旧 Worker 动态边界提供新路径的强类型请求。
type LocatorRequest struct {
	Selector SelectorSpec             `json:"selector_spec"`
	Fields   map[string]SelectorField `json:"fields,omitempty"`
	MaxItems int                      `json:"max_items,omitempty"`
	Text     string                   `json:"text,omitempty"`
	Editable bool                     `json:"editable,omitempty"`
}

// ReplyTarget 保存岗位对应的平台 ID 或经核实唯一的完整名称。
type ReplyTarget struct {
	PositionID, PositionName string
	NameUnique               bool
}

// ReplyConversation 保存稳定会话身份，不用姓名或数组序号代替 ID。
type ReplyConversation struct{ ID, PositionID, PositionName, Name string }

// ReplyMessage 保存页面读取的消息事实，Direction 为 inbound、outbound 或 system。
type ReplyMessage struct{ ID, Direction, Kind, Timestamp, Text string }

// ReplyContext 保存有序消息及指纹；草稿不进入消息指纹，由发送前单独核对。
type ReplyContext struct {
	Conversation       ReplyConversation
	Messages           []ReplyMessage
	Draft              string
	InboundFingerprint string
	Fingerprint        string
}

// ReplyStats 保存自动回复统计，不计入打招呼数量。
type ReplyStats struct {
	Checked int `json:"checked"`
	Replied int `json:"replied"`
	Skipped int `json:"skipped"`
	Failed  int `json:"failed"`
	Unknown int `json:"unknown"`
}

// AutoReplyRuntime 是可选平台能力；未实现的平台继续使用原打招呼流程。
type AutoReplyRuntime interface {
	AutoReplyAvailable() error
	PrepareReplyPage(context.Context, Executor) error
	ResolveReplyTarget(context.Context, Executor, string) (ReplyTarget, error)
	ScanUnreadReplies(context.Context, Executor, ReplyTarget, int) ([]ReplyConversation, error)
	ReadReplyContext(context.Context, Executor, ReplyTarget, ReplyConversation) (ReplyContext, error)
	RecheckReplyContext(context.Context, Executor, ReplyTarget, ReplyContext) (ReplyContext, error)
	StageReply(context.Context, Executor, ReplyTarget, ReplyContext, string) error
	SendReply(context.Context, Executor, ReplyTarget, ReplyContext, string) (bool, error)
	ConfirmReply(context.Context, Executor, ReplyTarget, ReplyConversation, string, string) (bool, error)
	// ResumeAfterReply 在自动回复成功后判断是否索要简历。
	// positionSnapshot 为岗位快照（含 common_config.request_resume 和 ai_config），
	// aiClient 为 AI 客户端（用于截图评分），screenshotsDir 为截图存放目录。
	// 返回动作类型：skipped/requested/ai_requested/ai_skipped/failed。
	ResumeAfterReply(ctx context.Context, exec Executor, conversation ReplyConversation, positionSnapshot map[string]any, aiClient any, screenshotsDir string) (string, error)
}

// ReplyHash 返回正文或已规范化数据的摘要，不保留原文。
func ReplyHash(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}

// ReplyPositionMatches 仅认可平台岗位 ID 或已经确认唯一的完整岗位名称。
// Boss 下拉选项的岗位 ID 可能包含城市和薪资后缀，会话侧只有岗位名，
// 因此 ID 比较同时支持包含匹配。
func ReplyPositionMatches(conversation ReplyConversation, target ReplyTarget) bool {
	if strings.TrimSpace(conversation.ID) == "" {
		return false
	}
	if conversation.PositionID != "" && target.PositionID != "" {
		if conversation.PositionID == target.PositionID {
			return true
		}
		// 下拉文本 "Java开发工程师 _ 南京 8-12K" 包含会话岗位名 "Java开发工程师"
		return strings.Contains(target.PositionID, conversation.PositionID) ||
			strings.Contains(conversation.PositionID, target.PositionID)
	}
	return target.NameUnique && target.PositionName != "" && conversation.PositionName == target.PositionName
}

// ReplyMessageFingerprint 优先使用稳定消息 ID；回退必须同时具备绝对时间、方向和文本。
func ReplyMessageFingerprint(conversationID string, message ReplyMessage) string {
	if conversationID == "" || (message.Direction != "inbound" && message.Direction != "outbound") {
		return ""
	}
	parts := []string{conversationID, message.Direction, message.ID}
	if message.ID == "" {
		if _, err := time.Parse(time.RFC3339Nano, message.Timestamp); err != nil || strings.TrimSpace(message.Text) == "" {
			return ""
		}
		parts = append(parts, message.Timestamp, message.Text)
	}
	raw, _ := json.Marshal(parts)
	return ReplyHash(string(raw))
}

// ValidateReplyContext 验证最后有效消息为候选人文本，并计算入站身份和完整上下文摘要。
func ValidateReplyContext(value ReplyContext, target ReplyTarget) (ReplyContext, error) {
	if !ReplyPositionMatches(value.Conversation, target) || len(value.Messages) == 0 {
		return ReplyContext{}, ErrReplyUnsafe
	}
	last := value.Messages[len(value.Messages)-1]
	if last.Direction != "inbound" || last.Kind != "text" || strings.TrimSpace(last.Text) == "" {
		return ReplyContext{}, ErrReplyUnsafe
	}
	fingerprint := ReplyMessageFingerprint(value.Conversation.ID, last)
	if fingerprint == "" {
		return ReplyContext{}, ErrReplyUnsafe
	}
	raw, _ := json.Marshal(struct {
		Conversation ReplyConversation
		Messages     []ReplyMessage
	}{value.Conversation, value.Messages})
	value.InboundFingerprint = fingerprint
	value.Fingerprint = ReplyHash(string(raw))
	return value, nil
}
