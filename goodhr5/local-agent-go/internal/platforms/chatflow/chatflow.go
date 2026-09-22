// Package chatflow 文件作用：提供各平台共用的候选人聊天框等待、身份核对、索要信息、消息发送和弹层关闭流程，
// 供 Boss、猎聘企业端等平台在打招呼后索要候选人信息时复用，避免每个平台重复实现相同的轮询与核对逻辑。
package chatflow

import (
	"context"
	"fmt"
	"strings"
	"time"

	"goodhr5/local-agent-go/internal/platformcore"
)

const (
	// NameCheckAttempts 是单轮探测中读取聊天框候选人姓名的重试次数。
	NameCheckAttempts = 5
	// ConfirmDialogPollCount 是索要确认弹框出现与否的探测轮数。
	ConfirmDialogPollCount = 10
	// ConfirmDialogPollInterval 是确认弹框探测的轮询间隔秒数。
	ConfirmDialogPollInterval = 0.1
	// ClosePollCount 是关闭聊天框后确认弹层消失的轮询轮数。
	ClosePollCount = 20
	// ClosePollInterval 是关闭确认的轮询间隔秒数。
	ClosePollInterval = 0.1
)

// Flow 描述一个平台候选人聊天框的稳定选择器与轮询参数。
// 字段值均为页面 CSS 选择器，由各平台包使用自己的稳定埋点常量填充。
type Flow struct {
	Label        string  // 平台展示名，用于日志前缀，如“Boss”“猎聘”
	ChatModal    string  // 聊天框根节点选择器
	ChatName     string  // 聊天框内候选人姓名选择器
	ChatClose    string  // 聊天框关闭按钮选择器
	Input        string  // 消息输入框选择器
	SendButton   string  // 发送按钮选择器，为空时按 Enter 发送
	PollInterval float64 // 等待聊天框的默认轮询间隔秒数，0 时按 0.2 秒处理
}

// RequestButton 描述一个索要动作的入口按钮与确认弹框选择器。
type RequestButton struct {
	Label         string // 动作展示名，如“手机号”
	Selector      string // 入口按钮选择器
	ConfirmDialog string // 确认弹框容器选择器，为空表示该动作没有确认环节
	ConfirmButton string // 确认弹框内的主按钮选择器，为空时点击 ConfirmDialog 本身
}

// RequestButtons 汇总手机号、微信、简历三类索要动作的按钮配置。
type RequestButtons struct {
	Phone  RequestButton // 索要手机号配置
	Wechat RequestButton // 索要微信配置
	Resume RequestButton // 索要简历配置
}

// AwaitConversation 轮询探测候选人聊天框状态。
// 返回 matched 表示聊天框已打开且姓名属于当前候选人，opened 表示聊天框已打开但可能属于其他候选人。
// 轮询期间发现属于其他候选人的聊天框会立即返回，由调用方决定是否清理后重新打开。
func AwaitConversation(ctx context.Context, exec platformcore.Executor, flow Flow, expectedName string, attempts int, interval float64) (matched bool, opened bool, err error) {
	if interval <= 0 {
		interval = flow.PollInterval
		if interval <= 0 {
			interval = 0.2
		}
	}
	startedAt := time.Now()
	for attempt := 1; attempt <= attempts; attempt++ {
		state, stateErr := inspectConversation(ctx, exec, flow)
		if stateErr != nil {
			return false, false, stateErr
		}
		if state.count > 1 {
			return false, false, fmt.Errorf("%s聊天框数量异常：数量=%d", flow.Label, state.count)
		}
		if state.count == 1 {
			if state.name == "" {
				// 姓名还没有渲染出来，继续等下一轮。
			} else if NamesMatch(expectedName, state.name) {
				exec.Log("info", fmt.Sprintf("%s索要信息状态：候选人=%s，聊天框已确认，聊天姓名=%s，轮次=%d/%d，耗时=%s", flow.Label, expectedName, state.name, attempt, attempts, time.Since(startedAt).Round(time.Millisecond)))
				return true, true, nil
			} else {
				exec.Log("warning", fmt.Sprintf("%s索要信息状态：候选人=%s，当前聊天姓名=%s，不属于当前候选人", flow.Label, expectedName, state.name))
				return false, true, nil
			}
		}
		if attempt < attempts {
			if err := exec.Delay(ctx, "等待"+flow.Label+"候选人聊天框", interval); err != nil {
				return false, false, err
			}
		}
	}
	return false, false, nil
}

