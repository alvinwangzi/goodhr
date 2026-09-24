// 本文件验证自动回复编排、停止和跨任务浏览器独占，不访问招聘网站。
package positionrunner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"goodhr5/local-agent-go/internal/browser"
	"goodhr5/local-agent-go/internal/cloudapi"
	"goodhr5/local-agent-go/internal/localai"
	"goodhr5/local-agent-go/internal/localdb"
	"goodhr5/local-agent-go/internal/platformcore"
)

// replyFixture 将真实页面边界替换为可控事实，主流程和 SQLite 仍使用真实实现。
type replyFixture struct {
	platformcore.AutoReplyRuntime
	current                  platformcore.ReplyContext
	scan                     []platformcore.ReplyConversation
	reads                    []platformcore.ReplyContext
	readCalls, prepared      int
	send                     func() (bool, error)
	stage                    func() error
	confirm                  bool
	stale                    bool
	sends, generations       int
	stagedText, sentText     string
	reviewScore, resumeCalls int
	reviewReason             string
	action                   string
	generate                 func(context.Context) (string, error)
}

// AutoReplyAvailable 表示测试平台配置已通过验证。
func (f *replyFixture) AutoReplyAvailable() error { return nil }

// PrepareReplyPage 记录消息页准备次数。
func (f *replyFixture) PrepareReplyPage(context.Context, platformcore.Executor) error {
	f.prepared++
	return nil
}

// ResolveReplyTarget 返回与岗位匹配的固定目标。
func (f *replyFixture) ResolveReplyTarget(context.Context, platformcore.Executor, string) (platformcore.ReplyTarget, error) {
	return platformcore.ReplyTarget{PositionID: "job1", PositionName: "Go", NameUnique: true}, nil
}

// ScanUnreadReplies 返回预设的未读会话列表。
func (f *replyFixture) ScanUnreadReplies(context.Context, platformcore.Executor, platformcore.ReplyTarget, int) ([]platformcore.ReplyConversation, error) {
	return f.scan, nil
}

// ReadReplyContext 按顺序返回预设会话内容，并记录读取次数。
func (f *replyFixture) ReadReplyContext(context.Context, platformcore.Executor, platformcore.ReplyTarget, platformcore.ReplyConversation) (platformcore.ReplyContext, error) {
	f.readCalls++
	if len(f.reads) == 0 {
		return platformcore.ReplyContext{}, platformcore.ErrReplyUnsafe
	}
	value := f.reads[0]
	f.reads = f.reads[1:]
	return value, nil
}

// GenerateReply 模拟远程 AI 决策，可在请求中触发停止或返回错误。
func (f *replyFixture) GenerateReply(ctx context.Context, request localai.ReplyRequest) (localai.ReplyDecision, error) {
	f.generations++
	text := "你好"
	var err error
	if f.generate != nil {
		text, err = f.generate(ctx)
	}
	// 不发送决策的正文和索要位必须为空，与生产边界校验保持一致。
	if f.action == "skip" || f.action == "uncertain" {
		return localai.ReplyDecision{Action: f.action, Reason: "测试不发送"}, err
	}
	return localai.ReplyDecision{Action: "reply", Text: text, Reason: "测试回答", RequestResume: request.AllowResumeRequest}, err
}

// RecheckReplyContext 模拟生成期间新消息或人工回复使旧答案失效。
func (f *replyFixture) RecheckReplyContext(_ context.Context, _ platformcore.Executor, _ platformcore.ReplyTarget, c platformcore.ReplyContext) (platformcore.ReplyContext, error) {
	if f.stale {
		return c, platformcore.ErrReplyUnsafe
	}
	return c, nil
}

// StageReply 模拟输入完成，允许测试在点击发送前取消。
func (f *replyFixture) StageReply(_ context.Context, _ platformcore.Executor, _ platformcore.ReplyTarget, _ platformcore.ReplyContext, text string) error {
	f.stagedText = text
	if f.stage != nil {
		return f.stage()
	}
	return nil
}

// SendReply 模拟真正的外部发送边界，记录发送次数。
func (f *replyFixture) SendReply(_ context.Context, _ platformcore.Executor, _ platformcore.ReplyTarget, _ platformcore.ReplyContext, text string) (bool, error) {
	f.sentText = text
	f.sends++
	if f.send != nil {
		return f.send()
	}
	return true, nil
}

// ConfirmReply 模拟页面是否已有对应的我方消息。
func (f *replyFixture) ConfirmReply(context.Context, platformcore.Executor, platformcore.ReplyTarget, platformcore.ReplyConversation, string, string) (bool, error) {
	return f.confirm, nil
}

// HasPendingResumeOffer 模拟检测候选人是否已主动发简历，默认返回 false。
func (f *replyFixture) HasPendingResumeOffer(context.Context, platformcore.Executor) (bool, error) {
	return false, nil
}

// AcceptPendingResumeOffer 模拟点击同意接受简历。
func (f *replyFixture) AcceptPendingResumeOffer(context.Context, platformcore.Executor) error {
	return nil
}

