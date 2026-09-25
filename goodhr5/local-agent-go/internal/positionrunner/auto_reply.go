// 本文件编排自动回复的 AI 决策、防重和发送状态，不包含平台页面差异。
package positionrunner

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"goodhr5/local-agent-go/internal/cloudapi"
	"goodhr5/local-agent-go/internal/localai"
	"goodhr5/local-agent-go/internal/localdb"
	"goodhr5/local-agent-go/internal/platformcore"
	"goodhr5/local-agent-go/internal/platforms"

	"github.com/google/uuid"
)

// autoReplyRoundInterval 是相邻自动回复轮次之间的等待时间。
const autoReplyRoundInterval = 3 * time.Second

// errReplyStorage 表示无法保存发送状态，必须终止任务。
var errReplyStorage = errors.New("自动回复记录保存失败，任务已停止")

// replyGenerator 复用当前 AI 客户端，返回是否回复及正文的结构化决策。
type replyGenerator interface {
	GenerateReply(context.Context, localai.ReplyRequest) (localai.ReplyDecision, error)
}

// replyFlow 保存本轮固定依赖，不在公共流程按平台名称分支。
type replyFlow struct {
	db                                 *localdb.DB
	runtime                            platformcore.AutoReplyRuntime
	exec                               platformcore.Executor
	generator                          replyGenerator
	aiClient                           *localai.Client
	target                             platformcore.ReplyTarget
	scope, platform, positionID, runID string
	request                            localai.ReplyRequest
	rejectTemplate                     string // 岗位自定义拒绝话术，留空用系统默认
	cloudClient                        *cloudapi.Client
	token                              string
	positionSnapshot                   map[string]any // 岗位快照，供回复后索要简历的 AI 评估使用
	screenshotsDir                     string         // 截图目录，供在线简历截图使用
	reviewScore                        int            // 回复前详细简历评分，供回复后索要简历使用
	reviewReason                       string         // 已判定索要的评分摘要，随档案入库。
	acceptedResume                     bool           // 已直接接受候选人主动发送的简历，跳过索要流程
	rejectedReply                      bool           // 本次使用拒绝话术，回复后不再索要简历
	allowResumeRequest                 bool           // 前置结论：评分过阈值且简历未索要/未收到，回复成功后直接点求简历。
}

// normalizeTaskType 保持省略时为打招呼，拒绝未知流程。
// 支持逗号分隔的多选值（如 "greeting,auto_reply"），返回第一个有效值作为主类型。
func normalizeTaskType(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		value = "greeting"
	}
	// 多选时取第一个有效类型作为主类型，其余类型由 lifecycle 串行调度。
	if idx := strings.Index(value, ","); idx > 0 {
		value = strings.TrimSpace(value[:idx])
	}
	if value != "greeting" && value != "auto_reply" {
		return "", fmt.Errorf("不支持的任务类型")
	}
	return value, nil
}

// parseTaskTypes 解析逗号分隔的任务类型列表，返回去重后的有效类型。
func parseTaskTypes(value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return []string{"greeting"}
	}
	parts := strings.Split(value, ",")
	seen := map[string]bool{}
	var result []string
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" || seen[part] {
			continue
		}
		if part != "greeting" && part != "auto_reply" {
			continue
		}
		seen[part] = true
		result = append(result, part)
	}
	if len(result) == 0 {
		return []string{"greeting"}
	}
	return result
}

// hasTaskType 判断任务类型列表是否包含指定类型。
func hasTaskType(types []string, target string) bool {
	for _, t := range types {
		if t == target {
			return true
		}
	}
	return false
}

