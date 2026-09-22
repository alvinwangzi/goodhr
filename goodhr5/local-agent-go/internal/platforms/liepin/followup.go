// Package liepin 文件作用：提供猎聘企业端基础筛选入口和打招呼后的候选人聊天框索要信息流程。
// 猎聘企业端聊天框与联系人列表同为 IM 组件体系，索要流程优先复用打招呼打开的聊天框，
// 未打开时通过候选人卡片内的“继续沟通”按钮打开。
package liepin

import (
	"context"
	"fmt"
	"strings"
	"time"

	"goodhr5/local-agent-go/internal/cloudapi"
	"goodhr5/local-agent-go/internal/platformcore"
	"goodhr5/local-agent-go/internal/platforms/chatflow"
)

// liepinConversationFlow 描述猎聘企业端候选人聊天框的稳定选择器。
var liepinConversationFlow = chatflow.Flow{
	Label:     "猎聘",
	ChatModal: ".im-ui-basic-chat-modal",
	ChatName:  ".im-ui-basic-chat-header-name",
	ChatClose: ".im-ui-basic-chat-header-modal-close",
	Input:     ".im-ui-basic-chat-modal textarea[placeholder='请输入文字，按Enter键发送']",
}

// liepinConversationRequestButtons 描述猎聘企业端三类索要动作的入口按钮和确认弹框选择器。
var liepinConversationRequestButtons = chatflow.RequestButtons{
	Phone:  chatflow.RequestButton{Label: "手机号", Selector: ".im-ui-action-button.action-item.action-phone", ConfirmDialog: ".ant-im-modal.ant-im-modal-confirm", ConfirmButton: ".ant-im-modal-confirm-btns .ant-im-btn-primary"},
	Wechat: chatflow.RequestButton{Label: "微信", Selector: ".im-ui-action-button.action-item.action-wechat", ConfirmDialog: ".ant-im-modal.ant-im-modal-confirm", ConfirmButton: ".ant-im-modal-confirm-btns .ant-im-btn-primary"},
	Resume: chatflow.RequestButton{Label: "简历", Selector: ".im-ui-action-button.action-item.action-resume", ConfirmDialog: ".ant-im-modal.ant-im-modal-confirm", ConfirmButton: ".ant-im-modal-confirm-btns .ant-im-btn-primary"},
}

const (
	// liepinContinueButtonSelector 是猎聘企业端候选人卡片“继续沟通”按钮的稳定埋点选择器，
	// 推荐标签和最新标签两张卡片埋点不同，用 CSS 多候选一次覆盖。
	liepinContinueButtonSelector = "[data-tlg-elem-id='b_pc_home_hp_res_listcard_chat_btn'], [data-tlg-elem-id='b_pc_home_new_res_listcard_chat_btn']"
	// liepinConversationReusePollCount 是复用打招呼已打开聊天框的短轮询轮数。
	liepinConversationReusePollCount = 15
	// liepinConversationReusePollInterval 是复用短轮询的间隔秒数。
	liepinConversationReusePollInterval = 0.1
	// liepinConversationOpenPollCount 是主动打开聊天框后的等待轮数。
	liepinConversationOpenPollCount = 50
	// liepinConversationOpenPollInterval 是主动打开等待的间隔秒数。
	liepinConversationOpenPollInterval = 0.2
)

// ApplyBasicFilters 保留猎聘企业端基础筛选入口，当前不执行页面操作。
func (r *Runtime) ApplyBasicFilters(context.Context, platformcore.Executor, cloudapi.PlatformConfig, map[string]any) error {
	return nil
}

// RequestCandidateInfo 在已确认身份的猎聘聊天框内按岗位配置索要信息并发送追加问候语。
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
		if err := chatflow.CloseConversation(cleanupCtx, exec, liepinConversationFlow); err != nil {
			exec.Log("warning", "猎聘索要信息：候选人="+name+"，聊天框收尾失败，错误="+err.Error())
			if resultErr == nil {
				resultErr = err
			}
		}
	}()
	// 打招呼后聊天框通常会自动打开，先短暂轮询复用，避免重复点击继续沟通。
	matched, opened, err := chatflow.AwaitConversation(ctx, exec, liepinConversationFlow, name, liepinConversationReusePollCount, liepinConversationReusePollInterval)
	if err != nil {
		return err
	}
	if matched {
		exec.Log("info", "猎聘索要信息：候选人="+name+"，复用打招呼已打开的聊天框")
	} else {
		if opened {
			if err := chatflow.CloseConversation(ctx, exec, liepinConversationFlow); err != nil {
				return fmt.Errorf("关闭猎聘其他候选人的聊天框失败：%w", err)
			}
		}
		if err := openLiepinConversation(ctx, exec, cfg, candidate); err != nil {
			return err
		}
		matched, _, err = chatflow.AwaitConversation(ctx, exec, liepinConversationFlow, name, liepinConversationOpenPollCount, liepinConversationOpenPollInterval)
		if err != nil {
			return err
		}
		if !matched {
			return fmt.Errorf("猎聘聊天框没有在等待时间内确认到当前候选人：%s", name)
		}
	}
	if err := chatflow.RequestInfo(ctx, exec, liepinConversationFlow, liepinConversationRequestButtons, request); err != nil {
		return err
	}
	if message != "" {
		exec.Log("info", "猎聘索要信息：准备发送岗位追加问候语")
		if err := chatflow.SendMessage(ctx, exec, liepinConversationFlow, message); err != nil {
			return err
		}
	}
	return nil
}

// openLiepinConversation 点击当前候选人卡片内的“继续沟通”按钮打开聊天框。
// 优先使用云端配置的继续沟通按钮选择器，未配置时回退到稳定埋点常量。
// ctx 为运行上下文，exec 为执行器，cfg 为平台配置，candidate 为候选人。
func openLiepinConversation(ctx context.Context, exec platformcore.Executor, cfg cloudapi.PlatformConfig, candidate platformcore.Candidate) error {
	item := platformElement(cfg, "card", "item")
	if item == nil {
		return fmt.Errorf("平台配置中无候选人卡片选择器")
	}
	clickTarget := platformElement(cfg, "actions", "continueBtn")
	if clickTarget == nil {
		clickTarget = map[string]any{"selector": liepinContinueButtonSelector}
	}
	if _, err := exec.Post(ctx, "/api/v1/page/list-click-by-index", map[string]any{
		"index":       intFromMap(candidate, "card_index"),
		"item":        item,
		"clickTarget": clickTarget,
		"timeout":     10000,
	}); err != nil {
		return fmt.Errorf("点击猎聘“继续沟通”失败：%w", err)
	}
	return nil
}
