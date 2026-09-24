// 本文件用模拟页面事实验证 Boss 会话隔离与发送保护；模拟选择器不用于真实配置。
package boss

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"goodhr5/local-agent-go/internal/platformcore"
)

// replyTestConfig 仅为模拟执行器提供选择器，不能作为 Boss 页面证据。
func replyTestConfig() replyPageConfig {
	spec := func(key string) platformcore.SelectorSpec { return platformcore.SelectorSpec{Selectors: []string{key}} }
	fields := map[string]platformcore.SelectorField{}
	for _, key := range []string{"id", "position_id", "position_name", "name"} {
		fields[key] = platformcore.SelectorField{Attribute: key}
	}
	messageFields := map[string]platformcore.SelectorField{}
	for _, key := range []string{"id", "direction", "kind", "timestamp", "text"} {
		messageFields[key] = platformcore.SelectorField{Attribute: key}
	}
	return replyPageConfig{Jobs: spec("jobs"), JobFields: map[string]platformcore.SelectorField{"id": {Attribute: "id"}, "name": {Attribute: "name"}}, Verified: true, MessagesURL: "https://fixture.invalid/chat", Unread: spec("unread"), UnreadFilter: spec("unread_filter"), Conversation: spec("conversation"), Active: spec("active"), Messages: spec("messages"), Input: spec("input"), Send: spec("send"), IdentityAttribute: "id", ConversationFields: fields, MessageFields: messageFields, Directions: map[string]string{"in": "inbound", "out": "outbound"}, Kinds: map[string]string{"text": "text", "file": "attachment"}}
}

// replyPage 模拟会话点击后列表重排和已读，不模拟业务决策。
type replyPage struct {
	envelope         bool
	lastOpen         any
	jobs             []map[string]string
	active           string
	unread           []string
	draft            string
	messages         map[string][]map[string]string
	sent             []string
	calls            int
	switchAfterInput bool
	resumeReceived   bool
	resumeRequested  bool
	resumeError      bool
	resumeMalformed  string
}

// newReplyPage 返回两个同名候选人的独立会话。
func newReplyPage() *replyPage {
	return &replyPage{unread: []string{"a", "b"}, messages: map[string][]map[string]string{"a": {{"id": "a1", "direction": "in", "kind": "text", "text": "你好"}}, "b": {{"id": "b1", "direction": "in", "kind": "text", "text": "你好"}}}}
}

// pageItems 构造现有 Worker 边界的字段响应。
func pageItems(fields ...map[string]string) map[string]any {
	items := make([]any, 0, len(fields))
	for _, item := range fields {
		decoded := make(map[string]any, len(item))
		for key, value := range item {
			decoded[key] = value
		}
		items = append(items, map[string]any{"text": item["text"], "fields": decoded})
	}
	return map[string]any{"items": items, "count": len(items)}
}

