// Package chatflow 本文件验证共享聊天框流程的姓名比对、等待探测、索要、发送和关闭行为。
package chatflow

import (
	"context"
	"strings"
	"testing"

	"goodhr5/local-agent-go/internal/platformcore"
)

// flowCall 记录一次 Worker 调用的路径和参数。
type flowCall struct {
	path    string
	payload map[string]any
}

// flowExecutor 模拟聊天框相关 Worker 接口，按选择器返回预设状态。
type flowExecutor struct {
	calls             []flowCall
	chatCount         int
	chatName          string
	confirmVisible    bool
	chatCloseDisabled bool
}

// Post 记录调用并按路径与选择器返回模拟结果。
func (e *flowExecutor) Post(_ context.Context, path string, payload any) (map[string]any, error) {
	data, _ := payload.(map[string]any)
	e.calls = append(e.calls, flowCall{path: path, payload: data})
	switch path {
	case "/api/v1/page/find-elements":
		selector := stringFromMap(mapFromAny(data["element"]), "selector")
		switch selector {
		case testFlow.ChatModal:
			count := e.chatCount
			if maxItems, ok := data["max_items"].(int); ok && count > maxItems {
				count = maxItems
			}
			items := make([]any, 0, count)
			for i := 0; i < count; i++ {
				items = append(items, map[string]any{
					"element_ref": "chat-ref",
					"fields":      map[string]any{"chat_name": e.chatName},
				})
			}
			return map[string]any{"data": map[string]any{"items": items}}, nil
		case testButtons.Phone.ConfirmDialog:
			if !e.confirmVisible {
				return emptyItems(), nil
			}
			return map[string]any{"data": map[string]any{"items": []any{
				map[string]any{"element_ref": "confirm-ref"},
			}}}, nil
		}
		return emptyItems(), nil
	case "/api/v1/page/click":
		selector := stringFromMap(mapFromAny(data["element"]), "selector")
		if selector == testFlow.ChatClose && !e.chatCloseDisabled {
			e.chatCount = 0
		}
	}
	return map[string]any{"data": map[string]any{}}, nil
}

// Log 模拟日志写入。
func (e *flowExecutor) Log(string, string) {}

// Delay 模拟等待但不真正睡眠，保证测试快速完成。
func (e *flowExecutor) Delay(_ context.Context, _ string, _ float64) error { return nil }

// testFlow 是测试共用的聊天框选择器配置。
var testFlow = Flow{
	Label:      "测试",
	ChatModal:  ".test-chat",
	ChatName:   ".test-chat-name",
	ChatClose:  ".test-chat-close",
	Input:      ".test-chat-input",
	SendButton: ".test-chat-send",
}

// testButtons 是测试共用的索要按钮配置。
var testButtons = RequestButtons{
	Phone: RequestButton{Label: "手机号", Selector: ".test-phone", ConfirmDialog: ".test-confirm", ConfirmButton: ".test-confirm-ok"},
}

// emptyItems 返回空元素列表响应。
func emptyItems() map[string]any {
	return map[string]any{"data": map[string]any{"items": []any{}}}
}

// TestNamesMatchRules 验证完整、包含、脱敏和称谓后缀的姓名比对规则。
func TestNamesMatchRules(t *testing.T) {
	cases := []struct {
		expected string
		actual   string
		want     bool
	}{
		{"张三", "张三", true},
		{"张三", " 张三 先生 ", true},
		{"张三三", "张三", true},
		{"李四", "张三", false},
		{"张*", "张三", true},
		{"张三", "张**", true},
		{"李**", "李四", true},
		{"李**", "张三", false},
		{"", "张三", false},
		{"张三", "", false},
	}
	for _, item := range cases {
		if got := NamesMatch(item.expected, item.actual); got != item.want {
			t.Fatalf("NamesMatch(%q, %q) = %v，期望 %v", item.expected, item.actual, got, item.want)
		}
	}
}

