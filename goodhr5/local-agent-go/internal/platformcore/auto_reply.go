// 本文件定义可选自动回复能力、强类型浏览器协议和消息安全校验，不包含平台分支。
package platformcore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"
)

// ErrReplyUnsafe 表示身份、岗位、消息或人工草稿无法安全确认。
var ErrReplyUnsafe = errors.New("会话、岗位或消息已变化，已跳过回复")

// SelectorSpec 定义标准 Locator 的选择器、范围、精确文本和属性约束。
type SelectorSpec struct {
	Selectors   []string          `json:"selectors"`
	Parent      *SelectorSpec     `json:"parent,omitempty"`
	Frame       string            `json:"frame,omitempty"`
	Nth         *int              `json:"nth,omitempty"`
	Text        string            `json:"text,omitempty"`
	VisibleText string            `json:"visible_text,omitempty"` // 按标准 innerText 精确核对可见文字，用于含隐藏提示的菜单。
	Attributes  map[string]string `json:"attributes,omitempty"`
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
	Download *DownloadRequest         `json:"download,omitempty"`
}

// DownloadRequest 关联一次浏览器点击及文件保存记录；Worker 只透传来源摘要，不解析业务含义。
type DownloadRequest struct {
	ID         string `json:"id"`
	SourceKey  string `json:"source_key"`
	PositionID string `json:"position_id"`
	TimeoutMS  int    `json:"timeout_ms"`
}

// ResumeAttachmentDownloader 是可选附件下载能力，不影响未实现的平台。
// 成功返回 saved 文件记录；failed 表示确定未完成，unknown 表示已点击但无法确认结果。
type ResumeAttachmentDownloader interface {
	DownloadResumeAttachment(context.Context, Executor, ReplyConversation, DownloadRequest) (map[string]any, error)
}

// ReplyTarget 保存岗位对应的平台 ID 或经核实唯一的完整名称。
type ReplyTarget struct {
	PositionID, PositionName string
	NameUnique               bool
}

// ReplyConversation 保存稳定会话身份，不用姓名或数组序号代替 ID。
type ReplyConversation struct {
	ID, PositionID, PositionName, Name string
	ObservedResumeStatus               string // 搜索结果读取的确定简历事实，不能从姓名推断。
}

// ReplyMessage 保存页面读取的消息事实，Direction 为 inbound、outbound 或 system。
type ReplyMessage struct{ ID, Direction, Kind, Timestamp, Text string }

// ReplyContext 保存有序消息及指纹；草稿不进入消息指纹，由发送前单独核对。
type ReplyContext struct {
	Conversation       ReplyConversation
	Messages           []ReplyMessage
	Draft              string
	InboundFingerprint string
	Fingerprint        string
	ResumeStatus       string // none、requested、received；空值表示页面状态未知。
}

// ReplyStats 保存自动回复统计，不计入打招呼数量。
type ReplyStats struct {
	AcceptedResume int `json:"accepted_resume"` // 接受附件人数，不算成功文字回复。
	Checked        int `json:"checked"`
	Replied        int `json:"replied"`
	Skipped        int `json:"skipped"`
	Failed         int `json:"failed"`
	Unknown        int `json:"unknown"`
}

// ReplyTargetVerifier 只读核对消息页与已确认的岗位筛选，不能展开下拉或修改页面。
type ReplyTargetVerifier interface {
	CheckReplyTarget(context.Context, Executor, ReplyTarget) (bool, error)
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
	// reviewScore 为 ReviewProfileBeforeReply 返回的详细简历评分，threshold 为岗位阈值。
	// 评分 >= 阈值时点击"求简历"按钮，否则跳过。
	// 返回动作类型：skipped/requested/failed。
	ResumeAfterReply(ctx context.Context, exec Executor, conversation ReplyConversation, positionSnapshot map[string]any, reviewScore int, threshold float64) (string, error)
	// ReviewProfileBeforeReply 在生成回复前查看候选人在线简历并评分。
	// 返回 (score, reason, error)。score < 0 表示无法评分（如按钮不存在）。
	ReviewProfileBeforeReply(ctx context.Context, exec Executor, conversation ReplyConversation, positionSnapshot map[string]any, aiClient any, screenshotsDir string) (int, string, error)
	// HasPendingResumeOffer 检测聊天面板中候选人是否已主动发起简历发送请求。
	HasPendingResumeOffer(ctx context.Context, exec Executor) (bool, error)
	// AcceptPendingResumeOffer 点击"同意"按钮接受候选人主动发送的附件简历。
	AcceptPendingResumeOffer(ctx context.Context, exec Executor) error
}

// ReGreetRuntime 是复打招呼的可选平台能力；未实现的平台不支持复打任务。
// 复打是主动发起新消息，与自动回复的"回复入站消息"不同：
// 会话内可能没有任何入站消息，不能复用 ReplyInboundFingerprint 校验链路。
type ReGreetRuntime interface {
	// LocateReplyConversation 通过平台搜索能力按姓名定位候选人并打开聊天面板。
	// 实现内部须核对面板姓名与搜索姓名一致，未找到时返回错误。
	LocateReplyConversation(ctx context.Context, exec Executor, name string) (ReplyConversation, error)
	// ReadOpenedReplyContext 读取当前已打开面板的上下文（不点击会话项）。
	// 复打场景面板已由搜索跳转打开，无会话项 data-id 可点。
	ReadOpenedReplyContext(ctx context.Context, exec Executor, target ReplyTarget, conversation ReplyConversation) (ReplyContext, error)
	// StageReGreet 输入前核对身份与空草稿，再把复打文本输入聊天框。
	StageReGreet(ctx context.Context, exec Executor, target ReplyTarget, conversation ReplyConversation, before ReplyContext, text string) error
	// SendReGreet 核对草稿与复打文本一致后点击发送。
	SendReGreet(ctx context.Context, exec Executor, target ReplyTarget, conversation ReplyConversation, before ReplyContext, text string) error
	// ConfirmReGreet 发送后核对面板新增了本次出站文本。
	// before 为发送前上下文，用于区分"本次新增"与"历史同文"。
	ConfirmReGreet(ctx context.Context, exec Executor, target ReplyTarget, conversation ReplyConversation, before ReplyContext, text string) (bool, error)
}