// Post 只支持新流程使用的通用路由，错误的专用路由会使测试失败。
func (p *replyPage) Post(ctx context.Context, path string, payload any) (result map[string]any, err error) {
	defer func() {
		if p.envelope && err == nil {
			result = map[string]any{"ok": true, "data": result}
		}
	}()
	p.calls++
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if path == "/api/v1/page/open" {
		p.lastOpen = payload
		return map[string]any{}, nil
	}
	request, ok := payload.(platformcore.LocatorRequest)
	if !ok {
		return nil, errors.New("非强类型请求")
	}
	key := request.Selector.Selectors[0]
	identity := func(id string) map[string]string {
		return map[string]string{"id": id, "name": "同名", "position_id": "job1", "position_name": "Go"}
	}
	switch path {
	case "/api/v1/page/find-elements":
		switch key {
		case "jobs":
			return pageItems(p.jobs...), nil
		case "unread":
			fields := []map[string]string{}
			for _, id := range p.unread {
				fields = append(fields, identity(id))
			}
			return pageItems(fields...), nil
		case "active":
			if _, ok := request.Fields["text"]; ok {
				if p.resumeMalformed == "text" {
					return pageItems(map[string]string{}), nil
				}
				text := ""
				if p.resumeRequested {
					text = "简历请求已发送"
				}
				return pageItems(map[string]string{"text": text}), nil
			}
			return pageItems(identity(p.active)), nil
		case "resume-received":
			if p.resumeMalformed == "items" {
				return map[string]any{"items": "invalid"}, nil
			}
			if p.resumeError {
				return nil, errors.New("页面状态读取失败")
			}
			if p.resumeReceived {
				return pageItems(map[string]string{}), nil
			}
			return pageItems(), nil
		case "messages":
			return pageItems(p.messages[p.active]...), nil
		}
	case "/api/v1/page/click":
		if key == "unread_filter" {
			return map[string]any{"clicked": true}, nil
		}
		if key == "conversation" {
			p.active = request.Selector.Attributes["id"]
			p.unread = []string{"b"}
			p.draft = ""
			return map[string]any{"clicked": true}, nil
		}
		if key == "send" {
			p.sent = append(p.sent, p.active)
			p.messages[p.active] = append(p.messages[p.active], map[string]string{"id": "sent1", "direction": "out", "kind": "text", "text": p.draft})
			p.draft = ""
			return map[string]any{"clicked": true}, nil
		}
	case "/api/v1/page/extract-text":
		return map[string]any{"text": p.draft}, nil
	case "/api/v1/page/type":
		p.draft = request.Text
		if p.switchAfterInput {
			p.active = "b"
		}
		return map[string]any{"verified": true}, nil
	}
	return nil, errors.New("未预期的浏览器请求")
}

// Log 模拟不含聊天原文的日志出口。
func (*replyPage) Log(string, string) {}

// Delay 测试中不引入轮询等待。
func (*replyPage) Delay(context.Context, string, float64) error { return nil }

// TestAutoReplyUnverifiedBlocked 验证未取得 Boss 页面证据时不执行任何浏览器动作。
func TestAutoReplyUnverifiedBlocked(t *testing.T) {
	runtime := NewRuntime()
	page := newReplyPage()
	if runtime.AutoReplyAvailable() == nil {
		t.Fatal("未验证配置不能启用")
	}
	if err := runtime.PrepareReplyPage(t.Context(), page); err == nil || page.calls != 0 {
		t.Fatalf("未验证配置访问了页面：%v", err)
	}
}

// TestReplyTargetRequiresUniqueJob 验证云端岗位名称必须在页面岗位列表中完整且唯一。
func TestReplyTargetRequiresUniqueJob(t *testing.T) {
	cfg := replyTestConfig()
	runtime := &Runtime{replyConfig: &cfg}
	page := newReplyPage()
	for _, jobs := range [][]map[string]string{{{"id": "j1", "name": "Go"}}, {{"id": "j1", "name": "Go"}, {"id": "j2", "name": "Go"}}, {{"name": "Go..."}}} {
		page.jobs = jobs
		target, err := runtime.ResolveReplyTarget(t.Context(), page, "Go")
		if len(jobs) == 1 && jobs[0]["name"] == "Go" {
			if err != nil || target.PositionID != "j1" || !target.NameUnique {
				t.Fatalf("岗位映射错误：%+v %v", target, err)
			}
		} else if err == nil {
			t.Fatal("接受了同名或截断岗位")
		}
	}
	if len(page.sent) != 0 || page.active != "" {
		t.Fatal("岗位核对不应打开会话或发送")
	}
}

