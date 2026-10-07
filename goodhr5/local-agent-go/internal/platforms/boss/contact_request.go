// 本文件实现 BOSS 当前候选人面板中的电话、微信和简历确认操作；仅使用标准 Locator，结果不明时不重发。
package boss

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"time"

	"goodhr5/local-agent-go/internal/platformcore"
)

// contactRequestConfig 描述同一动作的入口、确认和取消控件，确认框按提示文字隔离。
type contactRequestConfig struct {
	Button  platformcore.SelectorSpec `json:"button"`
	Confirm platformcore.SelectorSpec `json:"confirm"`
	Cancel  platformcore.SelectorSpec `json:"cancel"`
}

// contactSettings 返回动作专属控件；简历复用现有已验证的索要入口，不使用通用确定按钮。
func (r *Runtime) contactSettings(action string) (contactRequestConfig, error) {
	cfg := r.replyPageSettings()
	if action == "resume" {
		return contactRequestConfig{Button: cfg.ResumeButton, Confirm: cfg.ResumeConfirm, Cancel: platformcore.SelectorSpec{Selectors: []string{`.exchange-tooltip:visible:has(> .text:has-text("确定向牛人索取简历吗？")) > .btn-box > .boss-btn-outline.boss-btn:visible`}, Text: "取消"}}, nil
	}
	value, ok := cfg.ContactRequests[action]
	if !ok || len(value.Button.Selectors) == 0 || len(value.Confirm.Selectors) == 0 || len(value.Cancel.Selectors) == 0 {
		return contactRequestConfig{}, fmt.Errorf("缺少%s索要控件配置", platformcore.CandidateInfoActionLabel(action))
	}
	return value, nil
}

// contactRequestObserved 只认可我方或平台消息中的相关请求事实，取消和失败提示不能作为成功证据。
func contactRequestObserved(action string, current platformcore.ReplyContext) bool {
	if action == "resume" {
		return current.ResumeStatus == "requested" || current.ResumeStatus == "received"
	}
	for _, message := range current.Messages {
		if message.Direction != "outbound" && message.Direction != "system" {
			continue
		}
		text := message.Text
		if strings.Contains(text, "失败") || strings.Contains(text, "取消") || strings.Contains(text, "拒绝") || strings.Contains(text, "未发送") {
			continue
		}
		topic := action == "phone" && (strings.Contains(text, "交换手机") || strings.Contains(text, "交换电话")) || action == "wechat" && strings.Contains(text, "交换微信")
		if topic && (strings.Contains(text, "请求") || strings.Contains(text, "申请") || strings.Contains(text, "我想") || strings.Contains(text, "已交换")) {
			return true
		}
	}
	return false
}

// InspectCandidateInfoRequest 只读取当前面板，用于确认既有请求及未知结果，不点击任何控件。
func (r *Runtime) InspectCandidateInfoRequest(ctx context.Context, exec platformcore.Executor, target platformcore.ReplyTarget, conversation platformcore.ReplyConversation, action string) (bool, error) {
	current, err := r.ReadOpenedReplyContext(ctx, exec, target, conversation)
	if err != nil {
		return false, err
	}
	return contactRequestObserved(action, current), nil
}

