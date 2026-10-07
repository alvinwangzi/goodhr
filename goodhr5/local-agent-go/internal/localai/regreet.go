// 本文件实现复打招呼的 AI 决策：判断候选人是否明确拒绝过 + 生成复打内容。
// 一次 AI 调用同时完成拒绝检测和消息生成，减少 token 消耗。
package localai

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"
)

// ReGreetRequest 保存复打招呼 AI 决策所需的岗位规则和会话数据。
type ReGreetRequest struct {
	ReGreetPrompt       string // 岗位复打提示词（岗位级配置），为空时使用系统默认
	CandidateName       string // 候选人姓名
	PositionRequirement string // 岗位要求
	GreetMessage        string // 首次打招呼时我方发送的消息
	ConversationHistory string // 聊天上下文（按时间从旧到新排列）
	SkipRefusedCheck    bool   // true 时 AI 不判断拒绝，只生成复打内容
}

// ReGreetDecision 区分是否发送、复打内容和是否被拒绝。
type ReGreetDecision struct {
	ShouldSend   bool   `json:"should_send"`
	Message      string `json:"message"`
	IsRefused    bool   `json:"is_refused"`
	RefuseReason string `json:"refuse_reason"`
}

// GenerateReGreet 基于岗位复打提示词和聊天上下文生成复打内容，同时判断候选人是否明确拒绝过。
// 首次复打且对方完全没回复时，is_refused 恒为 false；第二次及以后的复打才需要检测拒绝。
func (c *Client) GenerateReGreet(ctx context.Context, request ReGreetRequest) (ReGreetDecision, error) {
	if err := ctx.Err(); err != nil {
		return ReGreetDecision{}, err
	}
	system := buildReGreetSystemPrompt(request)
	user := buildReGreetUserMessage(request)
	payload := map[string]any{
		"messages": []map[string]string{
			{"role": "system", "content": system},
			{"role": "user", "content": user},
		},
		"stream": true,
	}
	if !c.EnableThinking {
		payload["enable_thinking"] = false
	}
	// 复用 HTTP/SSE 能力，关闭进度回调和提前决策。
	replyClient := *c
	replyClient.Progress = nil
	replyClient.EarlyDecision = nil
	replyClient.Config.Extra = make(map[string]any, len(c.Config.Extra))
	for key, value := range c.Config.Extra {
		if key != "enable_thinking" {
			replyClient.Config.Extra[key] = value
		}
	}
	result, err := replyClient.Chat(ctx, payload)
	if err != nil {
		return ReGreetDecision{}, err
	}
	if err := ctx.Err(); err != nil {
		return ReGreetDecision{}, err
	}
	var raw struct {
		ShouldSend   *bool  `json:"should_send"`
		Message      string `json:"message"`
		IsRefused    bool   `json:"is_refused"`
		RefuseReason string `json:"refuse_reason"`
	}
	if err := json.Unmarshal([]byte(cleanAIText(result.Content)), &raw); err != nil || raw.ShouldSend == nil {
		return ReGreetDecision{}, fmt.Errorf("AI 复打决策格式不完整：%w", err)
	}
	decision := ReGreetDecision{
		ShouldSend:   *raw.ShouldSend,
		Message:      strings.TrimSpace(raw.Message),
		IsRefused:    raw.IsRefused,
		RefuseReason: strings.TrimSpace(raw.RefuseReason),
	}
	if request.SkipRefusedCheck {
		decision.IsRefused = false
		decision.RefuseReason = ""
	}
	// 校验：发送时消息必须非空且不超过 200 字。
	if decision.ShouldSend {
		if decision.Message == "" || utf8.RuneCountInString(decision.Message) > 200 {
			return ReGreetDecision{}, fmt.Errorf("AI 复打内容为空或超过 200 字")
		}
		// 被拒绝时不应发送。
		if decision.IsRefused {
			decision.ShouldSend = false
		}
	}
	return decision, nil
}

// buildReGreetSystemPrompt 组装复打 AI 的系统提示词。
func buildReGreetSystemPrompt(request ReGreetRequest) string {
	base := strings.TrimSpace(request.ReGreetPrompt)
	if base == "" {
		base = "你是招聘助理，负责对之前打过招呼但未回复的候选人再发一次招呼消息。"
	}
	base += `
以下规则始终适用：
复打消息必须简短友好，不超过 50 字，不要重复首次打招呼的原文，换一种表达方式引起候选人注意。
不编造待遇、事实或承诺；不提及具体薪资数字。
只输出一个 JSON 对象，不输出 Markdown 或解释：{"should_send":true,"message":"复打内容","is_refused":false,"refuse_reason":""}。
should_send=false 时 message 必须为空。refuse_reason 不超过 50 字。`
	if !request.SkipRefusedCheck {
		base += `
拒绝检测规则：如果聊天上下文中候选人明确表达过"不需要""别发了""不考虑""已有offer"等拒绝意思，is_refused 设为 true 并填写 refuse_reason，should_send 设为 false。
首次复打且对方完全没回复时，is_refused 恒为 false。`
	} else {
		base += `
本轮跳过拒绝检测，is_refused 恒为 false，只判断是否发送和生成消息内容。`
	}
	return base
}

// buildReGreetUserMessage 组装发给模型的复打用户消息。
func buildReGreetUserMessage(request ReGreetRequest) string {
	var b strings.Builder
	fmt.Fprintf(&b, "候选人：%s\n", request.CandidateName)
	if request.PositionRequirement != "" {
		fmt.Fprintf(&b, "岗位要求：%s\n", request.PositionRequirement)
	}
	if request.GreetMessage != "" {
		fmt.Fprintf(&b, "首次打招呼内容：%s\n", request.GreetMessage)
	}
	if request.ConversationHistory != "" {
		fmt.Fprintf(&b, "聊天上下文（按时间从旧到新）：\n%s\n", request.ConversationHistory)
	} else {
		b.WriteString("聊天上下文：候选人未回复过，无聊天内容。\n")
	}
	b.WriteString("目标：生成一条简短的复打消息，引起候选人注意。")
	return b.String()
}