// TestReplyWorkerEnvelope 验证真实 Worker 的 data 包装不会被误读为空列表或空草稿。
func TestReplyWorkerEnvelope(t *testing.T) {
	cfg := replyTestConfig()
	runtime := &Runtime{replyConfig: &cfg}
	page := newReplyPage()
	page.envelope = true
	target := platformcore.ReplyTarget{PositionID: "job1"}
	conversations, err := runtime.ScanUnreadReplies(t.Context(), page, target, 100)
	if err != nil || len(conversations) != 2 {
		t.Fatalf("包装响应读取失败：%v %v", conversations, err)
	}
	value, err := runtime.ReadReplyContext(t.Context(), page, target, conversations[0])
	if err != nil || len(value.Messages) != 1 {
		t.Fatalf("上下文读取失败：%+v %v", value, err)
	}
}

// TestReplyReadSkipsDividerItems 验证日期分隔线等非消息元素（方向与类型双双映射为空）不进入消息列表，会话仍能通过上下文校验。
func TestReplyReadSkipsDividerItems(t *testing.T) {
	cfg := replyTestConfig()
	runtime := &Runtime{replyConfig: &cfg}
	page := newReplyPage()
	page.messages["a"] = []map[string]string{
		{"id": "", "direction": "", "kind": "", "timestamp": "2026-09-23", "text": ""},
		{"id": "a1", "direction": "in", "kind": "text", "text": "你好"},
		{"id": "", "direction": "", "kind": "", "timestamp": "2026-09-24", "text": ""},
	}
	target := platformcore.ReplyTarget{PositionID: "job1"}
	conversation := platformcore.ReplyConversation{ID: "a", PositionID: "job1", Name: "同名"}
	value, err := runtime.ReadReplyContext(t.Context(), page, target, conversation)
	if err != nil {
		t.Fatal(err)
	}
	if len(value.Messages) != 1 {
		t.Fatalf("分隔线条目进入消息列表：%+v", value.Messages)
	}
	if _, err := platformcore.ValidateReplyContext(value, target); err != nil {
		t.Fatalf("含分隔线的会话应通过校验：%v", err)
	}
}

// TestAutoReplyStableIdentity 验证列表重排、同名和点击后已读均不导致串会话。
func TestAutoReplyStableIdentity(t *testing.T) {
	cfg := replyTestConfig()
	runtime := &Runtime{replyConfig: &cfg}
	page := newReplyPage()
	target := platformcore.ReplyTarget{PositionID: "job1"}
	conversations, err := runtime.ScanUnreadReplies(t.Context(), page, target, 100)
	if err != nil || len(conversations) != 2 {
		t.Fatalf("扫描：%v %v", conversations, err)
	}
	for _, conversation := range conversations {
		value, err := runtime.ReadReplyContext(t.Context(), page, target, conversation)
		if err != nil {
			t.Fatal(err)
		}
		value, err = platformcore.ValidateReplyContext(value, target)
		if err != nil {
			t.Fatal(err)
		}
		if err := runtime.StageReply(t.Context(), page, target, value, "请介绍经验"); err != nil {
			t.Fatal(err)
		}
		attempted, err := runtime.SendReply(t.Context(), page, target, value, "请介绍经验")
		if err != nil || !attempted {
			t.Fatalf("发送：%v %v", attempted, err)
		}
		confirmed, err := runtime.ConfirmReply(t.Context(), page, target, conversation, value.InboundFingerprint, platformcore.ReplyHash("请介绍经验"))
		if err != nil || !confirmed {
			t.Fatalf("确认：%v %v", confirmed, err)
		}
	}
	if len(page.sent) != 2 || page.sent[0] != "a" || page.sent[1] != "b" {
		t.Fatalf("串会话：%v", page.sent)
	}
}

