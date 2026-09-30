// Package boss 文件作用：岗位收尾阶段的回复检查流程——
// 切到 Boss 消息页，在会话列表中核对候选人是否已回复，已回复则打开会话执行"求简历"。
// 全程使用 worker 标准接口和 Playwright 定位能力，不注入任何页面脚本。
package boss

import (
	"context"
	"fmt"
	"strings"
	"time"

	"goodhr5/local-agent-go/internal/cloudapi"
	"goodhr5/local-agent-go/internal/platformcore"
	"goodhr5/local-agent-go/internal/platforms/chatflow"
)

// bossReplyFlow 描述 Boss 消息页右侧会话面板的稳定选择器。
// 消息页右侧面板与推荐牛人页聊天框同构，复用共享的身份核对逻辑。
var bossReplyFlow = chatflow.Flow{
	Label:     "Boss消息页",
	ChatModal: ".chat-global-conversation",
	ChatName:  ".chatview-name",
}

const (
	// bossReplyConversationPollCount 是点击会话项后等待右侧面板确认姓名的轮数。
	bossReplyConversationPollCount = 30
	// bossReplyConversationPollInterval 是右侧面板确认轮询的间隔秒数。
	bossReplyConversationPollInterval = 0.2
	// bossReplyScrollAttempts 是会话列表找不到候选人时最多向下滚动重试的屏数。
	bossReplyScrollAttempts = 5
	// bossReplyMenuWaitTimeout 是点击"沟通"菜单后等待进入消息页的最长时间。
	bossReplyMenuWaitTimeout = 8 * time.Second
)

// CheckResumeRequests 在岗位收尾时检查候选人是否已回复，已回复的执行求简历并返回逐人结果。
// 实现平台可选能力 ResumeRequestChecker；不在返回表中的姓名由调用方按"未回复"继续保留。
// ctx 为运行上下文，exec 为执行器，cfg 为平台配置，candidateNames 为待检查候选人姓名。
func (r *Runtime) CheckResumeRequests(ctx context.Context, exec platformcore.Executor, cfg cloudapi.PlatformConfig, candidateNames []string) (map[string]platformcore.ResumeRequestOutcome, error) {
	outcomes := make(map[string]platformcore.ResumeRequestOutcome, len(candidateNames))
	if len(candidateNames) == 0 {
		return outcomes, nil
	}
	if err := r.openChatHomePage(ctx, exec); err != nil {
		return nil, err
	}
	for _, name := range candidateNames {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if err := ctx.Err(); err != nil {
			return outcomes, err
		}
		outcomes[name] = r.checkOneResumeRequest(ctx, exec, cfg, name)
	}
	return outcomes, nil
}

// checkOneResumeRequest 检查单个候选人：查找会话、判断回复状态，已回复则打开会话并求简历。
// name 为候选人姓名，返回该候选人的处理结果。
func (r *Runtime) checkOneResumeRequest(ctx context.Context, exec platformcore.Executor, cfg cloudapi.PlatformConfig, name string) platformcore.ResumeRequestOutcome {
	text, ref, found := r.locateChatSession(ctx, exec, cfg, name)
	if !found {
		exec.Log("info", fmt.Sprintf("Boss回复检查：候选人=%s，会话列表滚动后仍未找到", name))
		return platformcore.ResumeRequestOutcome{Status: "not_found", Reason: "会话列表中未找到该候选人的会话"}
	}
	if isUnrepliedSessionText(text) {
		exec.Log("info", fmt.Sprintf("Boss回复检查：候选人=%s，最后一条消息仍是我方发出，继续等待回复", name))
		return platformcore.ResumeRequestOutcome{Status: "pending"}
	}
	exec.Log("info", fmt.Sprintf("Boss回复检查：候选人=%s，候选人已回复，准备求简历", name))
	return r.requestResumeFromSession(ctx, exec, cfg, name, ref)
}

// locateChatSession 在会话列表中查找候选人会话项，找不到时模拟真实用户向下滚动重试。
// 返回会话项文本、元素引用和是否找到。
func (r *Runtime) locateChatSession(ctx context.Context, exec platformcore.Executor, cfg cloudapi.PlatformConfig, name string) (string, string, bool) {
	for attempt := 0; attempt <= bossReplyScrollAttempts; attempt++ {
		session, err := r.findChatSession(ctx, exec, cfg, name)
		if err != nil {
			exec.Log("warning", fmt.Sprintf("Boss回复检查：候选人=%s，查找会话失败，错误=%s", name, err.Error()))
			return "", "", false
		}
		if boolFromMap(session, "found") {
			return stringFromMap(session, "text"), stringFromMap(session, "element_ref"), true
		}
		if attempt == bossReplyScrollAttempts {
			break
		}
		scrolled, err := r.scrollChatList(ctx, exec, cfg)
		if err != nil {
			exec.Log("warning", fmt.Sprintf("Boss回复检查：候选人=%s，滚动会话列表失败，错误=%s", name, err.Error()))
			return "", "", false
		}
		// 滚动前后首个会话文本一致说明列表已经到底，提前结束重试。
		if before, after := stringFromMap(scrolled, "before_text"), stringFromMap(scrolled, "after_text"); before != "" && before == after {
			break
		}
	}
	return "", "", false
}

// findChatSession 调用 worker 会话查找接口（滚动模式），返回候选人会话项文本与元素引用。
func (r *Runtime) findChatSession(ctx context.Context, exec platformcore.Executor, cfg cloudapi.PlatformConfig, name string) (map[string]any, error) {
	result, err := exec.Post(ctx, "/api/v1/boss/chat/find-session", map[string]any{
		"candidate_name":  name,
		"platform_config": cfg,
	})
	if err != nil {
		return nil, err
	}
	return workerDataMap(result), nil
}