// process 对一个已读取会话执行新消息判断、生成、复核、落库、发送和页面确认。
func (f *replyFlow) process(ctx context.Context, current platformcore.ReplyContext) (outcome string, processErr error) {
	f.rejectedReply = false
	f.acceptedResume = false
	f.allowResumeRequest = false
	f.reviewScore = -1
	f.reviewReason = ""
	defer func() {
		if f.allowResumeRequest && (processErr != nil || outcome == "unknown") {
			f.trackResume(current.Conversation, "pending", "回复未完成或未确认，尚未执行索要；详情见本地任务日志")
		}
	}()
	f.flowLog("info", fmt.Sprintf("自动回复处理开始：候选人=%s，会话ID=%s，消息数=%d", current.Conversation.Name, current.Conversation.ID, len(current.Messages)))
	if err := ctx.Err(); err != nil {
		return "skipped", err
	}
	// 接收附件不依赖文字消息；先校验岗位和会话，再单独处理附件，避免被文字回复去重挡住。
	if !platformcore.ReplyPositionMatches(current.Conversation, f.target) || strings.TrimSpace(current.Draft) != "" {
		return "skipped", platformcore.ErrReplyUnsafe
	}
	hasPendingOffer, pendingErr := f.runtime.HasPendingResumeOffer(ctx, f.exec)
	f.flowLog("info", fmt.Sprintf("待接受简历检测：候选人=%s，hasPending=%v，err=%v", current.Conversation.Name, hasPendingOffer, pendingErr))
	if pendingErr == nil && hasPendingOffer {
		f.trackResume(current.Conversation, "pending", "")
		if err := f.runtime.AcceptPendingResumeOffer(ctx, f.exec); err != nil {
			f.trackResume(current.Conversation, "pending", "接受简历失败，尚未确认收到")
			return "failed", fmt.Errorf("接受简历失败：%w", err)
		}
		f.acceptedResume = true
		if err := f.exec.Delay(ctx, "接受简历后", 1.5); err != nil {
			return "skipped", err
		}
		if pending, err := f.runtime.HasPendingResumeOffer(ctx, f.exec); err != nil || pending {
			f.trackResume(current.Conversation, "pending", "简历仍待接受或接受结果未确认")
			return "failed", fmt.Errorf("简历仍待接受，暂不下载")
		}
		f.trackResume(current.Conversation, "received", "")
		if err := f.downloadResumeIfNeeded(ctx, current.Conversation, true); err != nil {
			return "failed", err
		}
		return "accepted_resume", nil
	}
	if current.ResumeStatus == "requested" {
		f.trackResume(current.Conversation, "requested", "")
	}
	if current.ResumeStatus == "received" {
		f.trackResume(current.Conversation, "received", "")
		f.acceptedResume = true
		if err := f.downloadResumeIfNeeded(ctx, current.Conversation, false); err != nil {
			return "failed", err
		}
	}
	current, err := platformcore.ValidateReplyContext(current, f.target)
	if err != nil {
		return "skipped", fmt.Errorf("上下文验证失败: %w", err)
	}
	key := localdb.AutoReplyRecord{ProfileScope: f.scope, Platform: f.platform, ConversationID: current.Conversation.ID, InboundFingerprint: current.InboundFingerprint}
	existing, err := f.db.FindAutoReply(ctx, key)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "failed", errReplyStorage
	}
	if err == nil {
		switch existing.Status {
		case "sent", "unknown", "skip", "uncertain":
			return "skipped", fmt.Errorf("去重：已处理过（status=%s）", existing.Status)
		case "sending":
			if err := f.transition(existing.ID, "sending", "unknown", "interrupted"); err != nil {
				return "failed", err
			}
			return f.confirm(ctx, current.Conversation, existing, "unknown")
		case "prepared", "obsolete":
		default:
			return "skipped", fmt.Errorf("去重：状态=%s", existing.Status)
		}
	}
	if strings.TrimSpace(current.Draft) != "" {
		return "skipped", nil
	}
	for i := len(current.Messages) - 1; i >= 0; i-- {
		message := current.Messages[i]
		if message.Direction == "system" {
			continue
		}
		if message.Direction == "outbound" {
			return "skipped", fmt.Errorf("我方已回复，等待候选人新消息")
		}
		break
	}

	request := f.request
	request.CandidateName = current.Conversation.Name
	request.ResumeStatus = current.ResumeStatus
	f.acceptedResume = current.ResumeStatus == "received"
	// 查表分流：检查打招呼阶段的扫描记录。
	// 有记录且评分 >= 阈值 = 我们主动打过招呼的候选人，跳过看简历，由 AI 判断答疑和索要。
	// 无记录 = 候选人主动找我们，需要先面板初筛→在线简历评分，再决定回复还是拒绝。
	threshold := f.greetThreshold()
	f.flowLog("info", fmt.Sprintf("自动回复阈值：候选人=%s，阈值=%.0f", current.Conversation.Name, threshold))
	skipReview := false
	if f.cloudClient != nil && strings.TrimSpace(f.token) != "" {
		screening, screenErr := f.cloudClient.FindScreeningByName(ctx, f.token, f.positionID, f.platform, current.Conversation.Name)
		if screenErr != nil {
			f.flowLog("warning", fmt.Sprintf("扫描记录查询失败：候选人=%s，错误=%s，将走回复前简历评估", current.Conversation.Name, screenErr.Error()))
		} else if screening != nil {
			f.flowLog("info", fmt.Sprintf("扫描记录命中：候选人=%s，打招呼阶段评分=%d，阈值=%.0f", current.Conversation.Name, screening.Score, threshold))
			if float64(screening.Score) < threshold {
				// 打招呼阶段评分就不够，理论上不该打招呼，用拒绝话术
				request.RejectTemplate = f.rejectTemplate
				if request.RejectTemplate == "" {
					request.RejectTemplate = defaultRejectTemplate()
				}
				request.FAQ = nil
				f.flowLog("info", fmt.Sprintf("打招呼阶段评分不通过：候选人=%s，评分=%d，阈值=%.0f，使用拒绝话术", current.Conversation.Name, screening.Score, threshold))
			} else {
				// 我们主动打过招呼的，跳过看简历，继续判断回复需求
				skipReview = true
				f.reviewScore = screening.Score
				f.reviewReason = "沿用打招呼阶段已通过的评分"
				f.flowLog("info", fmt.Sprintf("我们主动打过招呼的候选人：候选人=%s，跳过看简历，直接回复", current.Conversation.Name))
			}
		} else {
			f.flowLog("info", fmt.Sprintf("扫描记录未命中：候选人=%s，将走回复前简历评估", current.Conversation.Name))
		}
	}
	// 本会话已发过拒绝话术时不再重复拒绝，也不再重开简历评估，直接交 AI 结合上下文判断。
	rejectedBefore := f.alreadyRejected(ctx, current.Messages, key, f.rejectTemplate, defaultRejectTemplate())
	if rejectedBefore {
		f.flowLog("info", fmt.Sprintf("会话已发过拒绝话术，不再重复拒绝和重评简历，改由AI判断：候选人=%s", current.Conversation.Name))
		request.RejectTemplate = ""
		skipReview = true
	}
	// 无扫描记录时，回复前先评估候选人。
	// 第1步：面板初筛（硬性条件快筛）→ 第2步：详细简历评分。
	if !skipReview {
		score, reason, reviewErr := f.reviewProfileBeforeReply(ctx, current.Conversation)
		f.reviewScore, f.reviewReason = score, reason
		if reviewErr != nil {
			f.flowLog("warning", fmt.Sprintf("回复前简历查看失败：候选人=%s，错误=%s，将按正常回复处理", current.Conversation.Name, reviewErr.Error()))
		} else if reason == "简历已收到" {
			// 简历已经在手，只禁止重复索要，候选人的新问题仍交给 AI 判断。
			f.acceptedResume = true
			request.ResumeStatus = "received"
			f.flowLog("info", fmt.Sprintf("简历已收到，继续判断是否需要答疑：候选人=%s", current.Conversation.Name))
		} else if score >= 0 && float64(score) < threshold {
			// 评分不通过，使用拒绝话术
			request.RejectTemplate = f.rejectTemplate
			if request.RejectTemplate == "" {
				request.RejectTemplate = defaultRejectTemplate()
			}
			request.FAQ = nil
			f.flowLog("info", fmt.Sprintf("简历评分不通过：候选人=%s，评分=%d，阈值=%.0f，原因=%s", current.Conversation.Name, score, threshold, reason))
		} else if float64(score) >= threshold {
			f.flowLog("info", fmt.Sprintf("简历评分通过：候选人=%s，评分=%d，阈值=%.0f，原因=%s", current.Conversation.Name, score, threshold, reason))
		}
	} else if !rejectedBefore {
		// 跳过看简历时沿用已通过的评分；是否索要仍由简历状态和 AI 决策共同决定。
		// 已拒绝过的会话不沿用高分，避免对已拒绝候选人再索要简历。
		if f.reviewScore < 0 {
			f.reviewScore = 100
		}
	}
	request.AllowResumeRequest = float64(f.reviewScore) >= threshold && request.ResumeStatus == "none" && !f.acceptedResume
	f.allowResumeRequest = request.AllowResumeRequest
	if f.allowResumeRequest && strings.TrimSpace(request.RejectTemplate) == "" {
		f.trackResume(current.Conversation, "pending", "")
	}
	decision := localai.ReplyDecision{Action: "reply"}
	text := strings.TrimSpace(request.RejectTemplate)
	if text != "" {
		f.rejectedReply = true
		f.flowLog("info", fmt.Sprintf("使用固定拒绝话术：候选人=%s，跳过AI回复生成", current.Conversation.Name))
	} else {
		start := max(0, len(current.Messages)-20)
		history, _ := json.Marshal(current.Messages[start:])
		request.History = string(history)
		f.flowLog("info", fmt.Sprintf("开始AI回复判断：候选人=%s，最近消息数=%d", current.Conversation.Name, len(current.Messages)-start))
		decision, err = f.generator.GenerateReply(ctx, request)
		if ctx.Err() != nil {
			return "skipped", ctx.Err()
		}
		if err != nil {
			return "failed", fmt.Errorf("AI 回复判断失败：%w", err)
		}
		if decision.Action != "reply" && decision.Action != "skip" && decision.Action != "uncertain" {
			return "failed", fmt.Errorf("AI 回复决策类型无法识别")
		}
		text = decision.Text
		f.flowLog("info", fmt.Sprintf("AI回复判断：候选人=%s，结果=%s，原因=%s", current.Conversation.Name, decision.Action, truncateForLog(decision.Reason, 200)))
	}
	if ctx.Err() != nil {
		return "skipped", ctx.Err()
	}
	text = strings.TrimSpace(text)
	if decision.Action == "reply" && (text == "" || utf8.RuneCountInString(text) > 1000) {
		return "failed", fmt.Errorf("回复内容不符合发送要求：长度=%d", utf8.RuneCountInString(text))
	}
	if decision.Action != "reply" && (text != "" || decision.RequestResume) {
		return "failed", fmt.Errorf("不发送决策包含发送动作")
	}
	f.flowLog("info", fmt.Sprintf("回复内容已准备：候选人=%s，内容长度=%d，允许索要=%t，内容前50字=%q", current.Conversation.Name, utf8.RuneCountInString(text), f.allowResumeRequest, truncateForLog(text, 50)))
	checked, err := f.runtime.RecheckReplyContext(ctx, f.exec, f.target, current)
	if err != nil {
		return replyOutcome(err), err
	}
	if strings.TrimSpace(checked.Draft) != "" {
		return "skipped", nil
	}
	if ctx.Err() != nil {
		return "skipped", ctx.Err()
	}
	key.PositionID = f.positionID
	key.RunID = f.runID
	key.ContextFingerprint = current.Fingerprint
	key.ReplyFingerprint = platformcore.ReplyHash(text)
	if decision.Action != "reply" {
		err = f.db.SaveAutoReplyDecision(ctx, key, decision.Action)
		if errors.Is(err, localdb.ErrAutoReplyConflict) {
			return "skipped", nil
		}
		if err != nil {
			return "failed", errReplyStorage
		}
		return "skipped", nil
	}
	record, err := f.db.PrepareAutoReply(ctx, key)
	if errors.Is(err, localdb.ErrAutoReplyConflict) {
		return "skipped", nil
	}
	if err != nil {
		return "failed", errReplyStorage
	}
	if err = f.runtime.StageReply(ctx, f.exec, f.target, current, text); err != nil {
		return f.obsolete(record, "prepared", err)
	}
	if err = ctx.Err(); err != nil {
		return f.obsolete(record, "prepared", err)
	}
	if err = f.db.TransitionAutoReply(ctx, record.ID, "prepared", "sending", ""); err != nil {
		return "failed", errReplyStorage
	}
	if err = ctx.Err(); err != nil {
		return f.obsolete(record, "sending", err)
	}
	f.flowLog("info", fmt.Sprintf("开始发送回复：候选人=%s", current.Conversation.Name))
	attempted, sendErr := f.runtime.SendReply(ctx, f.exec, f.target, current, text)
	if !attempted {
		if sendErr == nil {
			sendErr = platformcore.ErrReplyUnsafe
		}
		return f.obsolete(record, "sending", sendErr)
	}
	f.flowLog("info", fmt.Sprintf("回复已发送，等待确认：候选人=%s", current.Conversation.Name))
	// 发送动作已开始：停止信号不取消必要的结果确认，不再执行新的发送动作。
	return f.confirm(context.WithoutCancel(ctx), current.Conversation, record, "sending")
}