// RequestInfo 按岗位勾选项在已确认身份的聊天框内依次执行索要动作并处理确认弹框。
// 任一动作失败即返回错误，由调用方决定整体重试或跳过。
func RequestInfo(ctx context.Context, exec platformcore.Executor, flow Flow, buttons RequestButtons, request platformcore.CandidateInfoRequest) error {
	steps := []struct {
		enabled bool
		button  RequestButton
	}{
		{request.RequestPhone, buttons.Phone},
		{request.RequestWechat, buttons.Wechat},
		{request.RequestResume, buttons.Resume},
	}
	for _, step := range steps {
		if !step.enabled {
			continue
		}
		if step.button.Selector == "" {
			return fmt.Errorf("%s没有配置索要%s的入口按钮", flow.Label, step.button.Label)
		}
		exec.Log("info", fmt.Sprintf("%s索要信息：准备索要%s", flow.Label, step.button.Label))
		if _, err := exec.Post(ctx, "/api/v1/page/click", map[string]any{
			"element": map[string]any{"selector": step.button.Selector},
			"timeout": 5000,
		}); err != nil {
			return fmt.Errorf("点击%s“索要%s”失败：%w", flow.Label, step.button.Label, err)
		}
		if step.button.ConfirmDialog == "" {
			continue
		}
		if err := confirmRequestIfPresent(ctx, exec, flow, step.button); err != nil {
			return err
		}
	}
	return nil
}

// SendMessage 在已确认身份的聊天框内输入并发送消息。
// 配置了发送按钮时优先点击按钮，否则按 Enter 发送。
func SendMessage(ctx context.Context, exec platformcore.Executor, flow Flow, message string) error {
	if strings.TrimSpace(message) == "" {
		return fmt.Errorf("%s发送内容不能为空", flow.Label)
	}
	if _, err := exec.Post(ctx, "/api/v1/page/type", map[string]any{
		"element": map[string]any{"selector": flow.Input},
		"text":    message,
		"timeout": 5000,
	}); err != nil {
		return fmt.Errorf("输入%s消息失败：%w", flow.Label, err)
	}
	if flow.SendButton == "" {
		if _, err := exec.Post(ctx, "/api/v1/page/press-key", map[string]any{"key": "Enter"}); err != nil {
			return fmt.Errorf("按 Enter 发送%s消息失败：%w", flow.Label, err)
		}
		return nil
	}
	if _, err := exec.Post(ctx, "/api/v1/page/click", map[string]any{
		"element": map[string]any{"selector": flow.SendButton},
		"timeout": 5000,
	}); err != nil {
		return fmt.Errorf("点击%s发送按钮失败：%w", flow.Label, err)
	}
	return nil
}

// CloseConversation 点击关闭按钮并轮询确认聊天框消失，页面恢复干净后才允许主流程继续。
func CloseConversation(ctx context.Context, exec platformcore.Executor, flow Flow) error {
	startedAt := time.Now()
	if _, err := exec.Post(ctx, "/api/v1/page/click", map[string]any{
		"element": map[string]any{"selector": flow.ChatClose},
		"timeout": 5000,
	}); err != nil {
		return fmt.Errorf("关闭%s聊天框失败：%w", flow.Label, err)
	}
	for attempt := 1; attempt <= ClosePollCount; attempt++ {
		state, err := inspectConversation(ctx, exec, flow)
		if err != nil {
			return err
		}
		if state.count == 0 {
			exec.Log("info", fmt.Sprintf("%s索要信息状态：聊天框已关闭，轮次=%d/%d，耗时=%s", flow.Label, attempt, ClosePollCount, time.Since(startedAt).Round(time.Millisecond)))
			return nil
		}
		if attempt < ClosePollCount {
			if err := exec.Delay(ctx, "等待"+flow.Label+"聊天框关闭", ClosePollInterval); err != nil {
				return err
			}
		}
	}
	return fmt.Errorf("关闭%s聊天框后弹层未消失，耗时=%s", flow.Label, time.Since(startedAt).Round(time.Millisecond))
}

// NamesMatch 比较完整或脱敏的候选人姓名。
// 姓名含星号时按脱敏规则只校验首字符，否则要求相等或互为包含，均忽略空白和常见称谓后缀。
func NamesMatch(expected string, actual string) bool {
	expected = normalizeName(expected)
	actual = normalizeName(actual)
	if expected == "" || actual == "" {
		return false
	}
	if strings.Contains(expected, "*") || strings.Contains(actual, "*") {
		return []rune(expected)[0] == []rune(actual)[0]
	}
	return expected == actual || strings.Contains(expected, actual) || strings.Contains(actual, expected)
}

