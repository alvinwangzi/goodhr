// Package liepin 本文件验证猎聘企业端打招呼后索要信息流程的打开、索要和发送行为。
package liepin

import (
	"context"
	"strings"
	"testing"

	"goodhr5/local-agent-go/internal/cloudapi"
	"goodhr5/local-agent-go/internal/platformcore"
)

// liepinConversationCall 记录一次 Worker 调用的路径和参数。
type liepinConversationCall struct {
	path    string
	payload map[string]any
}

// liepinConversationExecutor 模拟猎聘企业端聊天框相关 Worker 接口，按选择器返回预设状态。
type liepinConversationExecutor struct {
	calls          []liepinConversationCall
	chatVisible    bool
	chatName       string
	confirmVisible bool
	openChatName   string
}

// Post 记录调用并按路径与选择器返回模拟结果。
func (e *liepinConversationExecutor) Post(_ context.Context, path string, payload any) (map[string]any, error) {
	data, _ := payload.(map[string]any)
	e.calls = append(e.calls, liepinConversationCall{path: path, payload: data})
	switch path {
	case "/api/v1/page/find-elements":
		selector := stringFromMap(mapFromAny(data["element"]), "selector")
		switch selector {
		case liepinConversationFlow.ChatModal:
			if !e.chatVisible {
				return emptyFindItems(), nil
			}
			return chatItems(e.chatName), nil
		case ".ant-im-modal.ant-im-modal-confirm":
			if !e.confirmVisible {
				return emptyFindItems(), nil
			}
			return confirmItems(), nil
		}
		return emptyFindItems(), nil
	case "/api/v1/page/list-click-by-index":
		e.chatVisible = true
		e.chatName = e.openChatName
	case "/api/v1/page/click":
		selector := stringFromMap(mapFromAny(data["element"]), "selector")
		if selector == liepinConversationFlow.ChatClose {
			e.chatVisible = false
		}
	}
	return map[string]any{"data": map[string]any{}}, nil
}

// Log 模拟日志写入。
func (e *liepinConversationExecutor) Log(string, string) {}

// Delay 记录等待时长但不真正睡眠，保证测试快速完成。
func (e *liepinConversationExecutor) Delay(_ context.Context, _ string, _ float64) error { return nil }

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

// confirmItems 返回索要确认弹框主按钮元素响应。
func confirmItems() map[string]any {
	return map[string]any{"data": map[string]any{"items": []any{
		map[string]any{"element_ref": "confirm-ref"},
	}}}
}

// clickTargetSelector 取出点击类调用中目标元素的 selector。
func clickTargetSelector(payload map[string]any) string {
	return stringFromMap(mapFromAny(payload["clickTarget"]), "selector")
}

// TestRequestCandidateInfoOpensChatByContinueButton 验证聊天框未打开时点击卡片继续沟通按钮并完成索要与发送。
func TestRequestCandidateInfoOpensChatByContinueButton(t *testing.T) {
	exec := &liepinConversationExecutor{confirmVisible: true, openChatName: "张三"}
	cfg := cloudapi.PlatformConfig{"id": "liepin", "card": map[string]any{
		"item": map[string]any{"selector": ".candidate-card"},
	}}
	runtime := NewRuntime()
	err := runtime.RequestCandidateInfo(context.Background(), exec, cfg, platformcore.Candidate{"candidate_name": "张三", "card_index": 3}, platformcore.CandidateInfoRequest{
		RequestPhone: true, RequestResume: true, GreetMessage: "期待与您沟通",
	})
	if err != nil {
		t.Fatal(err)
	}
	clicked := false
	for _, call := range exec.calls {
		if call.path != "/api/v1/page/list-click-by-index" {
			continue
		}
		clicked = true
		if intFromMap(call.payload, "index") != 3 {
			t.Fatal("继续沟通点击没有使用当前卡片的序号")
		}
		if got := clickTargetSelector(call.payload); got != liepinContinueButtonSelector {
			t.Fatalf("clickTarget = %s，应回退到稳定埋点选择器", got)
		}
		if got := stringFromMap(mapFromAny(call.payload["item"]), "selector"); got != ".candidate-card" {
			t.Fatalf("item selector = %s，应使用云端候选人卡片选择器", got)
		}
	}
	if !clicked {
		t.Fatal("聊天框未打开时没有点击继续沟通按钮")
	}
	if !hasClickSelector(exec, liepinConversationRequestButtons.Phone.Selector) {
		t.Fatal("没有点击索要手机号按钮")
	}
	if !hasClickSelector(exec, liepinConversationRequestButtons.Resume.Selector) {
		t.Fatal("没有点击索要简历按钮")
	}
	if !hasTypeText(exec, "期待与您沟通") {
		t.Fatal("没有输入追加问候语")
	}
	if !hasPressEnter(exec) {
		t.Fatal("猎聘没有按 Enter 发送消息")
	}
}

// TestRequestCandidateInfoUsesCloudContinueButton 验证云端配置了继续沟通按钮时优先使用配置。
func TestRequestCandidateInfoUsesCloudContinueButton(t *testing.T) {
	exec := &liepinConversationExecutor{confirmVisible: true, openChatName: "张三"}
	cfg := cloudapi.PlatformConfig{"id": "liepin", "card": map[string]any{
		"item": map[string]any{"selector": ".candidate-card"},
	}, "actions": map[string]any{
		"continueBtn": map[string]any{"selector": ".cfg-continue"},
	}}
	runtime := NewRuntime()
	if err := runtime.RequestCandidateInfo(context.Background(), exec, cfg, platformcore.Candidate{"candidate_name": "张三", "card_index": 0}, platformcore.CandidateInfoRequest{RequestPhone: true}); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, call := range exec.calls {
		if call.path == "/api/v1/page/list-click-by-index" && clickTargetSelector(call.payload) == ".cfg-continue" {
			found = true
		}
	}
	if !found {
		t.Fatal("云端配置的继续沟通按钮没有生效")
	}
}

// TestRequestCandidateInfoSkipsEmptyRequest 验证岗位未配置索要和问候语时不执行任何页面动作。
func TestRequestCandidateInfoSkipsEmptyRequest(t *testing.T) {
	exec := &liepinConversationExecutor{}
	runtime := NewRuntime()
	if err := runtime.RequestCandidateInfo(context.Background(), exec, nil, nil, platformcore.CandidateInfoRequest{}); err != nil {
		t.Fatal(err)
	}
	if len(exec.calls) != 0 {
		t.Fatalf("calls = %d，空请求不应有调用", len(exec.calls))
	}
}

// hasClickSelector 判断是否点击过指定选择器。
func hasClickSelector(exec *liepinConversationExecutor, selector string) bool {
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

// hasTypeText 判断是否向输入框输入过指定文本。
func hasTypeText(exec *liepinConversationExecutor, text string) bool {
	for _, call := range exec.calls {
		if call.path == "/api/v1/page/type" && stringFromMap(call.payload, "text") == text {
			return true
		}
	}
	return false
}

// hasPressEnter 判断是否按过 Enter 键。
func hasPressEnter(exec *liepinConversationExecutor) bool {
	for _, call := range exec.calls {
		if call.path == "/api/v1/page/press-key" && stringFromMap(call.payload, "key") == "Enter" {
			return true
		}
	}
	return false
}

var _ = strings.TrimSpace