// ReviewProfileBeforeReply 返回平台评分边界的预设结果，不调用真实 AI 或打开简历。
func (f *replyFixture) ReviewProfileBeforeReply(context.Context, platformcore.Executor, platformcore.ReplyConversation, map[string]any, any, string) (int, string, error) {
	if f.reviewReason != "" {
		return f.reviewScore, f.reviewReason, nil
	}
	return f.reviewScore, "资料评分结果", nil
}

// ResumeAfterReply 记录主流程是否发出了回复后的索要指令，不访问招聘网站。
func (f *replyFixture) ResumeAfterReply(context.Context, platformcore.Executor, platformcore.ReplyConversation, map[string]any, int, float64) (string, error) {
	f.resumeCalls++
	return "requested", nil
}

// newReplyFlowFixture 为防重测试创建真实存储、稳定会话和模拟的外部能力。
func newReplyFlowFixture(t *testing.T) (*replyFlow, *replyFixture, platformcore.ReplyContext) {
	t.Helper()
	f := &replyFixture{confirm: true}
	c := platformcore.ReplyContext{ResumeStatus: "none", Conversation: platformcore.ReplyConversation{ID: "c1", PositionID: "job1"}, Messages: []platformcore.ReplyMessage{{ID: "m1", Direction: "inbound", Kind: "text", Text: "你好"}}}
	flow := &replyFlow{db: openRunnerTestDB(t), runtime: f, generator: f, target: platformcore.ReplyTarget{PositionID: "job1"}, scope: "profile-hash", platform: "boss", positionID: "position1", runID: "run1"}
	return flow, f, c
}

// TestReplyFlowDedup 验证发送先落库，并且跨任务换措辞不会重复调用 AI 或发送。
func TestReplyFlowDedup(t *testing.T) {
	flow, f, c := newReplyFlowFixture(t)
	f.send = func() (bool, error) {
		record, err := flow.db.FindAutoReply(t.Context(), localdb.AutoReplyRecord{ProfileScope: "profile-hash", Platform: "boss", ConversationID: "c1", InboundFingerprint: platformcore.ReplyMessageFingerprint("c1", c.Messages[0])})
		if err != nil || record.Status != "sending" {
			t.Fatalf("发送前未落库：%+v %v", record, err)
		}
		return true, nil
	}
	outcome, err := flow.process(t.Context(), c)
	if err != nil || outcome != "sent" {
		t.Fatalf("处理失败：%s %v", outcome, err)
	}
	flow.runID = "run2"
	flow.positionID = "position2"
	outcome, err = flow.process(t.Context(), c)
	if outcome != "skipped" || f.sends != 1 || f.generations != 1 {
		t.Fatalf("重复回复：%s %v %+v", outcome, err, f)
	}
}

// TestReplyFlowRejectedUsesFixedTemplate 验证低分回复使用固定话术并落库，不调用 AI、不索要、不重复发送。
func TestReplyFlowRejectedUsesFixedTemplate(t *testing.T) {
	for _, tc := range []struct {
		name, template, want string
		score                int
		threshold            float64
	}{
		{"初筛零分用岗位话术", "感谢关注，目前经验与岗位要求不符。", "感谢关注，目前经验与岗位要求不符。", 0, 70},
		{"详细评分未过用岗位话术", "暂不匹配，感谢你的时间。", "暂不匹配，感谢你的时间。", 69, 70},
		{"未设置话术用默认", "", "感谢你的关注，我们看了你的信息，跟我们的岗位要求不匹配。下次有机会再合作。", 0, 70},
		{"空白话术用默认", " \n\t ", "感谢你的关注，我们看了你的信息，跟我们的岗位要求不匹配。下次有机会再合作。", 0, 70},
		{"按岗位自定义阈值拒绝", "暂不匹配，感谢关注。", "暂不匹配，感谢关注。", 79, 80},
	} {
		t.Run(tc.name, func(t *testing.T) {
			flow, f, c := newReplyFlowFixture(t)
			flow.positionSnapshot = map[string]any{"ai_config": map[string]any{"greet_score_threshold": tc.threshold, "reply_reject_template": tc.template}}
			flow.rejectTemplate = positionRejectTemplate(localdb.Position{PositionSnapshot: flow.positionSnapshot})
			f.reviewScore = tc.score
			f.generate = func(context.Context) (string, error) { return "请发送最新简历", nil }
			outcome, err := flow.process(t.Context(), c)
			if err != nil || outcome != "sent" {
				t.Fatalf("拒绝回复失败：%s %v", outcome, err)
			}
			if f.stagedText != tc.want || f.sentText != tc.want {
				t.Fatalf("应输入并发送拒绝话术 %q，实际输入=%q，发送=%q", tc.want, f.stagedText, f.sentText)
			}
			if f.generations != 0 || f.sends != 1 {
				t.Fatalf("拒绝分支调用 AI=%d，发送次数=%d", f.generations, f.sends)
			}
			record, err := flow.db.FindAutoReply(t.Context(), localdb.AutoReplyRecord{ProfileScope: flow.scope, Platform: flow.platform, ConversationID: c.Conversation.ID, InboundFingerprint: platformcore.ReplyMessageFingerprint(c.Conversation.ID, c.Messages[0])})
			if err != nil || record.Status != "sent" || record.ReplyFingerprint != platformcore.ReplyHash(tc.want) {
				t.Fatalf("未保存拒绝回复确认结果：%+v %v", record, err)
			}
			action, err := flow.resumeAfterReplyIfNeeded(t.Context(), c.Conversation)
			if err != nil || action != "" || f.resumeCalls != 0 {
				t.Fatalf("拒绝后仍索要简历：action=%q calls=%d err=%v", action, f.resumeCalls, err)
			}
			outcome, _ = flow.process(t.Context(), c)
			if outcome != "skipped" || f.sends != 1 || f.generations != 0 {
				t.Fatalf("拒绝回复未防重：%s sends=%d generations=%d", outcome, f.sends, f.generations)
			}
		})
	}
}