// downloadResumeIfNeeded 以本地下载记录去重；关联键不含岗位运行 ID，避免重跑或换岗位重复下载。
// freshOffer 表示刚接受了一次新的发送请求，允许保存新版附件；结果未知时仍禁止自动重复点击。
func (f *replyFlow) downloadResumeIfNeeded(ctx context.Context, conversation platformcore.ReplyConversation, freshOffer bool) (resultErr error) {
	defer func() {
		if resultErr != nil {
			f.trackResume(conversation, "received", "附件下载失败或结果未确认；详情见本地下载记录")
		}
	}()
	downloader, ok := f.runtime.(platformcore.ResumeAttachmentDownloader)
	if !ok {
		return nil
	}
	if f.scope == "" || f.platform == "" || conversation.ID == "" {
		return platformcore.ErrReplyUnsafe
	}
	identity, _ := json.Marshal([]string{"resume-attachment-v1", f.scope, f.platform, conversation.ID})
	source := platformcore.ReplyHash(string(identity))
	previous, err := f.db.LatestSourceDownload(source)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%w：读取附件下载记录失败", errReplyStorage)
	}
	if err == nil {
		if previous.Status == "pending" || previous.Status == "unknown" {
			f.flowLog("warning", fmt.Sprintf("附件下载跳过：候选人=%s，上次结果待确认，请先检查本地下载记录", conversation.Name))
			f.trackResume(conversation, "received", "上次附件下载结果待确认，请检查本地下载记录")
			return nil
		}
		if previous.Status == "saved" && !freshOffer {
			if file, statErr := os.Stat(previous.FilePath); statErr == nil && file.Mode().IsRegular() && file.Size() > 0 {
				f.flowLog("info", fmt.Sprintf("附件下载跳过：候选人=%s，已有成功下载记录", conversation.Name))
				f.trackResume(conversation, "downloaded", "")
				return nil
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	request := platformcore.DownloadRequest{ID: "resume_" + uuid.NewString(), SourceKey: source, PositionID: f.positionID, TimeoutMS: 20000}
	if _, err := f.db.SaveDownload(map[string]any{"id": request.ID, "source_key": source, "position_id": f.positionID, "status": "pending"}); err != nil {
		return fmt.Errorf("%w：无法登记附件下载", errReplyStorage)
	}
	downloadCtx, cancel := context.WithTimeout(ctx, 35*time.Second)
	defer cancel()
	f.flowLog("info", fmt.Sprintf("开始下载附件简历：候选人=%s", conversation.Name))
	record, downloadErr := downloader.DownloadResumeAttachment(downloadCtx, f.exec, conversation, request)
	status := stringFromMap(record, "status")
	if status == "saved" {
		file, statErr := os.Stat(stringFromMap(record, "file_path"))
		if stringFromMap(record, "id") != request.ID || statErr != nil || !file.Mode().IsRegular() || file.Size() <= 0 {
			return fmt.Errorf("附件下载未确认：未找到对应的完整本地文件")
		}
	}
	if status == "saved" || status == "failed" {
		record["id"], record["source_key"], record["position_id"] = request.ID, source, f.positionID
		if _, err := f.db.SaveDownload(record); err != nil {
			return fmt.Errorf("%w：保存附件下载结果失败", errReplyStorage)
		}
		if status == "failed" && downloadErr == nil {
			downloadErr = fmt.Errorf("附件下载失败，未保存文件")
		}
	} else if downloadErr == nil {
		downloadErr = fmt.Errorf("附件下载结果未确认，请检查本地下载记录")
	}
	if status == "saved" {
		f.trackResume(conversation, "downloaded", "")
	}
	// 不用 unknown 响应覆盖异步通知刚写入的 saved 记录。
	return downloadErr
}

// truncateForLog 截断字符串用于日志输出，避免刷屏。
func truncateForLog(s string, maxRunes int) string {
	if utf8.RuneCountInString(s) <= maxRunes {
		return s
	}
	runes := []rune(s)
	return string(runes[:maxRunes]) + "..."
}

// flowLog 安全地通过 exec 记录日志，exec 为 nil 时静默。
func (f *replyFlow) flowLog(level, message string) {
	if f.exec != nil {
		f.exec.Log(level, message)
	}
}

// replyOutcome 将安全跳过与会话错误区分，原始页面或模型错误不进入日志。
func replyOutcome(err error) string {
	if errors.Is(err, platformcore.ErrReplyUnsafe) || errors.Is(err, context.Canceled) {
		return "skipped"
	}
	return "failed"
}

// transition 使用独立短超时保存发送状态，停止后仍需保存确定结果。
func (f *replyFlow) transition(id, from, to, code string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := f.db.TransitionAutoReply(ctx, id, from, to, code); err != nil {
		return errReplyStorage
	}
	return nil
}

// obsolete 只在确认未触发发送动作时废弃旧答案，不自动删除输入草稿。
func (f *replyFlow) obsolete(record localdb.AutoReplyRecord, from string, cause error) (string, error) {
	if err := f.transition(record.ID, from, "obsolete", "not_sent"); err != nil {
		return "failed", err
	}
	return replyOutcome(cause), cause
}

// confirm 只依据平台页面证据确认发送；无法确认则保存 unknown 并禁止重发。
func (f *replyFlow) confirm(ctx context.Context, c platformcore.ReplyConversation, record localdb.AutoReplyRecord, from string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	attempts := 0
	reason := "等待超时，页面未出现与本次回复匹配的消息"
	for ctx.Err() == nil {
		attempts++
		confirmed, err := f.runtime.ConfirmReply(ctx, f.exec, f.target, c, record.InboundFingerprint, record.ReplyFingerprint)
		if err != nil {
			reason = "页面核对失败：" + err.Error()
			break
		}
		if confirmed {
			if err := f.transition(record.ID, from, "sent", ""); err != nil {
				return "failed", err
			}
			f.flowLog("info", fmt.Sprintf("回复发送已确认：候选人=%s，检查=%d次", c.Name, attempts))
			return "sent", nil
		}
		// 页面可能尚未显示新消息；只轮询读取，绝不重试发送动作。
		if err := sleepWithContext(ctx, 250*time.Millisecond); err != nil {
			break
		}
	}
	if from == "sending" {
		if err := f.transition(record.ID, "sending", "unknown", "unconfirmed"); err != nil {
			return "failed", err
		}
	}
	f.flowLog("warning", fmt.Sprintf("回复发送未确认：候选人=%s，检查=%d次，原因=%s；不重复发送，本轮不索要简历", c.Name, attempts, reason))
	return "unknown", nil
}

// resumeAfterReplyIfNeeded 在自动回复成功后判断是否需要索要简历。
// 只用前置结论 allowResumeRequest（评分过阈值且简历未索要/未收到），不再看正文措辞或 AI 标志。
// 已直接接受候选人主动发送的简历或使用拒绝话术时跳过。
func (f *replyFlow) resumeAfterReplyIfNeeded(ctx context.Context, conversation platformcore.ReplyConversation) (string, error) {
	if err := ctx.Err(); err != nil {
		f.flowLog("info", fmt.Sprintf("索要简历跳过：候选人=%s，任务已停止或等待超时", conversation.Name))
		return "skipped", err
	}
	if f.runtime == nil || f.positionSnapshot == nil || f.acceptedResume || f.rejectedReply {
		f.flowLog("info", fmt.Sprintf("索要简历跳过：候选人=%s，已接受简历=%t，本次拒绝=%t", conversation.Name, f.acceptedResume, f.rejectedReply))
		return "", nil
	}
	if !f.allowResumeRequest {
		f.flowLog("info", fmt.Sprintf("索要简历跳过：候选人=%s，前置结论不允许索要（评分未过阈值或简历已索要/已收到）", conversation.Name))
		return "", nil
	}
	resumeCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	action, err := f.runtime.ResumeAfterReply(resumeCtx, f.exec, conversation, f.positionSnapshot, f.reviewScore, f.greetThreshold())
	if err != nil {
		f.trackResume(conversation, "pending", "索要失败或确认超时；详情见本地任务日志")
	} else if action == "requested" {
		f.trackResume(conversation, "requested", "")
	}
	return action, err
}

// reviewProfileBeforeReply 在生成回复前查看候选人在线简历并评分。
// 委托给平台能力的 ReviewProfileBeforeReply 执行截图和 AI 评分。
func (f *replyFlow) reviewProfileBeforeReply(ctx context.Context, conversation platformcore.ReplyConversation) (int, string, error) {
	if f.runtime == nil || f.positionSnapshot == nil {
		return -1, "", nil
	}
	reviewCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	return f.runtime.ReviewProfileBeforeReply(reviewCtx, f.exec, conversation, f.positionSnapshot, f.aiClient, f.screenshotsDir)
}

// replyGeneratorFor 根据启动配置构建自动回复使用的 AI 客户端。
// options 为岗位运行启动参数，同时返回具体客户端供回复后索要简历的 AI 评估使用。
func replyGeneratorFor(options StartOptions) (replyGenerator, *localai.Client, error) {
	if err := validateAIConfig(options.AIConfig); err != nil {
		return nil, nil, err
	}
	client := localai.New(options.AIConfig)
	client.EnableThinking = false
	return client, client, nil
}

// positionReplyPrompt 读取岗位级回复提示词，存于岗位快照 ai_config.reply_prompt。
func positionReplyPrompt(position localdb.Position) string {
	return strings.TrimSpace(stringFromMap(mapValue(position.PositionSnapshot["ai_config"]), "reply_prompt"))
}

// positionRequirement 读取岗位要求文本，供回复提示词使用，缺失时留空。
func positionRequirement(position localdb.Position) string {
	if value := strings.TrimSpace(stringFromMap(mapValue(position.PositionSnapshot["common_config"]), "requirement")); value != "" {
		return value
	}
	return strings.TrimSpace(stringFromMap(position.PositionSnapshot, "requirement"))
}

// positionRejectTemplate 读取岗位自定义拒绝话术，留空时使用系统默认。
func positionRejectTemplate(position localdb.Position) string {
	return strings.TrimSpace(stringFromMap(mapValue(position.PositionSnapshot["ai_config"]), "reply_reject_template"))
}

// greetThreshold 读取岗位配置的打招呼阈值，默认 70。
func (f *replyFlow) greetThreshold() float64 {
	aiConfig := mapValue(f.positionSnapshot["ai_config"])
	return floatFromMapOr(aiConfig, "greet_score_threshold", 70)
}

// positionFAQ 读取岗位常见问答语料，最多返回 10 条。
func positionFAQ(position localdb.Position) []localai.FAQEntry {
	aiConfig := mapValue(position.PositionSnapshot["ai_config"])
	raw, ok := aiConfig["reply_faq"].([]any)
	if !ok {
		return nil
	}
	var result []localai.FAQEntry
	for _, item := range raw {
		entry, ok := item.(map[string]any)
		if !ok {
			continue
		}
		q := strings.TrimSpace(stringFromMap(entry, "q"))
		a := strings.TrimSpace(stringFromMap(entry, "a"))
		if q == "" || a == "" {
			continue
		}
		result = append(result, localai.FAQEntry{Q: q, A: a})
		if len(result) >= 10 {
			break
		}
	}
	return result
}

// defaultRejectTemplate 返回系统默认拒绝话术。
func defaultRejectTemplate() string {
	return "感谢你的关注，我们看了你的信息，跟我们的岗位要求不匹配。下次有机会再合作。"
}

// historyContainsReject 判断会话历史中是否已存在我方发送过的拒绝话术。
// 页面抽取的出站文本可能拼接“已读”等状态标签，方向字段也可能带不可见差异，
// 因此方向和正文都用包含匹配，精确相等会漏判已发过的拒绝。
func historyContainsReject(messages []platformcore.ReplyMessage, rejects ...string) bool {
	for _, message := range messages {
		if !strings.Contains(message.Direction, "outbound") {
			continue
		}
		for _, reject := range rejects {
			if reject = strings.TrimSpace(reject); reject != "" && strings.Contains(message.Text, reject) {
				return true
			}
		}
	}
	return false
}

// alreadyRejected 判断会话是否已发过拒绝话术：先查页面历史原文，再查本地发送记录指纹。
func (f *replyFlow) alreadyRejected(ctx context.Context, messages []platformcore.ReplyMessage, key localdb.AutoReplyRecord, templates ...string) bool {
	if historyContainsReject(messages, templates...) {
		return true
	}
	for _, template := range templates {
		template = strings.TrimSpace(template)
		if template == "" {
			continue
		}
		sent, err := f.db.HasSentReplyFingerprint(ctx, key.ProfileScope, key.Platform, key.ConversationID, platformcore.ReplyHash(template))
		if err != nil {
			f.flowLog("warning", fmt.Sprintf("查询拒绝发送记录失败：%s", err.Error()))
			continue
		}
		if sent {
			return true
		}
	}
	return false
}

// runAutoReplyTask 组装自动回复依赖并执行整轮编排。
// ctx 为运行上下文，position 为岗位运行记录，options 为启动参数。
func (r *Runner) runAutoReplyTask(ctx context.Context, position localdb.Position, options StartOptions) {
	platformRuntime, err := platforms.RuntimeFor(position.PlatformID)
	if err != nil {
		r.failStart(position.ID, err.Error(), options)
		return
	}
	runtime, ok := platformRuntime.(platformcore.AutoReplyRuntime)
	if !ok {
		r.failStart(position.ID, "当前平台暂不支持 AI 自动回复", options)
		return
	}
	generator, aiClient, err := replyGeneratorFor(options)
	if err != nil {
		r.failStart(position.ID, err.Error(), options)
		return
	}
	r.runAutoReply(ctx, position, options, runtime, generator, aiClient)
}

// runAutoReply 执行自动回复任务；浏览器动作只走不重试调用，错误分类决定整任务去留。
// ctx 为运行上下文，position 为岗位运行记录，options 为启动参数，runtime 为平台自动回复能力，generator 为回复生成器，aiClient 为 AI 客户端。
func (r *Runner) runAutoReply(ctx context.Context, position localdb.Position, options StartOptions, runtime platformcore.AutoReplyRuntime, generator replyGenerator, aiClient *localai.Client) platformcore.ReplyStats {
	positionID := position.ID
	totalRounds := scanRounds(options)
	stats := platformcore.ReplyStats{}
	r.updateReplyStats(positionID, stats)
	stopped := func(message string) {
		r.updateProgress(positionID, Progress{Stage: "stopped", Message: message, TotalRounds: totalRounds})
		_, _ = r.db.UpdatePositionStatus(positionID, "stopped")
		r.positionLog(positionID, "info", "自动回复停止："+message)
		r.notifyCloudAutoReplyStatus(positionID, options, "stopped", stats)
	}
	// fatal 判断错误是否需要终止整任务，不记录页面或聊天原文。
	fatal := func(err error) bool {
		if err == nil {
			return false
		}
		if isBrowserClosedPositionError(err) {
			stopped("浏览器已关闭，自动回复已结束")
			return true
		}
		if errors.Is(err, context.Canceled) {
			stopped("自动回复已按停止请求结束")
			return true
		}
		return false
	}
	r.positionLog(positionID, "info", "自动回复启动：正在启动浏览器")
	if _, err := r.worker.Start(ctx); err != nil {
		r.failStart(positionID, "浏览器启动失败："+err.Error(), options)
		return stats
	}
	exec := platformExecutor{runner: r, positionID: positionID, once: true}
	profileName := positionProfileName(position)
	if _, err := r.worker.CallOnce(ctx, "/api/v1/browser/start", map[string]any{
		"humanize":       true,
		"user_data_dir":  filepath.Join(r.profilesDir, profileName),
		"downloads_path": r.browserDownloadDir(),
		"no_script":      true,
	}); err != nil {
		r.failStart(positionID, "浏览器启动或显示校准失败："+err.Error(), options)
		return stats
	}
	r.positionLog(positionID, "info", "自动回复启动：正在打开消息页并核对岗位")
	if err := runtime.PrepareReplyPage(ctx, exec); err != nil {
		r.failStart(positionID, "消息页准备失败："+err.Error(), options)
		return stats
	}
	name := positionPositionName(position)
	r.positionLog(positionID, "info", "自动回复核对岗位：岗位名="+name)
	target, err := runtime.ResolveReplyTarget(ctx, exec, name)
	if err != nil {
		r.positionLog(positionID, "error", "自动回复岗位核对失败：岗位名="+name+"，错误="+err.Error())
		r.failStart(positionID, "页面岗位核对失败："+err.Error(), options)
		return stats
	}
	r.positionLog(positionID, "info", "自动回复岗位核对成功：positionID="+target.PositionID)
	cloudBase := strings.TrimSpace(options.CloudAPIBase)
	if cloudBase == "" {
		cloudBase = strings.TrimSpace(r.cloudAPIBase)
	}
	if cloudBase == "" {
		cloudBase = "https://www.xx.com"
	}
	flow := &replyFlow{
		db: r.db, runtime: runtime, exec: exec, generator: generator, aiClient: aiClient, target: target,
		scope:            platformcore.ReplyHash("profile:" + safePathName(profileName)),
		platform:         strings.ToLower(strings.TrimSpace(position.PlatformID)),
		positionID:       positionID,
		runID:            options.CloudRunID,
		rejectTemplate:   positionRejectTemplate(position),
		cloudClient:      cloudapi.New(cloudBase),
		token:            options.Token,
		positionSnapshot: position.PositionSnapshot,
		screenshotsDir:   r.screenshotsDir,
		request: localai.ReplyRequest{
			PositionName:        name,
			PositionRequirement: positionRequirement(position),
			ReplyPrompt:         positionReplyPrompt(position),
			ReplySystemPrompt:   options.AIConfig.ReplySystemPrompt,
			FAQ:                 positionFAQ(position),
		},
	}
	flow.flushResumeTracking()
	defer flow.flushResumeTracking()
	failures := 0
	for round := 1; round <= totalRounds; round++ {
		if err := ctx.Err(); err != nil {
			stopped("自动回复已按停止请求结束")
			return stats
		}
		r.updateProgress(positionID, Progress{Stage: "scanning", Message: fmt.Sprintf("自动回复：正在检查未读消息，第 %d/%d 轮", round, totalRounds), Round: round, TotalRounds: totalRounds})
		conversations, err := runtime.ScanUnreadReplies(ctx, exec, target, 100)
		if err != nil {
			r.failStart(positionID, "未读会话扫描失败："+err.Error(), options)
			return stats
		}
		convNames := make([]string, 0, len(conversations))
		for _, c := range conversations {
			convNames = append(convNames, c.Name)
		}
		r.positionLog(positionID, "info", fmt.Sprintf("自动回复扫描到 %d 个未读会话：%v", len(conversations), convNames))
		for _, conversation := range conversations {
			if err := ctx.Err(); err != nil {
				stopped("自动回复已按停止请求结束")
				return stats
			}
			current, err := runtime.ReadReplyContext(ctx, exec, target, conversation)
			if err != nil {
				stats.Checked++
				stats.Failed++
				failures++
				r.updateReplyStats(positionID, stats)
				r.positionLog(positionID, "warning", fmt.Sprintf("自动回复：会话读取失败，已跳过（候选人=%s，错误=%s）", conversation.Name, err.Error()))
				if fatal(err) {
					return stats
				}
			} else {
				outcome, err := flow.process(ctx, current)
				stats.Checked++
				switch outcome {
				case "sent":
					stats.Replied++
					failures = 0
					// 回复成功后判断是否需要索要简历
					if resumeAction, resumeErr := flow.resumeAfterReplyIfNeeded(ctx, current.Conversation); resumeErr != nil {
						r.positionLog(positionID, "warning", fmt.Sprintf("自动回复索要简历：候选人=%s，动作=%s，错误=%s", current.Conversation.Name, resumeAction, resumeErr.Error()))
					} else if resumeAction != "" {
						r.positionLog(positionID, "info", fmt.Sprintf("自动回复索要简历：动作=%s，候选人=%s", resumeAction, current.Conversation.Name))
					}
				case "accepted_resume":
					stats.Replied++
					failures = 0
					r.positionLog(positionID, "info", fmt.Sprintf("已直接接受候选人附件简历：候选人=%s", current.Conversation.Name))
				case "skipped":
					stats.Skipped++
					// 调试：输出跳过原因（消息方向、数量、去重状态）
					msgSummary := make([]string, 0, len(current.Messages))
					for _, m := range current.Messages {
						msgSummary = append(msgSummary, fmt.Sprintf("%s/%s", m.Direction, m.Kind))
					}
					if err != nil {
						r.positionLog(positionID, "info", fmt.Sprintf("自动回复跳过（候选人=%s，错误=%s，消息=%v）", current.Conversation.Name, err.Error(), msgSummary))
					} else {
						r.positionLog(positionID, "info", fmt.Sprintf("自动回复跳过（候选人=%s，消息=%v，草稿=%q）", current.Conversation.Name, msgSummary, current.Draft))
					}
				case "unknown":
					stats.Unknown++
				default:
					stats.Failed++
					failures++
					if err != nil {
						r.positionLog(positionID, "error", fmt.Sprintf("自动回复处理失败：候选人=%s，错误=%s，连续失败=%d", current.Conversation.Name, err.Error(), failures))
					} else {
						r.positionLog(positionID, "error", fmt.Sprintf("自动回复处理失败：候选人=%s，outcome=%s，连续失败=%d", current.Conversation.Name, outcome, failures))
					}
				}
				r.updateReplyStats(positionID, stats)
				// 自动回复流程：上报所有遇到的候选人扫描记录。
				r.reportAutoReplyScreening(ctx, position, options, conversation, outcome)
				if errors.Is(err, errReplyStorage) {
					r.failStart(positionID, err.Error(), options)
					return stats
				}
				if fatal(err) {
					return stats
				}
			}
			if failures >= 3 {
				r.failStart(positionID, "自动回复连续处理失败，任务已停止", options)
				return stats
			}
		}
		if len(conversations) == 0 {
			break
		}
		if round < totalRounds {
			if err := sleepWithContext(ctx, autoReplyRoundInterval); err != nil {
				stopped("自动回复已按停止请求结束")
				return stats
			}
		}
	}
	if r.isUserStopped(positionID) {
		stopped("自动回复已按停止请求结束")
		return stats
	}
	r.finishAutoReply(positionID, options, stats, totalRounds)
	return stats
}

// finishAutoReply 保存自动回复完成状态并同步云端，不触发收尾求简历。
func (r *Runner) finishAutoReply(positionID string, options StartOptions, stats platformcore.ReplyStats, totalRounds int) {
	message := fmt.Sprintf("自动回复完成：检查=%d，回复=%d，跳过=%d，失败=%d，未知=%d", stats.Checked, stats.Replied, stats.Skipped, stats.Failed, stats.Unknown)
	r.updateProgress(positionID, Progress{Stage: "completed", Message: message, Round: totalRounds, TotalRounds: totalRounds})
	_, _ = r.db.UpdatePositionStatus(positionID, "completed")
	r.positionLog(positionID, "info", message)
	r.notifyCloudAutoReplyStatus(positionID, options, "completed", stats)
}

// notifyCloudAutoReplyStatus 以自动回复任务类型同步云端终态，回复不计入打招呼数量。
func (r *Runner) notifyCloudAutoReplyStatus(positionID string, options StartOptions, status string, stats platformcore.ReplyStats) {
	if strings.TrimSpace(options.Token) == "" {
		return
	}
	baseURL := strings.TrimSpace(options.CloudAPIBase)
	if baseURL == "" {
		baseURL = strings.TrimSpace(r.cloudAPIBase)
	}
	if baseURL == "" {
		baseURL = "https://www.xx.com"
	}
	ctx, cancel := context.WithTimeout(context.Background(), cloudStatsSyncTimeout)
	defer cancel()
	request := cloudapi.TaskStatusRequest{Status: status, TaskType: "auto_reply", RunID: options.CloudRunID, MachineID: options.MachineID, Skipped: stats.Skipped}
	if _, err := cloudapi.New(baseURL).SyncTaskStatus(ctx, options.Token, positionID, request); err != nil {
		r.positionLog(positionID, "warning", "自动回复状态同步失败："+err.Error())
	}
}

// reportAutoReplyScreening 异步上报自动回复遇到的候选人扫描记录。
// position 为岗位运行记录，options 为启动参数，conversation 为当前会话，outcome 为处理结果。
func (r *Runner) reportAutoReplyScreening(ctx context.Context, position localdb.Position, options StartOptions, conversation platformcore.ReplyConversation, outcome string) {
	if strings.TrimSpace(options.Token) == "" {
		return
	}
	platform := strings.ToLower(strings.TrimSpace(position.PlatformID))
	// 使用会话 ID 作为平台候选人标识，自动回复场景下无法获取打招呼阶段的名片指纹。
	candidateID := conversation.ID
	if candidateID == "" {
		return
	}
	name := conversation.Name
	baseURL := strings.TrimSpace(options.CloudAPIBase)
	if baseURL == "" {
		baseURL = strings.TrimSpace(r.cloudAPIBase)
	}
	go func() {
		syncCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		record := cloudapi.ScreeningRecord{
			Platform:            platform,
			PlatformCandidateID: candidateID,
			CandidateName:       name,
			Status:              outcome,
			Source:              "auto_reply",
		}
		if err := cloudapi.New(baseURL).ReportScreenings(syncCtx, options.Token, position.ID, []cloudapi.ScreeningRecord{record}); err != nil {
			r.positionLog(position.ID, "warning", "自动回复扫描记录上报失败："+err.Error())
		}
	}()
}