// TestAutoReplyDraftAndStaleContext 验证草稿、切换会话、新消息、人工回复与停止均阻断发送。
func TestAutoReplyDraftAndStaleContext(t *testing.T) {
	for _, kind := range []string{"draft", "switch", "new-message", "human-reply", "cancel"} {
		t.Run(kind, func(t *testing.T) {
			cfg := replyTestConfig()
			runtime := &Runtime{replyConfig: &cfg}
			page := newReplyPage()
			target := platformcore.ReplyTarget{PositionID: "job1"}
			conversation := platformcore.ReplyConversation{ID: "a", PositionID: "job1"}
			value, err := runtime.ReadReplyContext(t.Context(), page, target, conversation)
			if err != nil {
				t.Fatal(err)
			}
			value, err = platformcore.ValidateReplyContext(value, target)
			if err != nil {
				t.Fatal(err)
			}
			if kind == "draft" {
				page.draft = "人工草稿"
				if err := runtime.StageReply(t.Context(), page, target, value, "AI回复"); err == nil {
					t.Fatal("覆盖草稿")
				}
				return
			}
			if err := runtime.StageReply(t.Context(), page, target, value, "AI回复"); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			switch kind {
			case "switch":
				page.active = "b"
			case "new-message":
				page.messages["a"] = append(page.messages["a"], map[string]string{"id": "a2", "direction": "in", "kind": "text", "text": "还有问题"})
			case "human-reply":
				page.messages["a"] = append(page.messages["a"], map[string]string{"id": "a2", "direction": "out", "kind": "text", "text": "人工回答"})
			case "cancel":
				cancel()
			}
			attempted, err := runtime.SendReply(ctx, page, target, value, "AI回复")
			if err == nil || attempted || len(page.sent) > 0 {
				t.Fatalf("过期答案被发送：%v %v", attempted, err)
			}
		})
	}
}

// TestAutoReplyConfirmationEvidence 验证输入框清空或相同旧回答均不能证明本次发送成功。
func TestAutoReplyConfirmationEvidence(t *testing.T) {
	cfg := replyTestConfig()
	runtime := &Runtime{replyConfig: &cfg}
	page := newReplyPage()
	page.active = "a"
	target := platformcore.ReplyTarget{PositionID: "job1"}
	conversation := platformcore.ReplyConversation{ID: "a", PositionID: "job1"}
	inbound := platformcore.ReplyMessageFingerprint("a", platformcore.ReplyMessage{ID: "a1", Direction: "inbound"})
	page.messages["a"] = append([]map[string]string{{"id": "old", "direction": "out", "kind": "text", "text": "你好"}}, page.messages["a"]...)
	confirmed, err := runtime.ConfirmReply(t.Context(), page, target, conversation, inbound, platformcore.ReplyHash("你好"))
	if err != nil || confirmed {
		t.Fatalf("旧消息误报成功：%v %v", confirmed, err)
	}
}

// TestReplyConfigRequiresJobIdentity 验证岗位识别字段缺失时自动回复保持不可用。
func TestReplyConfigRequiresJobIdentity(t *testing.T) {
	for _, key := range []string{"id", "name"} {
		cfg := replyTestConfig()
		delete(cfg.JobFields, key)
		if err := (&Runtime{replyConfig: &cfg}).AutoReplyAvailable(); err == nil {
			t.Fatalf("缺少岗位字段 %s 仍可用", key)
		}
	}
}

// TestPrepareReplyPageStrictDisplay 验证消息页准备声明严格显示读取，启动校准不注入脚本。
func TestPrepareReplyPageStrictDisplay(t *testing.T) {
	cfg := replyTestConfig()
	runtime := &Runtime{replyConfig: &cfg}
	page := newReplyPage()
	if err := runtime.PrepareReplyPage(t.Context(), page); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(page.lastOpen)
	if err != nil {
		t.Fatal(err)
	}
	var sent map[string]any
	if err := json.Unmarshal(raw, &sent); err != nil {
		t.Fatal(err)
	}
	if sent["no_script"] != true {
		t.Fatalf("消息页准备未声明 no_script：%v", sent)
	}
}