// TestReplyFlowQualifiedKeepsAI 验证达到岗位阈值仍生成正常回复，不误用已配置的拒绝话术。
func TestReplyFlowQualifiedKeepsAI(t *testing.T) {
	flow, f, c := newReplyFlowFixture(t)
	flow.positionSnapshot = map[string]any{"ai_config": map[string]any{"greet_score_threshold": 60.0}}
	flow.rejectTemplate = "暂不匹配，感谢关注。"
	f.reviewScore = 60
	f.generate = func(context.Context) (string, error) { return "你好，请发送最新简历。", nil }
	outcome, err := flow.process(t.Context(), c)
	if err != nil || outcome != "sent" || f.sentText != "你好，请发送最新简历。" || f.generations != 1 {
		t.Fatalf("合格候选人未正常生成回复：%s %v %+v", outcome, err, f)
	}
	action, err := flow.resumeAfterReplyIfNeeded(t.Context(), c.Conversation)
	if err != nil || action != "requested" || f.resumeCalls != 1 {
		t.Fatalf("合格候选人未继续索要：%q %v", action, err)
	}
}

// TestReplyFlowAlreadyRejectedDefersToAI 验证会话已发过拒绝话术后，评分不达标不再发第二条拒绝，改交 AI 结合上下文判断。
func TestReplyFlowAlreadyRejectedDefersToAI(t *testing.T) {
	flow, f, c := newReplyFlowFixture(t)
	flow.positionSnapshot = map[string]any{"ai_config": map[string]any{"greet_score_threshold": 70.0}}
	flow.rejectTemplate = "暂不匹配，感谢关注。"
	c.Messages = []platformcore.ReplyMessage{
		{ID: "m1", Direction: "inbound", Kind: "text", Text: "你好"},
		{ID: "m2", Direction: "outbound", Kind: "text", Text: "暂不匹配，感谢关注。"},
		{ID: "m3", Direction: "inbound", Kind: "text", Text: "好吧"},
	}
	f.reviewScore = 0
	f.action = "skip"
	outcome, err := flow.process(t.Context(), c)
	if err != nil || outcome != "skipped" {
		t.Fatalf("已拒绝会话应交AI判断后跳过：%s %v", outcome, err)
	}
	if f.generations != 1 {
		t.Fatalf("已拒绝会话未交AI判断：generations=%d", f.generations)
	}
	if f.sends != 0 || f.stagedText != "" {
		t.Fatalf("已拒绝会话仍发出第二条拒绝：sends=%d staged=%q", f.sends, f.stagedText)
	}
}

// TestReplyFlowAlreadyRejectedDefaultTemplate 验证系统默认拒绝话术已发过后，岗位自定义话术也不得重复拒绝。
func TestReplyFlowAlreadyRejectedDefaultTemplate(t *testing.T) {
	flow, f, c := newReplyFlowFixture(t)
	flow.positionSnapshot = map[string]any{"ai_config": map[string]any{"greet_score_threshold": 70.0}}
	flow.rejectTemplate = "暂不匹配，感谢关注。"
	c.Messages = []platformcore.ReplyMessage{
		{ID: "m1", Direction: "inbound", Kind: "text", Text: "你好"},
		{ID: "m2", Direction: "outbound", Kind: "text", Text: defaultRejectTemplate()},
		{ID: "m3", Direction: "inbound", Kind: "text", Text: "好吧"},
	}
	f.reviewScore = 0
	f.action = "skip"
	outcome, err := flow.process(t.Context(), c)
	if err != nil || outcome != "skipped" || f.generations != 1 || f.sends != 0 {
		t.Fatalf("默认拒绝话术已发过仍重复拒绝：%s %v generations=%d sends=%d", outcome, err, f.generations, f.sends)
	}
}

