// Package boss 本文件验证岗位收尾回复检查流程：切消息页、查会话、判回复、求简历的完整序列。
package boss

import (
	"context"
	"testing"

	"goodhr5/local-agent-go/internal/cloudapi"
	"goodhr5/local-agent-go/internal/platformcore"
)

// replyExecutor 模拟 Boss 消息页回复检查相关 Worker 接口，按候选人返回预设会话状态。
type replyExecutor struct {
	calls          []conversationCall
	onChatPage     bool
	messages       map[string]string
	lastFoundName  string
	resumeClicked  bool
	confirmVisible bool
	chatVisible    bool
	chatName       string
	scrollSame     bool
	delays         []float64
}

// Post 记录调用并按路径返回模拟结果。
func (e *replyExecutor) Post(_ context.Context, path string, payload any) (map[string]any, error) {
	data, _ := payload.(map[string]any)
	e.calls = append(e.calls, conversationCall{path: path, payload: data})
	switch path {
	case "/api/v1/page/list":
		url := "https://www.zhipin.com/web/chat/recommend"
		if e.onChatPage {
			url = "https://www.zhipin.com/web/chat/index"
		}
		return map[string]any{"data": map[string]any{"pages": []any{
			map[string]any{"url": url, "is_default": true},
		}}}, nil
	case "/api/v1/page/click-by-text":
		e.onChatPage = true
		return map[string]any{"data": map[string]any{}}, nil
	case "/api/v1/boss/chat/find-session":
		name := stringFromMap(data, "candidate_name")
		text, ok := e.messages[name]
		if !ok {
			return map[string]any{"data": map[string]any{"found": false, "text": ""}}, nil
		}
		e.lastFoundName = name
		return map[string]any{"data": map[string]any{"found": true, "text": text, "element_ref": "session-ref"}}, nil
	case "/api/v1/boss/chat/scroll-list":
		before := "22:08 张三"
		after := before
		if !e.scrollSame {
			after = "21:00 李四"
		}
		return map[string]any{"data": map[string]any{"scrolled": true, "before_text": before, "after_text": after}}, nil
	case "/api/v1/page/click":
		// 点击会话项后右侧面板打开该候选人会话。
		e.chatVisible = true
		e.chatName = e.lastFoundName
		return map[string]any{"data": map[string]any{"clicked": true}}, nil
	case "/api/v1/page/find-elements":
		selector := stringFromMap(mapFromAny(data["element"]), "selector")
		if selector == bossReplyFlow.ChatModal {
			if !e.chatVisible {
				return emptyFindItems(), nil
			}
			return chatItems(e.chatName), nil
		}
		return emptyFindItems(), nil
	case "/api/v1/boss/chat/request-resume":
		return map[string]any{"data": map[string]any{"clicked": e.resumeClicked, "confirm_visible": e.confirmVisible}}, nil
	}
	return map[string]any{"data": map[string]any{}}, nil
}

// Log 模拟日志写入。
func (e *replyExecutor) Log(string, string) {}

// Delay 记录等待时长但不真正睡眠，保证测试快速完成。
func (e *replyExecutor) Delay(_ context.Context, _ string, seconds float64) error {
	e.delays = append(e.delays, seconds)
	return nil
}

// hasCalledPath 判断是否调用过指定路径。
func hasCalledPath(exec *replyExecutor, path string) bool {
	for _, call := range exec.calls {
		if call.path == path {
			return true
		}
	}
	return false
}

// TestCheckResumeRequestsRequestsRepliedCandidate 验证已回复候选人走完整求简历序列。
func TestCheckResumeRequestsRequestsRepliedCandidate(t *testing.T) {
	exec := &replyExecutor{
		onChatPage:     false,
		messages:       map[string]string{"赵永豪": "1 22:08 赵永豪 Java开发工程师 您好！5年Java+Python双栈..."},
		resumeClicked:  true,
		confirmVisible: true,
	}
	runtime := NewRuntime()
	outcomes, err := runtime.CheckResumeRequests(context.Background(), exec, cloudapi.PlatformConfig{"id": "boss"}, []string{"赵永豪"})
	if err != nil {
		t.Fatal(err)
	}
	if outcome := outcomes["赵永豪"]; outcome.Status != "requested" {
		t.Fatalf("outcome = %+v，应已完成索要", outcome)
	}
	// 先切消息页再查会话：点击沟通菜单 → 查会话 → 点会话项 → 求简历。
	wantSequence := []string{
		"/api/v1/page/list",
		"/api/v1/page/click-by-text",
		"/api/v1/boss/chat/find-session",
		"/api/v1/page/click",
		"/api/v1/boss/chat/request-resume",
	}
	paths := make([]string, 0, len(exec.calls))
	for _, call := range exec.calls {
		paths = append(paths, call.path)
	}
	index := 0
	for _, want := range wantSequence {
		found := false
		for ; index < len(paths); index++ {
			if paths[index] == want {
				found = true
				index++
				break
			}
		}
		if !found {
			t.Fatalf("调用序列缺少 %s，实际=%v", want, paths)
		}
	}
}