// IdentityConversationLocator 提供按已验证会话 ID 定位的可选平台能力，不能凭姓名代替。
type IdentityConversationLocator interface {
	LocateReplyConversationByID(context.Context, Executor, string, string) (ReplyConversation, error)
}

// CandidateIdentityResolver 通过当前平台页面的直接证据核对推荐标识对应的完整会话标识。
type CandidateIdentityResolver interface {
	ResolveCandidateConversationID(context.Context, Executor, string, string) (string, string, error)
}

// AccountIdentityRuntime 提供当前招聘平台的只读登录账号证明，首次准备时可以安全补取。
type AccountIdentityRuntime interface {
	ObserveAccountIdentity(context.Context, Executor, bool) (string, error)
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

// ReplyMessageFingerprint 自适应指纹：优先消息 ID；ID 等于时间戳说明选择器配置重复（Boss 场景），退化为正文；
// ID 为空时回退到 RFC3339 时间戳 + 正文；都没有则只用正文。
// 此函数仅计算单条消息摘要；回复触发使用 ReplyInboundFingerprint 校验方向和消息批次。
func ReplyMessageFingerprint(conversationID string, message ReplyMessage) string {
	if conversationID == "" {
		return ""
	}
	text := strings.TrimSpace(message.Text)
	if text == "" {
		return ""
	}
	// 有真实消息 ID（且与时间戳不同，说明是独立标识）
	if message.ID != "" && message.ID != message.Timestamp {
		parts := []string{conversationID, message.ID}
		raw, _ := json.Marshal(parts)
		return ReplyHash(string(raw))
	}
	// 有合法绝对时间
	if message.Timestamp != "" {
		if _, err := time.Parse(time.RFC3339Nano, message.Timestamp); err == nil {
			parts := []string{conversationID, message.Timestamp, text}
			raw, _ := json.Marshal(parts)
			return ReplyHash(string(raw))
		}
	}
	// 退化：只用正文（Boss 等无稳定 ID 的平台）
	parts := []string{conversationID, text}
	raw, _ := json.Marshal(parts)
	return ReplyHash(string(raw))
}

// ReplyInboundFingerprint 选取最新候选人文字消息，忽略我方与系统消息。
// 无稳定消息 ID 时以相同入站正文在已读取历史中的出现次数区分再次提问；
// 首次出现保留旧摘要，兼容既有已发送和结果未知记录，不使用相对时间或 DOM 下标。
func ReplyInboundFingerprint(conversationID string, messages []ReplyMessage) string {
	for i := len(messages) - 1; i >= 0; i-- {
		m := messages[i]
		if m.Direction != "inbound" {
			continue
		}
		if m.Kind != "text" {
			return ""
		}
		base := ReplyMessageFingerprint(conversationID, m)
		if base == "" {
			return ""
		}
		if m.ID != "" && m.ID != m.Timestamp {
			return base
		}
		if _, err := time.Parse(time.RFC3339Nano, m.Timestamp); err == nil {
			return base
		}
		occurrence := 0
		for _, previous := range messages[:i+1] {
			if previous.Direction == "inbound" && previous.Kind == "text" && strings.TrimSpace(previous.Text) == strings.TrimSpace(m.Text) {
				occurrence++
			}
		}
		if occurrence <= 1 {
			return base
		}
		raw, _ := json.Marshal([]string{"inbound-occurrence-v1", base, strconv.Itoa(occurrence)})
		return ReplyHash(string(raw))
	}
	return ""
}

// ValidateReplyContext 验证会话中存在可回复的候选人入站文字消息，并计算入站身份和完整上下文摘要。
// 不要求最后一条消息必须是 inbound——会话末尾可能有 system 或 outbound（如已发过打招呼），
// 只要从末尾向前能找到一条入站文字消息即可。
func ValidateReplyContext(value ReplyContext, target ReplyTarget) (ReplyContext, error) {
	if !ReplyPositionMatches(value.Conversation, target) || len(value.Messages) == 0 {
		return ReplyContext{}, ErrReplyUnsafe
	}
	for _, message := range value.Messages {
		if message.Direction != "inbound" && message.Direction != "outbound" && message.Direction != "system" {
			return ReplyContext{}, ErrReplyUnsafe
		}
	}
	fingerprint := ReplyInboundFingerprint(value.Conversation.ID, value.Messages)
	if fingerprint == "" {
		return ReplyContext{}, ErrReplyUnsafe
	}
	raw2, _ := json.Marshal(struct {
		Conversation ReplyConversation
		Messages     []ReplyMessage
		ResumeStatus string
	}{value.Conversation, value.Messages, value.ResumeStatus})
	value.InboundFingerprint = fingerprint
	value.Fingerprint = ReplyHash(string(raw2))
	return value, nil
}