// TestReplyFlowRejectedFromScreening 验证已有低分记录触发的拒绝同样不调用 AI 或索要简历。
func TestReplyFlowRejectedFromScreening(t *testing.T) {
	flow, f, c := newReplyFlowFixture(t)
	c.Conversation.Name = "测试候选人"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/positions/position1/screenings/find" || r.URL.Query().Get("name") != "测试候选人" {
			t.Errorf("扫描记录请求不匹配：%s %s", r.Method, r.URL)
			http.Error(w, "请求不匹配", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"item":{"id":"screen1","position_id":"position1","platform":"boss","platform_candidate_id":"c1","candidate_name":"测试候选人","score":0,"status":"skipped","resume_status":"none","source":"greeting"}}`))
	}))
	defer server.Close()
	flow.cloudClient = cloudapi.New(server.URL)
	flow.token = "test-token"
	flow.positionSnapshot = map[string]any{"ai_config": map[string]any{"greet_score_threshold": 70.0}}
	flow.rejectTemplate = "暂不匹配，感谢关注。"
	f.reviewScore = 90
	outcome, err := flow.process(t.Context(), c)
	if err != nil || outcome != "sent" || f.sentText != "暂不匹配，感谢关注。" || f.generations != 0 {
		t.Fatalf("低分记录未使用固定拒绝话术：%s %v %+v", outcome, err, f)
	}
	action, err := flow.resumeAfterReplyIfNeeded(t.Context(), c.Conversation)
	if err != nil || action != "" || f.resumeCalls != 0 {
		t.Fatalf("拒绝后被后续评分触发索要：%q %v calls=%d", action, err, f.resumeCalls)
	}
}

// TestReplyFlowRejectedThenQualified 验证同一任务拒绝一人后，不影响下一位合格候选人的 AI 回复和索要。
func TestReplyFlowRejectedThenQualified(t *testing.T) {
	flow, f, c := newReplyFlowFixture(t)
	flow.positionSnapshot = map[string]any{"ai_config": map[string]any{"greet_score_threshold": 70.0}}
	flow.rejectTemplate = "暂不匹配，感谢关注。"
	f.reviewScore = 0
	outcome, err := flow.process(t.Context(), c)
	if err != nil || outcome != "sent" {
		t.Fatalf("首位候选人处理失败：%s %v", outcome, err)
	}
	_, _ = flow.resumeAfterReplyIfNeeded(t.Context(), c.Conversation)
	c.Conversation.ID = "c2"
	f.reviewScore = 70
	outcome, err = flow.process(t.Context(), c)
	if err != nil || outcome != "sent" || f.sentText != "你好" || f.generations != 1 {
		t.Fatalf("拒绝状态影响后续回复：%s %v %+v", outcome, err, f)
	}
	action, err := flow.resumeAfterReplyIfNeeded(t.Context(), c.Conversation)
	if err != nil || action != "requested" || f.resumeCalls != 1 {
		t.Fatalf("后续候选人索要分流错误：%q %v calls=%d", action, err, f.resumeCalls)
	}
}

// TestReplyFlowRejectedSafety 验证固定拒绝话术仍受停止、草稿、上下文变化和长度限制保护。
func TestReplyFlowRejectedSafety(t *testing.T) {
	for _, name := range []string{"处理前停止", "输入后停止", "过期", "草稿", "超长话术", "存储失败"} {
		t.Run(name, func(t *testing.T) {
			flow, f, c := newReplyFlowFixture(t)
			flow.positionSnapshot = map[string]any{"ai_config": map[string]any{"greet_score_threshold": 70.0}}
			flow.rejectTemplate = "暂不匹配，感谢关注。"
			f.reviewScore = 0
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			switch name {
			case "处理前停止":
				cancel()
			case "输入后停止":
				f.stage = func() error { cancel(); return nil }
			case "过期":
				f.stale = true
			case "草稿":
				c.Draft = "人工文字"
			case "超长话术":
				flow.rejectTemplate = strings.Repeat("拒", 1001)
			case "存储失败":
				_ = flow.db.Close()
			}
			outcome, err := flow.process(ctx, c)
			if outcome == "sent" || f.sends != 0 {
				t.Fatalf("不安全条件发送了拒绝回复：%s %v", outcome, err)
			}
			if f.generations != 0 {
				t.Fatalf("拒绝分支调用了 AI：%d", f.generations)
			}
		})
	}
}

// TestReplyFlowSafetyFailures 验证停止、过期、草稿、AI错误和存储失败时发送次数为零。
func TestReplyFlowSafetyFailures(t *testing.T) {
	for _, name := range []string{"停止生成", "停止输入后", "过期", "草稿", "AI错误", "存储失败", "我方消息", "系统消息"} {
		t.Run(name, func(t *testing.T) {
			flow, f, c := newReplyFlowFixture(t)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			switch name {
			case "停止生成":
				f.generate = func(context.Context) (string, error) { cancel(); return "旧答案", nil }
			case "停止输入后":
				f.stage = func() error { cancel(); return nil }
			case "过期":
				f.stale = true
			case "草稿":
				c.Draft = "人工文字"
			case "AI错误":
				f.generate = func(context.Context) (string, error) { return "", errors.New("外部请求失败") }
			case "存储失败":
				_ = flow.db.Close()
			case "我方消息":
				c.Messages[0].Direction = "outbound"
			case "系统消息":
				c.Messages[0].Kind = "system"
			}
			_, _ = flow.process(ctx, c)
			if f.sends != 0 {
				t.Fatalf("不安全条件下发送了 %d 次", f.sends)
			}
		})
	}
}

// TestReplyFlowUnknown 验证点击超时仅核对页面，不自动再次发送；已发送后停止仍完成必要确认。
func TestReplyFlowUnknown(t *testing.T) {
	flow, f, c := newReplyFlowFixture(t)
	f.confirm = false
	f.send = func() (bool, error) { return true, context.DeadlineExceeded }
	outcome, err := flow.process(t.Context(), c)
	if err != nil || outcome != "unknown" {
		t.Fatalf("结果不明未保留：%s %v", outcome, err)
	}
	_, _ = flow.process(t.Context(), c)
	if f.sends != 1 || f.generations != 1 {
		t.Fatal("结果不明被重发")
	}
	f.confirm = true
	outcome, err = flow.process(t.Context(), c)
	if outcome != "skipped" || f.sends != 1 {
		t.Fatalf("未知结果应跳过：%s %v", outcome, err)
	}
	flow, f, c = newReplyFlowFixture(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	f.send = func() (bool, error) { cancel(); return true, nil }
	outcome, err = flow.process(ctx, c)
	if err != nil || outcome != "sent" {
		t.Fatalf("发送开始后未确认结果：%s %v", outcome, err)
	}
}

// TestReplyFlowRestartAfterOutbound 验证真实发送后新增我方消息再运行，已发送和未知结果均不重复输入。
func TestReplyFlowRestartAfterOutbound(t *testing.T) {
	for _, confirmed := range []bool{true, false} {
		t.Run(fmt.Sprint(confirmed), func(t *testing.T) {
			flow, f, c := newReplyFlowFixture(t)
			c.Messages[0].ID = ""
			f.confirm = confirmed
			f.generate = func(context.Context) (string, error) { return "暂不匹配，感谢关注。", nil }
			if _, err := flow.process(t.Context(), c); err != nil {
				t.Fatal(err)
			}
			c.Messages = append(c.Messages, platformcore.ReplyMessage{Direction: "outbound", Kind: "text", Text: f.sentText})
			f.stagedText = ""
			restarted := *flow
			restarted.runID = "new-run"
			outcome, _ := restarted.process(t.Context(), c)
			if outcome != "skipped" || f.stagedText != "" || f.sends != 1 || f.generations != 1 {
				t.Fatalf("重启重复回复：%s stage=%q sends=%d AI=%d", outcome, f.stagedText, f.sends, f.generations)
			}
		})
	}
}

// decisionTestClient 仅替换远程模型响应，保留真实提示词、解析、主流程和 SQLite。
func decisionTestClient(t *testing.T, response *string, requests *int, captured *string) *localai.Client {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*requests++
		var body struct {
			Messages []struct{ Role, Content string } `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if captured != nil && len(body.Messages) == 2 {
			*captured = body.Messages[1].Content
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"content": *response}}}})
	}))
	t.Cleanup(server.Close)
	return localai.New(localdb.AIConfig{BaseURL: server.URL, APIKey: "test", Model: "test"})
}