// TestAwaitConversationResults 验证等待探测的匹配、他人会话、未打开和多聊天框四种结果。
func TestAwaitConversationResults(t *testing.T) {
	cases := []struct {
		name            string
		exec            *flowExecutor
		expectedName    string
		wantMatched     bool
		wantOpened      bool
		wantErrContains string
	}{
		{name: "本人聊天框匹配", exec: &flowExecutor{chatCount: 1, chatName: "张三女士"}, expectedName: "张三", wantMatched: true, wantOpened: true},
		{name: "他人聊天框立即返回", exec: &flowExecutor{chatCount: 1, chatName: "李四"}, expectedName: "张三", wantOpened: true},
		{name: "未打开等待超时", exec: &flowExecutor{}, expectedName: "张三"},
		{name: "聊天框数量异常", exec: &flowExecutor{chatCount: 2, chatName: "张三"}, expectedName: "张三", wantErrContains: "数量异常"},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			matched, opened, err := AwaitConversation(context.Background(), item.exec, testFlow, item.expectedName, 3, 0.1)
			if item.wantErrContains != "" {
				if err == nil || !strings.Contains(err.Error(), item.wantErrContains) {
					t.Fatalf("err = %v，应包含 %q", err, item.wantErrContains)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if matched != item.wantMatched || opened != item.wantOpened {
				t.Fatalf("matched=%v opened=%v，期望 matched=%v opened=%v", matched, opened, item.wantMatched, item.wantOpened)
			}
		})
	}
}

// TestRequestInfoClicksEnabledButtonsOnly 验证只点击勾选的索要按钮并确认弹框，未勾选不点击。
func TestRequestInfoClicksEnabledButtonsOnly(t *testing.T) {
	exec := &flowExecutor{chatCount: 1, confirmVisible: true}
	err := RequestInfo(context.Background(), exec, testFlow, testButtons, platformcore.CandidateInfoRequest{RequestPhone: true})
	if err != nil {
		t.Fatal(err)
	}
	if !hasFlowClick(exec, testButtons.Phone.Selector) {
		t.Fatal("没有点击索要手机号按钮")
	}
	if !hasFlowClick(exec, testButtons.Phone.ConfirmButton) {
		t.Fatal("确认弹框出现时没有点击确认按钮")
	}
	if hasFlowClick(exec, testButtons.Wechat.Selector) {
		t.Fatal("未勾选微信时不应点击微信按钮")
	}
}

// TestRequestInfoFailsWithoutButton 验证勾选了索要但没有配置按钮时报错。
func TestRequestInfoFailsWithoutButton(t *testing.T) {
	exec := &flowExecutor{chatCount: 1}
	buttons := RequestButtons{Resume: RequestButton{Label: "简历"}}
	err := RequestInfo(context.Background(), exec, testFlow, buttons, platformcore.CandidateInfoRequest{RequestResume: true})
	if err == nil || !strings.Contains(err.Error(), "简历") {
		t.Fatalf("err = %v，应提示未配置索要按钮", err)
	}
}

// TestSendMessageButtonAndEnter 验证配置发送按钮时点击按钮，未配置时按 Enter。
func TestSendMessageButtonAndEnter(t *testing.T) {
	exec := &flowExecutor{chatCount: 1}
	if err := SendMessage(context.Background(), exec, testFlow, "你好"); err != nil {
		t.Fatal(err)
	}
	if !hasFlowClick(exec, testFlow.SendButton) {
		t.Fatal("配置发送按钮时应点击发送按钮")
	}
	enterFlow := testFlow
	enterFlow.SendButton = ""
	exec2 := &flowExecutor{chatCount: 1}
	if err := SendMessage(context.Background(), exec2, enterFlow, "你好"); err != nil {
		t.Fatal(err)
	}
	if !hasFlowPressKey(exec2, "Enter") {
		t.Fatal("未配置发送按钮时应按 Enter 发送")
	}
	if err := SendMessage(context.Background(), exec, testFlow, "  "); err == nil {
		t.Fatal("空内容应报错")
	}
}

// TestCloseConversationVerifiesDisappearance 验证关闭后确认弹层消失，未消失时报错。
func TestCloseConversationVerifiesDisappearance(t *testing.T) {
	exec := &flowExecutor{chatCount: 1}
	if err := CloseConversation(context.Background(), exec, testFlow); err != nil {
		t.Fatal(err)
	}
	if !hasFlowClick(exec, testFlow.ChatClose) {
		t.Fatal("没有点击聊天框关闭按钮")
	}
	// 关闭点击无效时，弹层始终存在应报错。
	sticky := &flowExecutor{chatCount: 1, chatName: "张三", chatCloseDisabled: true}
	if err := CloseConversation(context.Background(), sticky, testFlow); err == nil {
		t.Fatal("弹层未消失时应报错")
	}
}

// hasFlowClick 判断是否点击过指定选择器。
func hasFlowClick(exec *flowExecutor, selector string) bool {
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

// hasFlowPressKey 判断是否按过指定按键。
func hasFlowPressKey(exec *flowExecutor, key string) bool {
	for _, call := range exec.calls {
		if call.path == "/api/v1/page/press-key" && stringFromMap(call.payload, "key") == key {
			return true
		}
	}
	return false
}