// conversationState 记录一次聊天框探测的数量和候选人姓名。
type conversationState struct {
	count int
	name  string
}

// inspectConversation 查询当前可见聊天框数量和聊天框内的候选人姓名。
func inspectConversation(ctx context.Context, exec platformcore.Executor, flow Flow) (conversationState, error) {
	state := conversationState{}
	result, err := exec.Post(ctx, "/api/v1/page/find-elements", map[string]any{
		"element":      map[string]any{"selector": flow.ChatModal},
		"visible_only": true,
		"max_items":    2,
		"fields":       []any{map[string]any{"chat_name": map[string]any{"selector": flow.ChatName}}},
	})
	if err != nil {
		return state, fmt.Errorf("查询%s聊天框状态失败：%w", flow.Label, err)
	}
	items := mapList(workerData(result, "items"))
	state.count = len(items)
	if state.count == 0 {
		return state, nil
	}
	fields := mapFromAny(items[0]["fields"])
	name := strings.TrimSpace(stringFromMap(fields, "chat_name"))
	// 姓名偶尔晚于聊天框渲染，短轮询重试避免误判为他人会话。
	for attempt := 1; name == "" && attempt < NameCheckAttempts; attempt++ {
		if err := exec.Delay(ctx, "等待"+flow.Label+"聊天姓名渲染", 0.1); err != nil {
			return state, err
		}
		retry, err := inspectConversationName(ctx, exec, flow)
		if err != nil {
			return state, err
		}
		name = retry
	}
	state.name = name
	return state, nil
}

// inspectConversationName 单独读取聊天框内的候选人姓名，用于姓名延迟渲染时的重试。
func inspectConversationName(ctx context.Context, exec platformcore.Executor, flow Flow) (string, error) {
	result, err := exec.Post(ctx, "/api/v1/page/find-elements", map[string]any{
		"element":      map[string]any{"selector": flow.ChatModal},
		"visible_only": true,
		"max_items":    1,
		"fields":       []any{map[string]any{"chat_name": map[string]any{"selector": flow.ChatName}}},
	})
	if err != nil {
		return "", fmt.Errorf("读取%s聊天姓名失败：%w", flow.Label, err)
	}
	items := mapList(workerData(result, "items"))
	if len(items) == 0 {
		return "", nil
	}
	fields := mapFromAny(items[0]["fields"])
	return strings.TrimSpace(stringFromMap(fields, "chat_name")), nil
}

// confirmRequestIfPresent 探测索要确认弹框，存在则点击主按钮，探测期内未出现则跳过确认。
func confirmRequestIfPresent(ctx context.Context, exec platformcore.Executor, flow Flow, button RequestButton) error {
	startedAt := time.Now()
	for attempt := 1; attempt <= ConfirmDialogPollCount; attempt++ {
		result, err := exec.Post(ctx, "/api/v1/page/find-elements", map[string]any{
			"element":      map[string]any{"selector": button.ConfirmDialog},
			"visible_only": true,
			"max_items":    1,
		})
		if err != nil {
			return fmt.Errorf("查找%s“索要%s”确认弹框失败：%w", flow.Label, button.Label, err)
		}
		items := mapList(workerData(result, "items"))
		if len(items) > 0 {
			target := button.ConfirmButton
			if target == "" {
				target = button.ConfirmDialog
			}
			payload := map[string]any{"timeout": 5000}
			if ref := firstNonEmpty(stringFromMap(items[0], "element_ref"), stringFromMap(items[0], "ref")); ref != "" && target == button.ConfirmDialog {
				payload["element_ref"] = ref
			} else {
				payload["element"] = map[string]any{"selector": target}
			}
			if _, err := exec.Post(ctx, "/api/v1/page/click", payload); err != nil {
				return fmt.Errorf("确认%s向候选人索要%s失败：%w", flow.Label, button.Label, err)
			}
			exec.Log("info", fmt.Sprintf("%s索要信息状态：已确认索要%s，耗时=%s", flow.Label, button.Label, time.Since(startedAt).Round(time.Millisecond)))
			return nil
		}
		if attempt < ConfirmDialogPollCount {
			if err := exec.Delay(ctx, "轮询"+flow.Label+"索要"+button.Label+"确认弹框", ConfirmDialogPollInterval); err != nil {
				return err
			}
		}
	}
	exec.Log("info", fmt.Sprintf("%s索要信息状态：未出现“索要%s”确认弹框，跳过确认，耗时=%s", flow.Label, button.Label, time.Since(startedAt).Round(time.Millisecond)))
	return nil
}