// TestCheckResumeRequestsKeepsPendingForUnreplied 验证未回复候选人保持待检查且不执行任何页面动作。
func TestCheckResumeRequestsKeepsPendingForUnreplied(t *testing.T) {
	exec := &replyExecutor{
		onChatPage: true,
		messages:   map[string]string{"张三": "[送达] 22:00 张三 Java开发工程师 您好，很高兴认识您..."},
	}
	runtime := NewRuntime()
	outcomes, err := runtime.CheckResumeRequests(context.Background(), exec, cloudapi.PlatformConfig{"id": "boss"}, []string{"张三"})
	if err != nil {
		t.Fatal(err)
	}
	if outcome := outcomes["张三"]; outcome.Status != "pending" {
		t.Fatalf("outcome = %+v，未回复应保持待检查", outcome)
	}
	if hasCalledPath(exec, "/api/v1/page/click") || hasCalledPath(exec, "/api/v1/boss/chat/request-resume") {
		t.Fatal("未回复候选人不应打开会话或求简历")
	}
	// 已在消息页时不应再点沟通菜单。
	if hasCalledPath(exec, "/api/v1/page/click-by-text") {
		t.Fatal("已在消息页时不应再点击沟通菜单")
	}
}

// TestCheckResumeRequestsMarksNotFoundAfterScroll 验证滚动到底仍找不到会话时标记未找到。
func TestCheckResumeRequestsMarksNotFoundAfterScroll(t *testing.T) {
	exec := &replyExecutor{onChatPage: true, scrollSame: true}
	runtime := NewRuntime()
	outcomes, err := runtime.CheckResumeRequests(context.Background(), exec, cloudapi.PlatformConfig{"id": "boss"}, []string{"王五"})
	if err != nil {
		t.Fatal(err)
	}
	if outcome := outcomes["王五"]; outcome.Status != "not_found" {
		t.Fatalf("outcome = %+v，滚动到底仍找不到应标记未找到", outcome)
	}
	if hasCalledPath(exec, "/api/v1/boss/chat/request-resume") {
		t.Fatal("未找到会话时不应执行求简历")
	}
	// 列表到底后应停止滚动重试。
	scrollCalls := 0
	for _, call := range exec.calls {
		if call.path == "/api/v1/boss/chat/scroll-list" {
			scrollCalls++
		}
	}
	if scrollCalls != 1 {
		t.Fatalf("scroll calls = %d，列表到底后不应继续滚动", scrollCalls)
	}
}

// TestCheckResumeRequestsFailsWhenResumeButtonMissing 验证求简历按钮点不到时返回失败原因。
func TestCheckResumeRequestsFailsWhenResumeButtonMissing(t *testing.T) {
	exec := &replyExecutor{
		onChatPage:    true,
		messages:      map[string]string{"李四": "21:58 李四 前端工程师 你好，我对这个岗位很感兴趣"},
		resumeClicked: false,
	}
	runtime := NewRuntime()
	outcomes, err := runtime.CheckResumeRequests(context.Background(), exec, cloudapi.PlatformConfig{"id": "boss"}, []string{"李四"})
	if err != nil {
		t.Fatal(err)
	}
	outcome := outcomes["李四"]
	if outcome.Status != "failed" || outcome.Reason == "" {
		t.Fatalf("outcome = %+v，按钮缺失应返回失败原因", outcome)
	}
}

// TestCheckResumeRequestsSkipsEmptyNames 验证空名单不触发任何页面动作。
func TestCheckResumeRequestsSkipsEmptyNames(t *testing.T) {
	exec := &replyExecutor{}
	runtime := NewRuntime()
	outcomes, err := runtime.CheckResumeRequests(context.Background(), exec, cloudapi.PlatformConfig{"id": "boss"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(outcomes) != 0 || len(exec.calls) != 0 {
		t.Fatalf("outcomes=%v calls=%d，空名单不应有调用", outcomes, len(exec.calls))
	}
}

// TestIsUnrepliedSessionText 验证会话项文本的回复状态判断规则。
func TestIsUnrepliedSessionText(t *testing.T) {
	tests := []struct {
		text     string
		unrepled bool
	}{
		{"[送达] 22:00 张三 Java 您好...", true},
		{"[已读] 21:30 李四 测试工程师 您好...", true},
		{"1 22:08 赵永豪 Java开发工程师 您好！5年Java...", false},
		{"22:18 戴海天 测试开发 张三的简历.pdf", false},
	}
	for _, test := range tests {
		if got := isUnrepliedSessionText(test.text); got != test.unrepled {
			t.Fatalf("isUnrepliedSessionText(%q) = %v，期望 %v", test.text, got, test.unrepled)
		}
	}
}

// 编译期断言：Boss Runtime 必须实现平台回复检查能力接口。
var _ platformcore.ResumeRequestChecker = NewRuntime()
