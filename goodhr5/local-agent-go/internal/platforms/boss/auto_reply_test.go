// 本文件用模拟页面事实验证 Boss 会话隔离与发送保护；模拟选择器不用于真实配置。
package boss

import (
	"context"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"goodhr5/local-agent-go/internal/platformcore"
)

// TestReplyResumeAllScreenshotParts 验证回复评分读取拼接后的完整图片，缺失分段必须失败而非降级第一屏。
func TestReplyResumeAllScreenshotParts(t *testing.T) {
	directory := t.TempDir()
	parts := []any{}
	for index, shade := range []color.RGBA{{R: 255, A: 255}, {B: 255, A: 255}} {
		name := filepath.Join(directory, string(rune('a'+index))+".png")
		img := image.NewRGBA(image.Rect(0, 0, 80, 120))
		for y := 0; y < 120; y++ {
			for x := 0; x < 80; x++ {
				img.SetRGBA(x, y, shade)
			}
		}
		file, err := os.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if err = png.Encode(file, img); err != nil {
			t.Fatal(err)
		}
		_ = file.Close()
		parts = append(parts, map[string]any{"file_path": name})
	}
	path, err := replyResumeScreenshotPath(newReplyPage(), directory, "虚构候选人", map[string]any{"screenshot_parts": parts})
	if err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	img, err := png.Decode(file)
	if err != nil || img.Bounds().Dy() <= 120 {
		t.Fatalf("只保留第一屏 %v", err)
	}
	if _, err = replyResumeScreenshotPath(newReplyPage(), directory, "虚构候选人", map[string]any{"screenshot_parts": []any{map[string]any{"file_path": "missing-a"}, map[string]any{"file_path": "missing-b"}}}); err == nil {
		t.Fatal("缺失分段仍允许评分")
	}
}

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
	return replyPageConfig{Jobs: spec("jobs"), JobFields: map[string]platformcore.SelectorField{"id": {Attribute: "id"}, "name": {Attribute: "name"}}, Verified: true, MessagesURL: "https://fixture.invalid/chat", Unread: spec("unread"), UnreadFilter: spec("unread_filter"), Conversation: spec("conversation"), Selected: spec("selected"), Active: spec("active"), Messages: spec("messages"), Input: spec("input"), Send: spec("send"), IdentityAttribute: "id", ConversationFields: fields, MessageFields: messageFields, Directions: map[string]string{"in": "inbound", "out": "outbound"}, Kinds: map[string]string{"text": "text", "file": "attachment"}}
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
	onDelay          func()
	contactOpen      string
	contactClicks    int
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
	if path == "/api/v1/page/list" {
		return map[string]any{"pages": []any{}}, nil
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
		case "phone-button", "wechat-button":
			return pageItems(map[string]string{}), nil
		case "all-popups", ".exchange-tooltip:visible":
			if p.contactOpen != "" {
				return pageItems(map[string]string{}), nil
			}
			return pageItems(), nil
		case "phone-confirm", "phone-cancel", "wechat-confirm", "wechat-cancel":
			if strings.HasPrefix(key, p.contactOpen+"-") && p.contactOpen != "" {
				return pageItems(map[string]string{}), nil
			}
			return pageItems(), nil
		case "jobs":
			return pageItems(p.jobs...), nil
		case "unread":
			fields := []map[string]string{}
			for _, id := range p.unread {
				fields = append(fields, identity(id))
			}
			return pageItems(fields...), nil
		case "selected":
			if p.active == "" {
				return pageItems(), nil
			}
			return pageItems(identity(p.active)), nil
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
		if key == "phone-button" || key == "wechat-button" {
			p.contactOpen = strings.TrimSuffix(key, "-button")
			return map[string]any{"clicked": true}, nil
		}
		if key == "phone-confirm" || key == "wechat-confirm" {
			p.contactClicks++
			p.contactOpen = ""
			p.messages[p.active] = append(p.messages[p.active], map[string]string{"direction": "out", "kind": "text", "text": "我想发起交换手机请求"})
			return map[string]any{"clicked": true}, nil
		}
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

// TestContactConfirmRejectsChangedCandidate 验证出现正确确认框后切换候选人，也不能点击确定。
func TestContactConfirmRejectsChangedCandidate(t *testing.T) {
	cfg := replyTestConfig()
	settings := contactRequestConfig{Button: platformcore.SelectorSpec{Selectors: []string{"phone-button"}}, Confirm: platformcore.SelectorSpec{Selectors: []string{"phone-confirm"}}, Cancel: platformcore.SelectorSpec{Selectors: []string{"phone-cancel"}}}
	cfg.ContactRequests = map[string]contactRequestConfig{"phone": settings}
	runtime := &Runtime{replyConfig: &cfg}
	page := newReplyPage()
	page.active = "a"
	conversation := platformcore.ReplyConversation{ID: "a", Name: "同名"}
	target := platformcore.ReplyTarget{PositionName: "Go"}
	// 测试已有模拟器将通用弹窗选择器视为没有弹窗，入口后打开电话确认。
	prepared, err := runtime.PrepareCandidateInfoRequest(t.Context(), page, target, conversation, "phone")
	if err != nil {
		t.Fatal(err)
	}
	page.active = "b"
	page.messages["b"] = append(page.messages["b"], map[string]string{"direction": "in", "kind": "text", "text": "另一个人的新消息"})
	if state, err := runtime.SubmitCandidateInfoRequest(t.Context(), page, target, conversation, prepared); err == nil || state != "unknown" || page.contactClicks != 0 {
		t.Fatalf("切换后仍确认：%s %v clicks=%d", state, err, page.contactClicks)
	}
}

// Log 模拟不含聊天原文的日志出口。
func (*replyPage) Log(string, string) {}

// Delay 测试中不引入轮询等待。
func (p *replyPage) Delay(ctx context.Context, _ string, _ float64) error {
	if p.onDelay != nil {
		p.onDelay()
	}
	return ctx.Err()
}

// TestReGreetRechecksMessages 验证输入和发送之间出现新消息时不会点击发送。
func TestReGreetRechecksMessages(t *testing.T) {
	cfg := replyTestConfig()
	runtime := &Runtime{replyConfig: &cfg}
	page := newReplyPage()
	page.active = "a"
	page.messages["a"] = []map[string]string{{"id": "first", "direction": "out", "kind": "text", "text": "首次招呼"}}
	conversation := platformcore.ReplyConversation{ID: "a", Name: "同名"}
	target := platformcore.ReplyTarget{PositionName: "Go"}
	before, err := runtime.ReadOpenedReplyContext(t.Context(), page, target, conversation)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.StageReGreet(t.Context(), page, target, conversation, before, "复打消息"); err != nil {
		t.Fatal(err)
	}
	page.messages["a"] = append(page.messages["a"], map[string]string{"id": "new", "direction": "in", "kind": "text", "text": "刚刚回复"})
	if err := runtime.SendReGreet(t.Context(), page, target, conversation, before, "复打消息"); err == nil || len(page.sent) != 0 {
		t.Fatalf("上下文已变化仍发送：%v", err)
	}
}

// TestReGreetWaitsForConfirmation 验证发送结果延迟出现时只等待证据，不重发；历史同文不算成功。
func TestReGreetWaitsForConfirmation(t *testing.T) {
	cfg := replyTestConfig()
	runtime := &Runtime{replyConfig: &cfg}
	page := newReplyPage()
	page.active = "a"
	page.messages["a"] = []map[string]string{{"id": "old", "direction": "out", "kind": "text", "text": "复打消息"}}
	conversation := platformcore.ReplyConversation{ID: "a", Name: "同名"}
	target := platformcore.ReplyTarget{PositionName: "Go"}
	before, err := runtime.ReadOpenedReplyContext(t.Context(), page, target, conversation)
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := runtime.ConfirmReGreet(t.Context(), page, target, conversation, before, "复打消息"); ok || err != nil {
		t.Fatalf("历史同文被误确认：%v %v", ok, err)
	}
	page.onDelay = func() {
		page.messages["a"] = append(page.messages["a"], map[string]string{"id": "new", "direction": "out", "kind": "text", "text": "复打消息"})
		page.onDelay = nil
	}
	if ok, err := runtime.ConfirmReGreet(t.Context(), page, target, conversation, before, "复打消息"); !ok || err != nil {
		t.Fatalf("延迟发送未确认：%v %v", ok, err)
	}
	if len(page.sent) != 0 {
		t.Fatal("确认过程中重复点击发送")
	}
}

// TestAutoReplyUnverifiedBlocked 验证未取得 Boss 页面证据时不执行任何浏览器动作。
func TestAutoReplyUnverifiedBlocked(t *testing.T) {
	runtime := &Runtime{replyConfig: &replyPageConfig{Verified: false}}
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

// TestConfirmReplyIgnoresStatusLabels 验证出站文本带“送达/已读”等状态标签时不影响发送确认。
func TestConfirmReplyIgnoresStatusLabels(t *testing.T) {
	cfg := replyTestConfig()
	runtime := &Runtime{replyConfig: &cfg}
	page := newReplyPage()
	page.active = "a"
	page.messages["a"] = []map[string]string{
		{"id": "a1", "direction": "in", "kind": "text", "text": "你好"},
		{"id": "a2", "direction": "out", "kind": "text", "text": "送达\n请发送一份最新简历。\n已读"},
	}
	target := platformcore.ReplyTarget{PositionID: "job1"}
	conversation := platformcore.ReplyConversation{ID: "a", PositionID: "job1", Name: "同名"}
	inbound := platformcore.ReplyInboundFingerprint("a", []platformcore.ReplyMessage{{ID: "a1", Direction: "inbound", Kind: "text", Text: "你好"}})
	confirmed, err := runtime.ConfirmReply(t.Context(), page, target, conversation, inbound, platformcore.ReplyHash("请发送一份最新简历。"))
	if err != nil || !confirmed {
		t.Fatalf("状态标签导致发送确认失败：confirmed=%t err=%v", confirmed, err)
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
			conversation := platformcore.ReplyConversation{ID: "a", PositionID: "job1", Name: "同名"}
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
	conversation := platformcore.ReplyConversation{ID: "a", PositionID: "job1", Name: "同名"}
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

// resumeRequestExecutor 只模拟页面与 Worker 边界；真实平台流程负责等待、核对和请求结果判断。
type resumeRequestExecutor struct {
	*replyPage
	mode                           string
	opened, confirmed              bool
	checks, resultChecks, requests int
	actions                        []string
	logs                           []string
	cancel                         context.CancelFunc
}

// Post 模拟确认框延迟显示以及确认后请求状态延迟更新，不把点击当作发送成功。
func (e *resumeRequestExecutor) Post(ctx context.Context, path string, payload any) (map[string]any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	req, ok := payload.(platformcore.LocatorRequest)
	if !ok || len(req.Selector.Selectors) == 0 {
		return nil, errors.New("缺少强类型定位请求")
	}
	key := req.Selector.Selectors[0]
	if path == "/api/v1/page/find-elements" {
		if key == "resume-confirm" {
			e.checks++
			if e.opened && e.mode != "missing-dialog" && e.mode != "cancel-wait" && (e.mode != "delayed" || e.checks >= 4) {
				return pageItems(map[string]string{}), nil
			}
			return pageItems(), nil
		}
		if key == "active" {
			if e.opened && e.mode == "switched" {
				return pageItems(map[string]string{"name": "其他候选人"}), nil
			}
			if e.confirmed {
				e.resultChecks++
				if e.mode != "missing-result" && (e.mode != "delayed" || e.resultChecks >= 4) {
					e.resumeRequested = true
				}
			}
		}
	}
	if path == "/api/v1/page/click" {
		switch key {
		case "resume-button":
			e.actions = append(e.actions, "open")
			if req.Selector.Parent == nil || req.Selector.Text != "求简历" {
				return nil, errors.New("求简历定位范围或文本不正确")
			}
			if e.mode == "entry-not-clicked" {
				return map[string]any{"clicked": false}, nil
			}
			e.opened = true
			return map[string]any{"clicked": true}, nil
		case "resume-confirm":
			e.actions = append(e.actions, "confirm")
			if !e.opened || e.mode == "missing-dialog" {
				return nil, errors.New("确认框未出现")
			}
			if e.mode == "confirm-error" {
				return nil, errors.New("确定点击失败")
			}
			if e.mode == "confirm-not-clicked" {
				return map[string]any{"clicked": false}, nil
			}
			e.confirmed, e.opened = true, false
			e.requests++
			return map[string]any{"ok": true, "data": map[string]any{"clicked": true}}, nil
		case ".boss-btn-primary":
			return nil, errors.New("确定按钮未限定索取简历弹窗")
		}
	}
	return e.replyPage.Post(ctx, path, payload)
}

// Delay 推进测试中的等待边界，必要时模拟用户停止，不进行真实睡眠。
func (e *resumeRequestExecutor) Delay(ctx context.Context, _ string, _ float64) error {
	if e.mode == "cancel-wait" && e.cancel != nil {
		e.cancel()
	}
	return ctx.Err()
}

// Log 保留用户可见结果，验证失败不能记录为请求成功。
func (e *resumeRequestExecutor) Log(_ string, message string) { e.logs = append(e.logs, message) }

// TestResumeRequestConfirmation 验证完整的求简历、确认和结果核实，异常时不伪报成功。
func TestResumeRequestConfirmation(t *testing.T) {
	for _, mode := range []string{"success", "delayed", "missing-dialog", "confirm-error", "missing-result", "read-error", "switched", "cancel-wait", "entry-not-clicked", "confirm-not-clicked", "already-requested", "already-received"} {
		t.Run(mode, func(t *testing.T) {
			cfg := replyTestConfig()
			if err := json.Unmarshal([]byte(`{"resume_received":{"selectors":["resume-received"]},"resume_requested_text":"简历请求已发送","resume_button":{"selectors":["resume-button"],"text":"求简历"},"resume_confirm":{"selectors":["resume-confirm"],"text":"确定"}}`), &cfg); err != nil {
				t.Fatal(err)
			}
			runtime := &Runtime{replyConfig: &cfg}
			page := newReplyPage()
			page.active = "a"
			page.resumeError = mode == "read-error"
			page.resumeRequested = mode == "already-requested"
			page.resumeReceived = mode == "already-received"
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			exec := &resumeRequestExecutor{replyPage: page, mode: mode, cancel: cancel}
			action, err := runtime.ResumeAfterReply(ctx, exec, platformcore.ReplyConversation{ID: "a", Name: "同名", PositionID: "job1"}, nil, 95, 70)
			success := mode == "success" || mode == "delayed"
			if success {
				if err != nil || action != "requested" || !page.resumeRequested || exec.requests != 1 || !reflect.DeepEqual(exec.actions, []string{"open", "confirm"}) {
					t.Fatalf("未完成两步索要和结果确认：action=%s err=%v requested=%t requests=%d actions=%v", action, err, page.resumeRequested, exec.requests, exec.actions)
				}
				return
			}
			if mode == "already-requested" || mode == "already-received" {
				if action != "skipped" || err != nil || len(exec.actions) != 0 {
					t.Fatalf("重复索要：%s %v %v", action, err, exec.actions)
				}
				return
			}
			if err == nil || action == "requested" {
				t.Fatalf("异常被记作成功：%s %v actions=%v", action, err, exec.actions)
			}
			if mode != "missing-result" && exec.requests != 0 {
				t.Fatalf("异常后仍发送了请求：%d", exec.requests)
			}
			if mode == "read-error" && len(exec.actions) != 0 {
				t.Fatalf("状态未知仍点击：%v", exec.actions)
			}
			for _, message := range exec.logs {
				if strings.Contains(message, "索要请求已发出") {
					t.Fatalf("失败误报成功：%s", message)
				}
			}
		})
	}
}

// attachmentExecutor 模拟外部浮层和文件保存响应，使用真实平台方法验证动作顺序与作用域。
type attachmentExecutor struct {
	*replyPage
	opened      bool
	actions     []string
	downloadErr error
	closeErr    error
}

// Post 在通用协议边界检查附件入口、页面级下载按钮和关闭按钮。
func (e *attachmentExecutor) Post(ctx context.Context, path string, payload any) (map[string]any, error) {
	req, ok := payload.(platformcore.LocatorRequest)
	if !ok {
		return nil, errors.New("非强类型请求")
	}
	key := req.Selector.Selectors[0]
	if path == "/api/v1/page/find-elements" && (key == "attachment-toolbar" || key == "attachment-close") {
		if e.opened {
			return pageItems(map[string]string{}), nil
		}
		return pageItems(), nil
	}
	if path == "/api/v1/page/click" {
		switch key {
		case "resume-received":
			if req.Selector.Parent == nil {
				return nil, errors.New("附件入口缺少当前面板范围")
			}
			e.actions = append(e.actions, "open")
			e.opened = true
			return map[string]any{"clicked": true}, nil
		case "attachment-download":
			if req.Selector.Parent != nil || !e.opened || req.Download == nil {
				return nil, errors.New("下载不在全屏浮层或缺少记录关联")
			}
			e.actions = append(e.actions, "download")
			if e.downloadErr != nil {
				return nil, e.downloadErr
			}
			return map[string]any{"ok": true, "data": map[string]any{"clicked": true, "download": map[string]any{"id": req.Download.ID, "source_key": req.Download.SourceKey, "status": "saved", "file_path": "saved.pdf"}}}, nil
		case "attachment-close":
			if req.Selector.Parent != nil || ctx.Err() != nil {
				return nil, errors.New("关闭作用域或清理上下文错误")
			}
			e.actions = append(e.actions, "close")
			if e.closeErr != nil {
				return nil, e.closeErr
			}
			e.opened = false
			return map[string]any{"clicked": true}, nil
		}
	}
	return e.replyPage.Post(ctx, path, payload)
}

// TestAttachmentDownloadFlow 验证真实平台代码在成功和失败时均关闭全屏浮层，保留已保存结果。
func TestAttachmentDownloadFlow(t *testing.T) {
	for _, mode := range []string{"saved", "download-error", "close-error"} {
		t.Run(mode, func(t *testing.T) {
			cfg := replyTestConfig()
			if err := json.Unmarshal([]byte(`{"resume_received":{"selectors":["resume-received"]},"attachment_toolbar":{"selectors":["attachment-toolbar"]},"attachment_download":{"selectors":["attachment-download"]},"attachment_close":{"selectors":["attachment-close"]}}`), &cfg); err != nil {
				t.Fatal(err)
			}
			runtime := &Runtime{replyConfig: &cfg}
			downloader, ok := any(runtime).(platformcore.ResumeAttachmentDownloader)
			if !ok {
				t.Fatal("缺少附件两步下载能力")
			}
			page := newReplyPage()
			page.active = "a"
			page.resumeReceived = true
			exec := &attachmentExecutor{replyPage: page}
			if mode == "download-error" {
				exec.downloadErr = errors.New("下载响应超时")
			}
			if mode == "close-error" {
				exec.closeErr = errors.New("关闭失败")
			}
			record, err := downloader.DownloadResumeAttachment(t.Context(), exec, platformcore.ReplyConversation{ID: "a", PositionID: "job1", Name: "同名"}, platformcore.DownloadRequest{ID: "d1", SourceKey: "s1", PositionID: "p1", TimeoutMS: 1000})
			if !reflect.DeepEqual(exec.actions, []string{"open", "download", "close"}) {
				t.Fatalf("动作顺序错误：%v err=%v", exec.actions, err)
			}
			if mode == "saved" && (err != nil || record["status"] != "saved") {
				t.Fatalf("下载未完成：%v %v", record, err)
			}
			if mode != "saved" && err == nil {
				t.Fatal("错误被吞掉")
			}
			if mode == "download-error" && record["status"] == "saved" {
				t.Fatal("点击失败误记成功")
			}
			if mode == "close-error" && record["status"] != "saved" {
				t.Fatal("关闭失败丢失已保存的下载结果")
			}
		})
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