// TestReplyResumeContext 验证简历状态只来自当前会话，不把平台通知误认为候选人。
func TestReplyResumeContext(t *testing.T) {
	for _, tc := range []struct {
		name, want                  string
		received, requested, broken bool
		malformed                   string
	}{
		{"未索要", "none", false, false, false, ""},
		{"已索要", "requested", false, true, false, ""},
		{"已收到", "received", true, true, false, ""},
		{"读取失败", "unknown", false, false, true, ""},
		{"错误元素格式", "unknown", false, false, false, "items"},
		{"缺少页面正文", "unknown", false, false, false, "text"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := replyTestConfig()
			cfg.ResumeReceived = platformcore.SelectorSpec{Selectors: []string{"resume-received"}}
			cfg.ResumeRequestedText = "简历请求已发送"
			cfg.Kinds["notice"] = "system"
			runtime := &Runtime{replyConfig: &cfg}
			page := newReplyPage()
			page.active = "a"
			page.resumeReceived, page.resumeRequested, page.resumeError = tc.received, tc.requested, tc.broken
			page.resumeMalformed = tc.malformed
			page.messages["a"] = append(page.messages["a"], map[string]string{"direction": "notice", "kind": "notice", "text": "系统通知"})
			value, err := runtime.readCurrentReply(t.Context(), page, platformcore.ReplyTarget{PositionID: "job1"}, platformcore.ReplyConversation{ID: "a", Name: "同名", PositionID: "job1"})
			if err != nil || value.ResumeStatus != tc.want {
				t.Fatalf("简历状态=%q want=%q err=%v", value.ResumeStatus, tc.want, err)
			}
			if _, err := platformcore.ValidateReplyContext(value, platformcore.ReplyTarget{PositionID: "job1"}); err != nil {
				t.Fatalf("平台通知阻断了文字问答：%v", err)
			}
		})
	}
}

// TestReplyConfirmationUsesInboundBoundary 验证同文多轮时仅认可当前问题之后的我方回复。
func TestReplyConfirmationUsesInboundBoundary(t *testing.T) {
	cfg := replyTestConfig()
	runtime := &Runtime{replyConfig: &cfg}
	page := newReplyPage()
	page.active = "a"
	page.messages["a"] = []map[string]string{
		{"direction": "in", "kind": "text", "text": "双休吗？"},
		{"direction": "out", "kind": "text", "text": "是双休。"},
		{"direction": "in", "kind": "text", "text": "双休吗？"},
	}
	target := platformcore.ReplyTarget{PositionID: "job1"}
	conversation := platformcore.ReplyConversation{ID: "a", PositionID: "job1", Name: "同名"}
	value, err := runtime.readCurrentReply(t.Context(), page, target, conversation)
	if err != nil {
		t.Fatal(err)
	}
	value, err = platformcore.ValidateReplyContext(value, target)
	if err != nil {
		t.Fatal(err)
	}
	confirmed, err := runtime.ConfirmReply(t.Context(), page, target, conversation, value.InboundFingerprint, platformcore.ReplyHash("是双休。"))
	if err != nil || confirmed {
		t.Fatalf("历史回答被当作本次发送证据：%t %v", confirmed, err)
	}
	page.messages["a"] = append(page.messages["a"], map[string]string{"direction": "out", "kind": "text", "text": "是双休。"})
	confirmed, err = runtime.ConfirmReply(t.Context(), page, target, conversation, value.InboundFingerprint, platformcore.ReplyHash("是双休。"))
	if err != nil || !confirmed {
		t.Fatalf("本次回答未确认：%t %v", confirmed, err)
	}
}

// resumeOfferExecutor 记录平台发给 Worker 的请求，复用页面执行器的日志和等待能力。
type resumeOfferExecutor struct {
	*replyPage
	path    string
	request platformcore.LocatorRequest
	result  map[string]any
	err     error
}