// TestReplyFlowDecisionNoSend 验证无需回复和无法确定均持久化，不输入、不索要；新问题仍能回复。
func TestReplyFlowDecisionNoSend(t *testing.T) {
	for _, action := range []string{"skip", "uncertain"} {
		t.Run(action, func(t *testing.T) {
			flow, f, c := newReplyFlowFixture(t)
			calls := 0
			response := fmt.Sprintf(`{"action":%q,"text":"","reason":"当前无需自动发送","request_resume":false}`, action)
			flow.generator = decisionTestClient(t, &response, &calls, nil)
			outcome, err := flow.process(t.Context(), c)
			if err != nil || outcome != "skipped" || f.sends != 0 || f.stagedText != "" {
				t.Fatalf("不发送决策被发送：%s %v text=%q", outcome, err, f.sentText)
			}
			record, err := flow.db.FindAutoReply(t.Context(), localdb.AutoReplyRecord{ProfileScope: flow.scope, Platform: flow.platform, ConversationID: "c1", InboundFingerprint: platformcore.ReplyMessageFingerprint("c1", c.Messages[0])})
			if err != nil || record.Status != action {
				t.Fatalf("未保存决策：%+v %v", record, err)
			}
			restarted := *flow
			restarted.runID = "next-run"
			_, _ = restarted.process(t.Context(), c)
			if calls != 1 || f.sends != 0 {
				t.Fatalf("同一消息重复判断：AI=%d sends=%d", calls, f.sends)
			}
			c.Messages = append(c.Messages, platformcore.ReplyMessage{ID: "m2", Direction: "inbound", Kind: "text", Text: "几点上班？"})
			response = `{"action":"reply","text":"上午九点上班。","reason":"岗位已配置办公时间","request_resume":false}`
			outcome, err = restarted.process(t.Context(), c)
			if err != nil || outcome != "sent" || f.sentText != "上午九点上班。" || calls != 2 {
				t.Fatalf("新问题无法回复：%s %v %q calls=%d", outcome, err, f.sentText, calls)
			}
		})
	}
}

