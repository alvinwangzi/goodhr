// Package platformcore 定义本地岗位运行平台运行时的统一接口。
package platformcore

import (
	"context"

	"goodhr5/local-agent-go/internal/cloudapi"
)

// Executor 定义平台实现调用浏览器 Worker 的统一执行器。
type Executor interface {
	// Post 调用浏览器 Worker 接口并返回响应。
	Post(ctx context.Context, path string, payload any) (map[string]any, error)
	// Log 写入岗位运行日志。
	Log(level string, message string)
	// Delay 按业务动作等待指定秒数。
	Delay(ctx context.Context, label string, seconds float64) error
}

// Candidate 表示平台抽取到的候选人。
type Candidate map[string]any

// CandidatePageState 保存平台页面核实的既有事实，不包含未知的实际沟通时间。
type CandidatePageState struct {
	ContactObserved bool
	ResumeStatus    string
}

// CandidateStateReader 是平台可选能力；主流程负责同步记录，平台只读取页面事实。
type CandidateStateReader interface {
	ReadCandidateState(context.Context, Executor, cloudapi.PlatformConfig, Candidate) (CandidatePageState, error)
}

// CandidateStateObservedError 表示点击前发现已有沟通；主流程应同步事实并跳过，而不是重试发送。
type CandidateStateObservedError struct{ State CandidatePageState }

// Error 返回不包含候选人隐私的页面状态变化说明。
func (e *CandidateStateObservedError) Error() string {
	return "页面已显示沟通或简历记录，跳过首次打招呼"
}

// DetailRequest 表示读取候选人详情的请求。
type DetailRequest struct {
	PositionID     string
	Mode           string
	ScreenshotsDir string
	Filename       string
}

// DetailResult 表示平台读取候选人详情后的统一结果。
type DetailResult struct {
	Text       string
	Screenshot map[string]any
	Source     string
}

// CandidateInfoRequest 表示打招呼成功后需要向候选人索要的信息和追加消息。
type CandidateInfoRequest struct {
	RequestPhone  bool
	RequestWechat bool
	RequestResume bool
	GreetMessage  string
}

// CandidateInfoActionLabel 将协议动作转换为面向用户的中文名称。
func CandidateInfoActionLabel(action string) string {
	switch action {
	case "phone":
		return "电话"
	case "wechat":
		return "微信"
	case "resume":
		return "简历"
	default:
		return action
	}
}

// CandidateInfoPreparation 保存一个已经核对的确认框及发送前上下文，不包含 DOM 操作代码。
type CandidateInfoPreparation struct {
	Action      string
	Before      ReplyContext
	AlreadyDone bool
}

// CandidateInfoRequestOperator 提供可选的分阶段索要能力，公共流程在点击确认前持久化发送意图。
type CandidateInfoRequestOperator interface {
	PrepareCandidateInfoRequest(context.Context, Executor, ReplyTarget, ReplyConversation, string) (CandidateInfoPreparation, error)
	SubmitCandidateInfoRequest(context.Context, Executor, ReplyTarget, ReplyConversation, CandidateInfoPreparation) (string, error)
	CancelCandidateInfoRequest(context.Context, Executor, ReplyTarget, ReplyConversation, CandidateInfoPreparation) error
	InspectCandidateInfoRequest(context.Context, Executor, ReplyTarget, ReplyConversation, string) (bool, error)
}

// ResumeRequestOutcome 表示单个候选人回复检查与索要执行的结果。
type ResumeRequestOutcome struct {
	// Status 为处理结果：requested 已完成索要；pending 候选人尚未回复；not_found 会话列表中未找到；failed 执行失败。
	Status string
	// Reason 为未执行或失败的原因，成功时为空。
	Reason string
}

