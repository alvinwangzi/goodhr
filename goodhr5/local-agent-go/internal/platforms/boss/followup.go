// Package boss 文件作用：提供 Boss 基础筛选入口和打招呼后的候选人聊天框索要信息流程。
// Boss 打招呼后聊天框会自动打开，索要流程优先复用该聊天框，未打开时再通过候选人定位接口打开。
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

// bossConversationFlow 描述 Boss 候选人全局聊天框的稳定选择器。
var bossConversationFlow = chatflow.Flow{
	Label:      "Boss",
	ChatModal:  ".chat-global-conversation",
	ChatName:   ".chatview-name",
	ChatClose:  ".iboss-close",
	Input:      ".bosschat-chat-input.chat-global-message.boss-chat-editor-input[contenteditable='true']",
	SendButton: ".btn-send:not(.btn-disabled)",
}

// bossConversationRequestButtons 描述 Boss 三类索要动作的入口按钮和确认浮层选择器。
var bossConversationRequestButtons = chatflow.RequestButtons{
	Phone:  chatflow.RequestButton{Label: "手机号", Selector: ".iboss-phone.btn-bar:not(.disabled)", ConfirmDialog: ".boss-btn-primary", ConfirmButton: ".boss-btn-primary"},
	Wechat: chatflow.RequestButton{Label: "微信", Selector: ".iboss-wechat.btn-bar:not(.disabled)", ConfirmDialog: ".boss-btn-primary", ConfirmButton: ".boss-btn-primary"},
	Resume: chatflow.RequestButton{Label: "简历", Selector: ".iboss-editor-resume.btn-bar:not(.disabled)", ConfirmDialog: ".boss-btn-primary", ConfirmButton: ".boss-btn-primary"},
}

const (
	bossConversationReusePollCount    = 30   // 复用打招呼已打开聊天框的短轮询轮数；Boss 聊天框姓名渲染较慢，需留足等待
	bossConversationReusePollInterval = 0.2  // 复用短轮询的间隔秒数
	bossConversationOpenPollCount     = 50   // 主动打开聊天框后的等待轮数
	bossConversationOpenPollInterval  = 0.2  // 主动打开等待的间隔秒数
)

// ApplyBasicFilters 保留 Boss 基础筛选入口，当前不执行页面操作。
func (r *Runtime) ApplyBasicFilters(context.Context, platformcore.Executor, cloudapi.PlatformConfig, map[string]any) error {
	return nil
}

// RequestCandidateInfo 在已确认身份的 Boss 聊天框内按岗位配置索要信息并发送追加问候语。
// ctx 为运行上下文，exec 为执行器，cfg 为平台配置，candidate 为候选人，request 为岗位索要配置。
func (r *Runtime) RequestCandidateInfo(ctx context.Context, exec platformcore.Executor, cfg cloudapi.PlatformConfig, candidate platformcore.Candidate, request platformcore.CandidateInfoRequest) (resultErr error) {
	message := strings.TrimSpace(request.GreetMessage)
	if !request.RequestPhone && !request.RequestWechat && !request.RequestResume && message == "" {
		return nil
	}
	name := candidateName(candidate)
	interactionStarted := true
	defer func() {
		if !interactionStarted {
			return
		}
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 8*time.Second)
		defer cancel()
		if err := chatflow.CloseConversation(cleanupCtx, exec, bossConversationFlow); err != nil {
			exec.Log("warning", "Boss索要信息：候选人="+name+"，聊天框收尾失败，错误="+err.Error())
			if resultErr == nil {
				resultErr = err
			}
		}
	}()
	// 打招呼后聊天框通常会自动打开，先短暂轮询复用，避免重复点击继续沟通。
	matched, opened, err := chatflow.AwaitConversation(ctx, exec, bossConversationFlow, name, bossConversationReusePollCount, bossConversationReusePollInterval)
	if err != nil {
		return err
	}
	if matched {
		exec.Log("info", "Boss索要信息：候选人="+name+"，复用打招呼已打开的聊天框")
	} else {
		if opened {
			if err := chatflow.CloseConversation(ctx, exec, bossConversationFlow); err != nil {
				return fmt.Errorf("关闭 Boss 其他候选人的聊天框失败：%w", err)
			}
		}
		if err := r.openConversation(ctx, exec, cfg, candidate); err != nil {
			return err
		}
		matched, _, err = chatflow.AwaitConversation(ctx, exec, bossConversationFlow, name, bossConversationOpenPollCount, bossConversationOpenPollInterval)
		if err != nil {
			return err
		}
		if !matched {
			return fmt.Errorf("Boss聊天框没有在等待时间内确认到当前候选人：%s", name)
		}
	}
	if err := chatflow.RequestInfo(ctx, exec, bossConversationFlow, bossConversationRequestButtons, request); err != nil {
		return err
	}
	if message != "" {
		exec.Log("info", "Boss索要信息：准备发送岗位追加问候语")
		if err := chatflow.SendMessage(ctx, exec, bossConversationFlow, message); err != nil {
			return err
		}
	}
	return nil
}

// openConversation 通过候选人定位接口把当前候选人卡片滚动到可见，并点击打招呼或继续沟通入口打开聊天框。
// 已打过招呼的候选人卡片上没有打招呼按钮，必须兼容继续沟通入口。
// ctx 为运行上下文，exec 为执行器，cfg 为平台配置，candidate 为候选人。
func (r *Runtime) openConversation(ctx context.Context, exec platformcore.Executor, cfg cloudapi.PlatformConfig, candidate platformcore.Candidate) error {
	payload := bossCandidateVisiblePayload(cfg, candidate)
	payload["debug_stage"] = "request-info-open-conversation"
	if _, err := exec.Post(ctx, "/api/v1/boss/candidates/visible", payload); err != nil {
		return fmt.Errorf("定位 Boss 候选人失败：%w", err)
	}
	if _, err := exec.Post(ctx, "/api/v1/boss/candidates/open-chat", payload); err != nil {
		return fmt.Errorf("打开 Boss 候选人聊天框失败：%w", err)
	}
	return nil
}