// TestReplyFlowDecisionSafety 验证异常决策、停止和上下文变化不会落成已处理或发送正文。
func TestReplyFlowDecisionSafety(t *testing.T) {
	for _, tc := range []struct {
		name, response string
		stale          bool
	}{
		{"非JSON", "请发送简历", false},
		{"未知动作", `{"action":"send","text":"你好","reason":"测试","request_resume":false}`, false},
		{"跳过却含正文", `{"action":"skip","text":"你好","reason":"测试","request_resume":false}`, false},
		{"空回复", `{"action":"reply","text":"","reason":"测试","request_resume":false}`, false},
		{"跳过缺少正文", `{"action":"skip","reason":"测试","request_resume":false}`, false},
		{"生成后会话变化", `{"action":"reply","text":"你好","reason":"测试","request_resume":false}`, true},
		{"跳过前会话变化", `{"action":"skip","text":"","reason":"测试","request_resume":false}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			flow, f, c := newReplyFlowFixture(t)
			calls := 0
			flow.generator = decisionTestClient(t, &tc.response, &calls, nil)
			f.stale = tc.stale
			_, err := flow.process(t.Context(), c)
			if err == nil || f.sends != 0 || f.stagedText != "" {
				t.Fatalf("不安全决策被使用：err=%v sent=%q", err, f.sentText)
			}
			_, err = flow.db.FindAutoReply(t.Context(), localdb.AutoReplyRecord{ProfileScope: flow.scope, Platform: flow.platform, ConversationID: "c1", InboundFingerprint: platformcore.ReplyMessageFingerprint("c1", c.Messages[0])})
			if err == nil {
				t.Fatal("不安全决策被标记已处理")
			}
		})
	}
}

// TestReplyFlowReceivedResumeAnswers 验证收到简历仍回答问题，最近多条对话和 FAQ 被传给 AI，且不再索要。
func TestReplyFlowReceivedResumeAnswers(t *testing.T) {
	flow, f, c := newReplyFlowFixture(t)
	flow.positionSnapshot = map[string]any{"ai_config": map[string]any{"greet_score_threshold": 70.0}}
	f.reviewScore = 100
	f.reviewReason = "简历已收到"
	flow.request.FAQ = []localai.FAQEntry{{Q: "办公时间", A: "9:00-18:00"}}
	for i := 0; i < 22; i++ {
		c.Messages = append(c.Messages, platformcore.ReplyMessage{ID: fmt.Sprint(i + 2), Direction: "inbound", Kind: "text", Text: fmt.Sprintf("问题%02d", i)})
	}
	c.Messages = append(c.Messages, platformcore.ReplyMessage{ID: "last", Direction: "inbound", Kind: "text", Text: "几点上班？"})
	calls := 0
	captured := ""
	response := `{"action":"reply","text":"上午九点上班。","reason":"根据办公时间回答","request_resume":false}`
	flow.generator = decisionTestClient(t, &response, &calls, &captured)
	outcome, err := flow.process(t.Context(), c)
	if err != nil || outcome != "sent" || f.sentText != "上午九点上班。" {
		t.Fatalf("收到简历阻止答疑：%s %v %q", outcome, err, f.sentText)
	}
	for _, want := range []string{"几点上班？", "问题21", "问题20", "9:00-18:00", "received"} {
		if !strings.Contains(captured, want) {
			t.Errorf("AI缺少上下文：%s", want)
		}
	}
	if strings.Contains(captured, "问题00") {
		t.Error("没有限制最近对话长度")
	}
	_, _ = flow.resumeAfterReplyIfNeeded(t.Context(), c.Conversation)
	if f.resumeCalls != 0 {
		t.Fatal("收到简历后仍然索要")
	}
}

// TestReplyFlowRepeatedQuestion 验证无消息 ID 时，旧问答不会挡住候选人再次提问，同一轮仍只发送一次。
func TestReplyFlowRepeatedQuestion(t *testing.T) {
	flow, f, c := newReplyFlowFixture(t)
	c.Messages[0].ID = ""
	c.Messages[0].Text = "双休吗？"
	response := `{"action":"reply","text":"是双休。","reason":"岗位资料明确","request_resume":false}`
	calls := 0
	flow.generator = decisionTestClient(t, &response, &calls, nil)
	for round := 0; round < 2; round++ {
		outcome, err := flow.process(t.Context(), c)
		if err != nil || outcome != "sent" {
			t.Fatalf("第%d轮未回答：%s %v", round+1, outcome, err)
		}
		c.Messages = append(c.Messages, platformcore.ReplyMessage{Direction: "outbound", Kind: "text", Text: "是双休。"})
		if outcome, _ := flow.process(t.Context(), c); outcome != "skipped" {
			t.Fatal("已回答后未跳过")
		}
		if calls != round+1 || f.sends != round+1 {
			t.Fatalf("同一轮重复处理：AI=%d sends=%d", calls, f.sends)
		}
		c.Messages = append(c.Messages, platformcore.ReplyMessage{Direction: "inbound", Kind: "text", Text: "双休吗？"})
	}
}

// TestReplyFlowHumanReplySkipsAI 验证没有程序发送记录时，人工已回答也不能被系统通知再次触发。
func TestReplyFlowHumanReplySkipsAI(t *testing.T) {
	flow, f, c := newReplyFlowFixture(t)
	c.Messages = append(c.Messages,
		platformcore.ReplyMessage{Direction: "outbound", Kind: "text", Text: "人工已答复"},
		platformcore.ReplyMessage{Direction: "system", Kind: "system", Text: "对方已读"},
	)
	outcome, _ := flow.process(t.Context(), c)
	if outcome != "skipped" || f.generations != 0 || f.sends != 0 || f.stagedText != "" {
		t.Fatalf("人工答复后重复处理：%s AI=%d sends=%d", outcome, f.generations, f.sends)
	}
}

// TestReplyFlowResumeDecision 验证 AI 可只答疑不索要，已索要、已收到或未知状态始终禁止再次索要。
func TestReplyFlowResumeDecision(t *testing.T) {
	for _, tc := range []struct {
		name, status, action string
		request              bool
		wantCalls            int
	}{
		{"允许且需要索要", "none", "reply", true, 1},
		{"只需回答", "none", "reply", false, 0},
		{"已索要继续答疑", "requested", "reply", false, 0},
		{"已收到继续答疑", "received", "reply", false, 0},
		{"状态未知只答疑", "unknown", "reply", false, 0},
		{"无需回答不索要", "none", "skip", false, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			flow, f, c := newReplyFlowFixture(t)
			flow.positionSnapshot = map[string]any{"ai_config": map[string]any{"greet_score_threshold": 70.0}}
			f.reviewScore = 90
			c.ResumeStatus = tc.status
			text := "上午九点上班。"
			if tc.action == "skip" {
				text = ""
			}
			response := fmt.Sprintf(`{"action":%q,"text":%q,"reason":"根据上下文决定","request_resume":%t}`, tc.action, text, tc.request)
			calls := 0
			captured := ""
			flow.generator = decisionTestClient(t, &response, &calls, &captured)
			outcome, err := flow.process(t.Context(), c)
			wantOutcome := "sent"
			if tc.action == "skip" {
				wantOutcome = "skipped"
			}
			if err != nil || outcome != wantOutcome {
				t.Fatalf("决策未生效：%s %v", outcome, err)
			}
			if !strings.Contains(captured, fmt.Sprintf("allow_resume_request=%t", tc.status == "none")) {
				t.Fatalf("AI未收到正确索要许可：status=%s", tc.status)
			}
			if tc.action == "reply" && f.sentText != text {
				t.Fatalf("发送内容不是正文：%q", f.sentText)
			}
			if _, err := flow.resumeAfterReplyIfNeeded(t.Context(), c.Conversation); err != nil {
				t.Fatal(err)
			}
			if f.resumeCalls != tc.wantCalls {
				t.Fatalf("索要次数=%d want=%d", f.resumeCalls, tc.wantCalls)
			}
		})
	}
}

// TestReplyTaskConfiguration 验证任务默认、配置提示词隔离和自动回复独立轮次。
func TestReplyTaskConfiguration(t *testing.T) {
	if got, err := normalizeTaskType(""); err != nil || got != "greeting" {
		t.Fatal(got, err)
	}
	if _, err := normalizeTaskType("other"); err == nil {
		t.Fatal("接受了未知任务")
	}
	if scanRounds(StartOptions{TaskType: "auto_reply"}) != 1 {
		t.Fatal("回复错误沿用了三轮默认")
	}
	cfg := aiConfigFromCloud(map[string]any{"reply_system_prompt": "回复规则", "prompt_template": "评分规则"})
	if cfg.ReplySystemPrompt != "回复规则" {
		t.Fatal("回复配置未透传")
	}
}

// TestBrowserLeaseAcrossTasks 验证不同岗位不能并发，取消后必须等旧任务退出再释放。
func TestBrowserLeaseAcrossTasks(t *testing.T) {
	r := newTestRunner(t, openRunnerTestDB(t), &fakeWorker{})
	ctx, cancel := context.WithCancel(context.Background())
	if !r.setRunning("one", cancel, StartOptions{}) {
		t.Fatal("首任务未取得占用")
	}
	if r.setRunning("two", func() {}, StartOptions{}) {
		t.Fatal("不同岗位抢占了浏览器")
	}
	r.markUserStoppedAndCancel("one")
	if ctx.Err() == nil {
		t.Fatal("停止未取消上下文")
	}
	if r.setRunning("two", func() {}, StartOptions{}) {
		t.Fatal("旧任务退出前释放了占用")
	}
	r.clear("one")
	if !r.setRunning("two", func() {}, StartOptions{}) {
		t.Fatal("退出后未释放占用")
	}
	r.clear("two")
}

// TestResumeBrowserLeaseHandoff 验证收尾预先继承占用，并等待主任务退出后才操作页面。
func TestResumeBrowserLeaseHandoff(t *testing.T) {
	r := newTestRunner(t, openRunnerTestDB(t), &fakeWorker{})
	r.setRunning("one", func() {}, StartOptions{})
	release, ready, ok := r.reserveResumeBrowser("one")
	if !ok {
		t.Fatal("无法继承占用")
	}
	select {
	case <-ready:
		t.Fatal("主任务未退出就允许收尾")
	default:
	}
	r.clear("one")
	select {
	case <-ready:
	default:
		t.Fatal("主任务退出后未唤醒收尾")
	}
	if r.setRunning("two", func() {}, StartOptions{}) {
		t.Fatal("收尾未结束却开始新任务")
	}
	if _, _, ok := r.reserveResumeBrowser("two"); ok {
		t.Fatal("繁忙时允许另一个收尾")
	}
	release()
	if !r.setRunning("two", func() {}, StartOptions{}) {
		t.Fatal("收尾后未释放")
	}
	r.clear("two")
}

// onceWorker 证明自动回复浏览器动作只走不重试调用，旧 Call 路径被拒绝。
type onceWorker struct{ calls []string }

// Start 返回浏览器已启动。
func (w *onceWorker) Start(context.Context) (browser.WorkerStatus, error) {
	return browser.WorkerStatus{Running: true}, nil
}

// Call 模拟旧重试路径并拒绝服务。
func (w *onceWorker) Call(context.Context, string, any) (map[string]any, error) {
	return nil, errors.New("自动回复不应使用可重试调用")
}

// CallOnce 记录调用路径并返回成功。
func (w *onceWorker) CallOnce(_ context.Context, path string, _ any) (map[string]any, error) {
	w.calls = append(w.calls, path)
	return map[string]any{"data": map[string]any{}}, nil
}

// newReplyConversation 返回属于当前岗位的一条候选人文本会话。
func newReplyConversation() platformcore.ReplyContext {
	return platformcore.ReplyContext{Conversation: platformcore.ReplyConversation{ID: "c1", PositionID: "job1", PositionName: "Go"}, Messages: []platformcore.ReplyMessage{{ID: "m1", Direction: "inbound", Kind: "text", Text: "你好"}}}
}

// TestRunAutoReplyRound 验证整轮编排：只走不重试调用、逐会话处理并聚合统计。
func TestRunAutoReplyRound(t *testing.T) {
	worker := &onceWorker{}
	r := newTestRunner(t, openRunnerTestDB(t), worker)
	fixture := &replyFixture{confirm: true}
	c := newReplyConversation()
	fixture.scan = []platformcore.ReplyConversation{c.Conversation}
	fixture.reads = []platformcore.ReplyContext{c}
	options := StartOptions{TaskType: "auto_reply", ScanRounds: 1, CloudRunID: "run-1"}
	stats := r.runAutoReply(t.Context(), localdb.Position{ID: "p1", Name: "Go", PlatformID: "boss"}, options, fixture, fixture, nil)
	if stats.Replied != 1 || stats.Checked != 1 || fixture.sends != 1 || fixture.prepared != 1 {
		t.Fatalf("整轮编排错误：%+v sends=%d prepared=%d", stats, fixture.sends, fixture.prepared)
	}
	if len(worker.calls) == 0 || worker.calls[0] != "/api/v1/browser/start" {
		t.Fatalf("浏览器启动顺序错误：%v", worker.calls)
	}
}

// TestRunAutoReplyFailureLimit 验证连续会话错误三次后整任务停止，不再读取后续会话。
func TestRunAutoReplyFailureLimit(t *testing.T) {
	r := newTestRunner(t, openRunnerTestDB(t), &onceWorker{})
	fixture := &replyFixture{confirm: true}
	fixture.send = func() (bool, error) { return false, errors.New("页面动作失败") }
	c := newReplyConversation()
	fixture.scan = []platformcore.ReplyConversation{c.Conversation, c.Conversation, c.Conversation, c.Conversation}
	fixture.reads = []platformcore.ReplyContext{c, c, c, c}
	stats := r.runAutoReply(t.Context(), localdb.Position{ID: "p1", Name: "Go", PlatformID: "boss"}, StartOptions{TaskType: "auto_reply", ScanRounds: 1, CloudRunID: "run-1"}, fixture, fixture, nil)
	if stats.Failed != 3 || fixture.readCalls != 3 {
		t.Fatalf("连续失败未按三次停止：%+v reads=%d", stats, fixture.readCalls)
	}
}

// TestAutoReplyStopCancelsImmediately 验证自动回复任务可被识别并在停止时立即取消。
func TestAutoReplyStopCancelsImmediately(t *testing.T) {
	r := newTestRunner(t, openRunnerTestDB(t), &fakeWorker{})
	_, cancel := context.WithCancel(context.Background())
	defer cancel()
	if !r.setRunning("p1", cancel, StartOptions{TaskType: "auto_reply"}) {
		t.Fatal("未取得占用")
	}
	if !r.autoReplyRunning("p1") {
		t.Fatal("未识别自动回复任务")
	}
	if r.autoReplyRunning("missing") {
		t.Fatal("未知岗位误报")
	}
	r.clear("p1")
	if r.autoReplyRunning("p1") {
		t.Fatal("清理后仍误报")
	}
}
