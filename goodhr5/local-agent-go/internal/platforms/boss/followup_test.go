// Package boss 本文件验证 Boss 打招呼后索要信息流程的复用、打开、索要和收尾行为。
package boss

import (
	"context"
	"strings"
	"testing"

	"goodhr5/local-agent-go/internal/cloudapi"
	"goodhr5/local-agent-go/internal/platformcore"
)

// conversationCall 记录一次 Worker 调用的路径和参数。
type conversationCall struct {
	path    string
	payload map[string]any
}

// conversationExecutor 模拟 Boss 聊天框相关 Worker 接口，按选择器返回预设状态。
type conversationExecutor struct {
	calls          []conversationCall
	chatVisible    bool
	chatName       string
	confirmVisible bool
	chatNameSticky bool
	delays         []float64
}

// Post 记录调用并按路径与选择器返回模拟结果。
func (e *conversationExecutor) Post(_ context.Context, path string, payload any) (map[string]any, error) {
	data, _ := payload.(map[string]any)
	e.calls = append(e.calls, conversationCall{path: path, payload: data})
	switch path {
	case "/api/v1/page/find-elements":
		selector := stringFromMap(mapFromAny(data["element"]), "selector")
		switch selector {
		case bossConversationFlow.ChatModal:
			if !e.chatVisible {
				return emptyFindItems(), nil
			}
			return chatItems(e.chatName), nil
		case ".boss-btn-primary":
			if !e.confirmVisible {
				return emptyFindItems(), nil
			}
			return confirmItems(), nil
		}
		return emptyFindItems(), nil
	case "/api/v1/page/click":
		selector := stringFromMap(mapFromAny(data["element"]), "selector")
		if selector == bossConversationFlow.ChatClose {
			e.chatVisible = false
		}
	case "/api/v1/boss/candidates/greet":
		e.chatVisible = true
		if name := stringFromMap(data, "diagnostic_candidate_name"); name != "" && !e.chatNameSticky {
			e.chatName = name
		}
	}
	return map[string]any{"data": map[string]any{}}, nil
}

// Log 模拟日志写入。
func (e *conversationExecutor) Log(string, string) {}

// Delay 记录等待时长但不真正睡眠，保证测试快速完成。
func (e *conversationExecutor) Delay(_ context.Context, _ string, seconds float64) error {
	e.delays = append(e.delays, seconds)
	return nil
}

// emptyFindItems 返回空元素列表响应。
func emptyFindItems() map[string]any {
	return map[string]any{"data": map[string]any{"items": []any{}}}
}

// chatItems 返回带候选人姓名字段的聊天框元素响应。
func chatItems(name string) map[string]any {
	return map[string]any{"data": map[string]any{"items": []any{
		map[string]any{"element_ref": "chat-ref", "fields": map[string]any{"chat_name": name}},
	}}}
}

// confirmItems 返回索要确认浮层主按钮元素响应。
func confirmItems() map[string]any {
	return map[string]any{"data": map[string]any{"items": []any{
		map[string]any{"element_ref": "confirm-ref"},
	}}}
}

// callPaths 汇总测试中实际调用过的 Worker 路径。
func callPaths(exec *conversationExecutor) []string {
	paths := make([]string, 0, len(exec.calls))
	for _, call := range exec.calls {
		paths = append(paths, call.path)
	}
	return paths
}

// hasClickSelector 判断是否点击过指定选择器。
func hasClickSelector(exec *conversationExecutor, selector string) bool {
	for _, call := range exec.calls {
		if call.path != "/api/v1/page/click" {
			continue
		}
		if stringFromMap(mapFromAny(call.payload["element"]), "selector") == selector {
			return true
		}
	}
	return false
}