// Post 捕获真实平台方法生成的定位协议，不模拟选择器匹配行为。
func (e *resumeOfferExecutor) Post(_ context.Context, path string, payload any) (map[string]any, error) {
	request, ok := payload.(platformcore.LocatorRequest)
	if !ok {
		return nil, errors.New("非强类型定位请求")
	}
	e.path, e.request = path, request
	return e.result, e.err
}

// TestPendingResumeOfferUsesConfiguredTarget 防止检测和点击脱离同一配置或把过滤文本放到请求外层。
func TestPendingResumeOfferUsesConfiguredTarget(t *testing.T) {
	var cfg replyPageConfig
	if err := json.Unmarshal([]byte(`{"active":{"selectors":["active-panel"]},"pending_resume_accept":{"selectors":["resume-notice > .op > a.btn"],"text":"同意"}}`), &cfg); err != nil {
		t.Fatal(err)
	}
	want := platformcore.SelectorSpec{
		Selectors: []string{"resume-notice > .op > a.btn"},
		Text:      "同意",
		Parent:    &platformcore.SelectorSpec{Selectors: []string{"active-panel"}},
	}
	for _, action := range []string{"find-elements", "click"} {
		t.Run(action, func(t *testing.T) {
			exec := &resumeOfferExecutor{replyPage: newReplyPage(), result: map[string]any{
				"items": []any{map[string]any{"text": "同意", "fields": map[string]any{}}}, "count": 1, "clicked": true,
			}}
			runtime := &Runtime{replyConfig: &cfg}
			if action == "find-elements" {
				pending, err := runtime.HasPendingResumeOffer(t.Context(), exec)
				if err != nil || !pending {
					t.Errorf("可接受按钮未被识别：pending=%v err=%v", pending, err)
				}
			} else if err := runtime.AcceptPendingResumeOffer(t.Context(), exec); err != nil {
				t.Fatal(err)
			}
			if exec.path != "/api/v1/page/"+action || !reflect.DeepEqual(exec.request.Selector, want) {
				t.Errorf("未使用当前面板内的配置目标：path=%s selector=%+v", exec.path, exec.request.Selector)
			}
			if exec.request.Text != "" {
				t.Errorf("按钮过滤文本不能放在请求外层：%q", exec.request.Text)
			}
		})
	}
}

// TestPendingResumeOfferDetectionFailsSoft 验证按钮命中可识别，零命中、异常响应和检测错误不终止任务。
func TestPendingResumeOfferDetectionFailsSoft(t *testing.T) {
	found := map[string]any{"items": []any{map[string]any{"text": "同意", "fields": map[string]any{}}}, "count": 1}
	for _, tc := range []struct {
		name   string
		result map[string]any
		err    error
		want   bool
	}{
		{name: "按钮存在", result: found, want: true},
		{name: "真实响应信封", result: map[string]any{"ok": true, "data": found}, want: true},
		{name: "无按钮", result: map[string]any{"items": []any{}, "count": 0}},
		{name: "异常响应", result: map[string]any{"items": "invalid"}},
		{name: "空响应"},
		{name: "检测失败", err: errors.New("页面读取失败")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			exec := &resumeOfferExecutor{replyPage: newReplyPage(), result: tc.result, err: tc.err}
			pending, err := NewRuntime().HasPendingResumeOffer(t.Context(), exec)
			if err != nil || pending != tc.want {
				t.Fatalf("待接受状态不符：pending=%v want=%v err=%v", pending, tc.want, err)
			}
		})
	}
}

// TestAcceptPendingResumeOfferPropagatesClickError 验证多目标或不可见等真实点击错误仍向上传递。
func TestAcceptPendingResumeOfferPropagatesClickError(t *testing.T) {
	cause := errors.New("目标不唯一或不可见")
	exec := &resumeOfferExecutor{replyPage: newReplyPage(), err: cause}
	if err := NewRuntime().AcceptPendingResumeOffer(t.Context(), exec); !errors.Is(err, cause) {
		t.Fatalf("点击错误被吞掉或丢失原因：%v", err)
	}
}