// searchChatSession 调用 worker 搜索接口（搜索框模式），按姓名查找候选人会话。
// 点击搜索按钮 → 输入姓名 → 返回匹配的会话项。
func (r *Runtime) searchChatSession(ctx context.Context, exec platformcore.Executor, name string) (map[string]any, error) {
	result, err := exec.Post(ctx, "/api/v1/boss/chat/search-session", map[string]any{
		"candidate_name": name,
	})
	if err != nil {
		return nil, err
	}
	return workerDataMap(result), nil
}

// scrollChatList 把鼠标移到会话列表中部并用真实滚轮下滚一屏，返回滚动前后首个会话项文本。
func (r *Runtime) scrollChatList(ctx context.Context, exec platformcore.Executor, cfg cloudapi.PlatformConfig) (map[string]any, error) {
	result, err := exec.Post(ctx, "/api/v1/boss/chat/scroll-list", map[string]any{
		"distance":        600,
		"platform_config": cfg,
	})
	if err != nil {
		return nil, err
	}
	return workerDataMap(result), nil
}

// requestResumeFromSession 点击候选人会话项打开右侧面板，核对身份后执行求简历。
// name 为候选人姓名，ref 为会话项元素引用，返回处理结果。
func (r *Runtime) requestResumeFromSession(ctx context.Context, exec platformcore.Executor, cfg cloudapi.PlatformConfig, name string, ref string) platformcore.ResumeRequestOutcome {
	if _, err := exec.Post(ctx, "/api/v1/page/click", map[string]any{
		"element_ref": ref,
		"timeout":     3000,
	}); err != nil {
		return platformcore.ResumeRequestOutcome{Status: "failed", Reason: fmt.Sprintf("点击候选人会话失败：%v", err)}
	}
	matched, _, err := chatflow.AwaitConversation(ctx, exec, bossReplyFlow, name, bossReplyConversationPollCount, bossReplyConversationPollInterval)
	if err != nil {
		return platformcore.ResumeRequestOutcome{Status: "failed", Reason: err.Error()}
	}
	if !matched {
		return platformcore.ResumeRequestOutcome{Status: "failed", Reason: "右侧会话面板没有确认到当前候选人"}
	}
	result, err := exec.Post(ctx, "/api/v1/boss/chat/request-resume", map[string]any{
		"platform_config": cfg,
	})
	if err != nil {
		return platformcore.ResumeRequestOutcome{Status: "failed", Reason: fmt.Sprintf("点击求简历失败：%v", err)}
	}
	data := workerDataMap(result)
	if !boolFromMap(data, "clicked") {
		return platformcore.ResumeRequestOutcome{Status: "failed", Reason: "右侧工具栏没有找到可点击的求简历按钮"}
	}
	if !boolFromMap(data, "confirm_visible") {
		exec.Log("warning", fmt.Sprintf("Boss回复检查：候选人=%s，求简历确认弹窗未出现，按直接发送处理", name))
	}
	exec.Log("info", fmt.Sprintf("Boss回复检查：候选人=%s，求简历动作已完成", name))
	return platformcore.ResumeRequestOutcome{Status: "requested"}
}

// openChatHomePage 确保当前页面已切到 Boss 消息页；不在消息页时点击左侧"沟通"菜单并等待地址变化。
func (r *Runtime) openChatHomePage(ctx context.Context, exec platformcore.Executor) error {
	if on, err := r.isOnChatHomePage(ctx, exec); err == nil && on {
		exec.Log("info", "Boss回复检查：当前已在消息页，跳过菜单切换")
		return nil
	}
	exec.Log("info", "Boss回复检查：准备点击左侧沟通菜单进入消息页")
	if _, err := exec.Post(ctx, "/api/v1/page/click-by-text", map[string]any{
		"text":    "沟通",
		"exact":   false,
		"timeout": 5000,
	}); err != nil {
		return fmt.Errorf("点击 Boss 左侧沟通菜单失败：%w", err)
	}
	deadline := time.Now().Add(bossReplyMenuWaitTimeout)
	for time.Now().Before(deadline) {
		if err := ctx.Err(); err != nil {
			return err
		}
		if on, _ := r.isOnChatHomePage(ctx, exec); on {
			exec.Log("info", "Boss回复检查：已进入消息页")
			return nil
		}
		if err := exec.Delay(ctx, "等待 Boss 消息页打开", 0.3); err != nil {
			return err
		}
	}
	return fmt.Errorf("点击沟通菜单后没有进入 Boss 消息页")
}

// isOnChatHomePage 读取当前页面地址并判断是否已在 Boss 消息页。
func (r *Runtime) isOnChatHomePage(ctx context.Context, exec platformcore.Executor) (bool, error) {
	result, err := exec.Post(ctx, "/api/v1/page/list", map[string]any{})
	if err != nil {
		return false, err
	}
	pages := mapList(workerData(result, "pages"))
	if len(pages) == 0 {
		return false, nil
	}
	current := currentDefaultPage(pages)
	return strings.Contains(stringFromMap(current, "url"), "/web/chat/index"), nil
}

// isUnrepliedSessionText 判断会话项文本是否表示候选人尚未回复。
// Boss 会话预览含 [送达]/[已读] 标记表示最后一条消息仍是我方发出，候选人没有回复过。
func isUnrepliedSessionText(text string) bool {
	return strings.Contains(text, "[送达]") || strings.Contains(text, "[已读]")
}

// boolFromMap 读取布尔字段，缺失或类型不符时返回 false。
func boolFromMap(item map[string]any, key string) bool {
	value, ok := item[key]
	if !ok || value == nil {
		return false
	}
	result, ok := value.(bool)
	return ok && result
}