// PrepareCandidateInfoRequest 核对身份、空草稿和未打开的确认框，再点击动作专属入口并等待确认框。
func (r *Runtime) PrepareCandidateInfoRequest(ctx context.Context, exec platformcore.Executor, target platformcore.ReplyTarget, conversation platformcore.ReplyConversation, action string) (prepared platformcore.CandidateInfoPreparation, resultErr error) {
	prepared.Action = action
	settings, err := r.contactSettings(action)
	if err != nil {
		return prepared, err
	}
	current, err := r.ReadOpenedReplyContext(ctx, exec, target, conversation)
	if err != nil {
		return prepared, err
	}
	prepared.Before = current
	if contactRequestObserved(action, current) {
		prepared.AlreadyDone = true
		return prepared, nil
	}
	if strings.TrimSpace(current.Draft) != "" {
		return prepared, platformcore.ErrReplyUnsafe
	}
	cfg := r.replyPageSettings()
	popup := replyChildSelector(platformcore.SelectorSpec{Selectors: []string{".exchange-tooltip:visible"}}, cfg.Active)
	if opened, err := r.hasElement(ctx, exec, popup); err != nil || opened {
		return prepared, fmt.Errorf("已有确认框或无法核对状态，暂停%s索要", platformcore.CandidateInfoActionLabel(action))
	}
	button := replyChildSelector(settings.Button, cfg.Active)
	if available, err := r.hasElement(ctx, exec, button); err != nil || !available {
		return prepared, fmt.Errorf("%s按钮尚不可用，保留待处理", platformcore.CandidateInfoActionLabel(action))
	}
	// 入口只打开确认框；任何失败都尝试取消本次打开的同动作确认框。
	defer func() {
		if resultErr != nil {
			cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
			defer cancel()
			_ = r.CancelCandidateInfoRequest(cleanup, exec, target, conversation, prepared)
		}
	}()
	if _, err := exec.Post(ctx, "/api/v1/page/click", platformcore.LocatorRequest{Selector: button}); err != nil {
		return prepared, err
	}
	if err := r.waitReplyElementState(ctx, exec, replyChildSelector(settings.Confirm, cfg.Active), true, "索要"+platformcore.CandidateInfoActionLabel(action)+"确认框"); err != nil {
		return prepared, err
	}
	return prepared, nil
}

// SubmitCandidateInfoRequest 在公共流程已登记 sending 后核对上下文，只点击一次当前动作确认并等待请求证据。
func (r *Runtime) SubmitCandidateInfoRequest(ctx context.Context, exec platformcore.Executor, target platformcore.ReplyTarget, conversation platformcore.ReplyConversation, prepared platformcore.CandidateInfoPreparation) (string, error) {
	settings, err := r.contactSettings(prepared.Action)
	if err != nil {
		return "unknown", err
	}
	current, err := r.ReadOpenedReplyContext(ctx, exec, target, conversation)
	if err != nil {
		return "unknown", err
	}
	if !reflect.DeepEqual(current.Messages, prepared.Before.Messages) || strings.TrimSpace(current.Draft) != "" {
		return "unknown", platformcore.ErrReplyUnsafe
	}
	if contactRequestObserved(prepared.Action, current) {
		return "satisfied", nil
	}
	confirm := replyChildSelector(settings.Confirm, r.replyPageSettings().Active)
	if _, err := exec.Post(ctx, "/api/v1/page/click", platformcore.LocatorRequest{Selector: confirm}); err != nil {
		return "unknown", err
	}
	for attempt := 0; attempt < 20; attempt++ {
		current, err := r.ReadOpenedReplyContext(ctx, exec, target, conversation)
		if err != nil {
			return "unknown", err
		}
		opened, err := r.hasElement(ctx, exec, confirm)
		if err != nil {
			return "unknown", err
		}
		if !opened && contactRequestObserved(prepared.Action, current) {
			return "requested", nil
		}
		if err := exec.Delay(ctx, "等待索要请求结果", 0.25); err != nil {
			return "unknown", err
		}
	}
	return "unknown", fmt.Errorf("%s已点击确认，但结果未确认，不重复发送", platformcore.CandidateInfoActionLabel(prepared.Action))
}

// CancelCandidateInfoRequest 只取消仍属于同一候选人的本次确认框，不触碰用户切换后的其他会话。
func (r *Runtime) CancelCandidateInfoRequest(ctx context.Context, exec platformcore.Executor, target platformcore.ReplyTarget, conversation platformcore.ReplyConversation, prepared platformcore.CandidateInfoPreparation) error {
	if _, err := r.ReadOpenedReplyContext(ctx, exec, target, conversation); err != nil {
		return err
	}
	settings, err := r.contactSettings(prepared.Action)
	if err != nil {
		return err
	}
	cancelButton := replyChildSelector(settings.Cancel, r.replyPageSettings().Active)
	if opened, err := r.hasElement(ctx, exec, cancelButton); err != nil {
		return err
	} else if !opened {
		return nil
	}
	_, err = exec.Post(ctx, "/api/v1/page/click", platformcore.LocatorRequest{Selector: cancelButton})
	return err
}