// TestRequestCandidateInfoReusesAutoOpenedChat 验证打招呼已打开聊天框时直接复用并完成索要和发消息。
func TestRequestCandidateInfoReusesAutoOpenedChat(t *testing.T) {
	exec := &conversationExecutor{chatVisible: true, chatName: "张三", confirmVisible: true}
	runtime := NewRuntime()
	err := runtime.RequestCandidateInfo(context.Background(), exec, cloudapi.PlatformConfig{"id": "boss"}, platformcore.Candidate{"candidate_name": "张三", "card_index": 0}, platformcore.CandidateInfoRequest{
		RequestPhone: true, RequestWechat: true, RequestResume: true, GreetMessage: "方便留个联系方式吗",
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range callPaths(exec) {
		if strings.Contains(path, "/api/v1/boss/candidates/") {
			t.Fatalf("复用聊天框时不应再调用候选人定位接口，实际调用=%v", callPaths(exec))
		}
	}
	if !hasClickSelector(exec, bossConversationRequestButtons.Phone.Selector) {
		t.Fatal("没有点击索要手机号按钮")
	}
	if !hasClickSelector(exec, bossConversationRequestButtons.Resume.Selector) {
		t.Fatal("没有点击索要简历按钮")
	}
	if !hasClickSelector(exec, ".btn-send:not(.btn-disabled)") {
		t.Fatal("没有点击发送按钮")
	}
	if !hasClickSelector(exec, bossConversationFlow.ChatClose) {
		t.Fatal("收尾时没有关闭聊天框")
	}
}

// TestRequestCandidateInfoOpensChatWhenClosed 验证聊天框未打开时先定位候选人并打开聊天框再索要。
func TestRequestCandidateInfoOpensChatWhenClosed(t *testing.T) {
	exec := &conversationExecutor{chatVisible: false, confirmVisible: true}
	runtime := NewRuntime()
	err := runtime.RequestCandidateInfo(context.Background(), exec, cloudapi.PlatformConfig{"id": "boss"}, platformcore.Candidate{"candidate_name": "张三", "card_index": 2}, platformcore.CandidateInfoRequest{
		RequestPhone: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	visibleCalled := false
	for _, call := range exec.calls {
		switch call.path {
		case "/api/v1/boss/candidates/visible":
			visibleCalled = true
			if intFromMap(call.payload, "card_index") != 2 {
				t.Fatal("候选人定位没有使用当前卡片的序号")
			}
		case "/api/v1/boss/candidates/greet":
			if !visibleCalled {
				t.Fatal("应先定位候选人再打开聊天框")
			}
		}
	}
	if !visibleCalled {
		t.Fatal("聊天框未打开时没有调用候选人定位接口")
	}
	if !hasClickSelector(exec, bossConversationRequestButtons.Phone.Selector) {
		t.Fatal("打开聊天框后没有执行索要")
	}
}

// TestRequestCandidateInfoFailsWhenChatNameMismatch 验证重新打开后聊天框仍属于其他候选人时报错并且不执行索要。
func TestRequestCandidateInfoFailsWhenChatNameMismatch(t *testing.T) {
	exec := &conversationExecutor{chatVisible: true, chatName: "李四", confirmVisible: true, chatNameSticky: true}
	runtime := NewRuntime()
	err := runtime.RequestCandidateInfo(context.Background(), exec, cloudapi.PlatformConfig{"id": "boss"}, platformcore.Candidate{"candidate_name": "张三", "card_index": 0}, platformcore.CandidateInfoRequest{
		RequestPhone: true,
	})
	if err == nil || !strings.Contains(err.Error(), "张三") {
		t.Fatalf("err = %v，应提示未能确认当前候选人", err)
	}
	if hasClickSelector(exec, bossConversationRequestButtons.Phone.Selector) {
		t.Fatal("聊天对象未确认时不应执行索要")
	}
}

// TestRequestCandidateInfoSkipsEmptyRequest 验证岗位未配置索要和问候语时不执行任何页面动作。
func TestRequestCandidateInfoSkipsEmptyRequest(t *testing.T) {
	exec := &conversationExecutor{}
	runtime := NewRuntime()
	if err := runtime.RequestCandidateInfo(context.Background(), exec, nil, nil, platformcore.CandidateInfoRequest{}); err != nil {
		t.Fatal(err)
	}
	if len(exec.calls) != 0 {
		t.Fatalf("calls = %d，空请求不应有调用", len(exec.calls))
	}
}