// Runtime 定义主流程调用的平台能力。
type Runtime interface {
	// OpenEntryPage 打开平台入口页面。
	OpenEntryPage(ctx context.Context, exec Executor, cfg cloudapi.PlatformConfig, entryURL string) error
	// PrepareEntryPage 处理平台入口页弹框或初始化动作。
	PrepareEntryPage(ctx context.Context, exec Executor, cfg cloudapi.PlatformConfig) error
	// IsPositionEntryPage 判断当前页面是否仍是岗位运行入口页面。
	IsPositionEntryPage(ctx context.Context, exec Executor, cfg cloudapi.PlatformConfig) (bool, error)
	// CurrentPositionName 读取当前页面岗位名称。
	CurrentPositionName(ctx context.Context, exec Executor, cfg cloudapi.PlatformConfig) (string, error)
	// SelectPosition 切换当前页面岗位。
	SelectPosition(ctx context.Context, exec Executor, cfg cloudapi.PlatformConfig, positionName string) error
	// ListVisibleCandidates 提取当前可见候选人。
	ListVisibleCandidates(ctx context.Context, exec Executor, cfg cloudapi.PlatformConfig, maxItems int) ([]Candidate, error)
	// ScrollCandidateList 滚动候选人列表。
	ScrollCandidateList(ctx context.Context, exec Executor, cfg cloudapi.PlatformConfig, distance int) error
	// FetchCandidateDetail 读取候选人详情。
	FetchCandidateDetail(ctx context.Context, exec Executor, cfg cloudapi.PlatformConfig, candidate Candidate, request DetailRequest) (DetailResult, error)
	// CloseCandidateDetail 关闭候选人详情。
	CloseCandidateDetail(ctx context.Context, exec Executor, cfg cloudapi.PlatformConfig, candidate Candidate) error
	// GreetCandidate 执行候选人打招呼。
	GreetCandidate(ctx context.Context, exec Executor, cfg cloudapi.PlatformConfig, candidate Candidate) error
	// CandidateFilterText 返回候选人筛选文本。
	CandidateFilterText(candidate Candidate) string
	// CandidateFingerprint 返回候选人去重指纹。
	CandidateFingerprint(candidate Candidate) string
	// CleanCandidateDetailText 清理平台详情文本中的非简历内容。
	CleanCandidateDetailText(text string) string
}

// BasicFilterApplier 是平台可选实现的基础筛选能力。
type BasicFilterApplier interface {
	// ApplyBasicFilters 在岗位处理完成后应用平台基础筛选条件。
	ApplyBasicFilters(ctx context.Context, exec Executor, cfg cloudapi.PlatformConfig, positionSnapshot map[string]any) error
}

// CandidateInfoRequester 是平台可选实现的打招呼后索要信息能力。
type CandidateInfoRequester interface {
	// RequestCandidateInfo 在打招呼成功后索要候选人信息并发送追加消息。
	RequestCandidateInfo(ctx context.Context, exec Executor, cfg cloudapi.PlatformConfig, candidate Candidate, request CandidateInfoRequest) error
}

// CandidateFollowupPagePreparer 关闭原候选人弹窗并准备可按真实 ID 核对的消息页，不自行发送。
type CandidateFollowupPagePreparer interface {
	PrepareCandidateFollowup(context.Context, Executor) error
}

// CandidateFollowupMessageStager 复用标准消息输入与上下文核对，保留原追加问候语长度规则。
type CandidateFollowupMessageStager interface {
	StageCandidateFollowup(context.Context, Executor, ReplyTarget, ReplyConversation, ReplyContext, string) error
}

// ResumeRequestChecker 是平台可选实现的"检查候选人回复并索要简历"能力。
// 用于岗位收尾阶段：切到平台消息页，确认候选人已回复后再执行索要动作。
type ResumeRequestChecker interface {
	// CheckResumeRequests 检查名单候选人是否已回复，对已回复者执行索要动作。
	// candidateNames 为候选人姓名列表，返回以姓名为键的结果表；未出现在结果表中的姓名视为 pending。
	CheckResumeRequests(ctx context.Context, exec Executor, cfg cloudapi.PlatformConfig, candidateNames []string) (map[string]ResumeRequestOutcome, error)
}

// PositionSearchPreparer 是平台可选实现的岗位运行搜索准备能力。
// 主流程会在首次确认岗位前调用；只有需要先按岗位配置搜索候选人的平台才需要实现。
type PositionSearchPreparer interface {
	PreparePositionSearch(ctx context.Context, exec Executor, cfg cloudapi.PlatformConfig, positionSnapshot map[string]any) error
}

// DirectPositionSelector 定义无需读取当前岗位、每次直接切换岗位运行岗位的平台策略。
type DirectPositionSelector interface {
	// ShouldSelectPositionDirectly 返回平台是否应跳过当前岗位读取和切换后复核。
	ShouldSelectPositionDirectly() bool
}

// PositionSelectionSkipper 定义完全不需要读取或切换页面岗位的平台策略。
type PositionSelectionSkipper interface {
	// ShouldSkipPositionSelection 返回平台是否应跳过全部页面岗位处理。
	ShouldSkipPositionSelection() bool
}

// DetailAnalysisScroller 定义 AI 分析期间可滚动候选人详情的平台能力。
type DetailAnalysisScroller interface {
	// ScrollCandidateDetail 在当前已打开的候选人详情中滚动一次。
	ScrollCandidateDetail(ctx context.Context, exec Executor, cfg cloudapi.PlatformConfig, candidate Candidate, distance int) error
}
