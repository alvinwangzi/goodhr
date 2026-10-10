// 本文件编排三项独立索要：勾选过滤、身份核对、发送意图、结果保存和云端补报；平台仅执行确认控件操作。
package positionrunner

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"goodhr5/local-agent-go/internal/actiondispatch"
	"goodhr5/local-agent-go/internal/cloudapi"
	"goodhr5/local-agent-go/internal/localdb"
	"goodhr5/local-agent-go/internal/platformcore"
)

// syncCandidateInfoFeedback 只重试结果记录，不重新发送请求；结果有新修订时才再次补报。
func (r *Runner) syncCandidateInfoFeedback(ctx context.Context, position localdb.Position, options StartOptions, item localdb.CandidateInfoRequest, action, state string) {
	if state == "" || state == "sending" || item.Synced[action] == state {
		return
	}
	base := strings.TrimSpace(options.CloudAPIBase)
	if base == "" {
		base = strings.TrimSpace(r.cloudAPIBase)
	}
	if base == "" || options.Token == "" {
		return
	}
	syncCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	err := cloudapi.New(base).NotifyCandidateInfoResults(syncCtx, options.Token, position.ID, options.CloudRunID, []cloudapi.CandidateInfoFeedback{{RequestID: item.ID, CandidateID: item.CandidateID, CandidateName: item.CandidateName, Action: action, State: state}})
	if err != nil {
		r.positionLog(position.ID, "warning", "索要结果保留待补报："+item.CandidateName+"，"+action+"，"+err.Error())
		return
	}
	if err := r.db.MarkCandidateInfoSynced(item.ID, action, state); err != nil {
		r.positionLog(position.ID, "warning", "索要同步确认保存失败："+err.Error())
	}
}

// performCandidateInfoChecks 处理稳定 ID 名单，只执行本轮和入队时共同勾选的动作，成功和未知均不重发。
func (r *Runner) performCandidateInfoChecks(ctx context.Context, position localdb.Position, runtime platformcore.Runtime, options StartOptions) {
	if options.candidateInfoRemaining != nil {
		*options.candidateInfoRemaining = nil
	}
	items, err := r.db.ListCandidateInfoRequests(position.ID)
	if err != nil {
		r.positionLog(position.ID, "warning", "读取索要意图失败："+err.Error())
		return
	}
	if len(items) == 0 {
		return
	}
	selected := candidateInfoRequestFromPosition(position)
	if options.candidateInfoBatchIDs != nil {
		selectedIDs := map[string]bool{}
		for _, id := range options.candidateInfoBatchIDs {
			selectedIDs[id] = true
		}
		filtered := []localdb.CandidateInfoRequest{}
		for _, item := range items {
			if selectedIDs[item.ID] {
				filtered = append(filtered, item)
			}
		}
		items = filtered
	}
	wanted := map[string]bool{"phone": selected.RequestPhone, "wechat": selected.RequestWechat, "resume": selected.RequestResume}
	for _, item := range items {
		for action, state := range item.Results {
			r.syncCandidateInfoFeedback(ctx, position, options, item, action, state)
		}
	}
	operator, ok := runtime.(platformcore.CandidateInfoRequestOperator)
	if !ok {
		r.positionLog(position.ID, "info", "当前平台未提供分项确认能力，三项名单保留，不改成求简历")
		return
	}
	pageRuntime, ok := runtime.(platformcore.AutoReplyRuntime)
	if !ok {
		return
	}
	locator, ok := runtime.(platformcore.ReGreetRuntime)
	if !ok {
		return
	}
	base := strings.TrimSpace(options.CloudAPIBase)
	if base == "" {
		base = strings.TrimSpace(r.cloudAPIBase)
	}
	if base == "" || options.Token == "" {
		r.positionLog(position.ID, "warning", "索要缺少会员校验凭证，未执行")
		return
	}
	subscription, err := cloudapi.New(base).FetchSubscription(ctx, options.Token)
	if err != nil || !boolFromMap(subscription, "allow_auto_reply") {
		r.positionLog(position.ID, "warning", "当前没有 Pro 索要权限，名单保留，未执行")
		return
	}
	hasWork := false
	names := map[string]int{}
	for _, item := range items {
		names[item.CandidateName]++
		for action, enabled := range item.Actions {
			if enabled && wanted[action] && item.Results[action] != "requested" && item.Results[action] != "satisfied" {
				hasWork = true
			}
		}
	}
	if !hasWork {
		return
	}
	exec := platformExecutor{runner: r, positionID: position.ID, once: true}
	target, err := ensureReplyTarget(ctx, pageRuntime, exec, positionPositionName(position), options.preparedReplyTarget)
	if err != nil {
		r.positionLog(position.ID, "warning", "索要岗位核对失败："+err.Error())
		return
	}
	now := time.Now
	if options.actionNow != nil {
		now = options.actionNow
	}
	batchStarted := now()
	if options.candidateInfoRemaining != nil {
		for _, item := range items {
			*options.candidateInfoRemaining = append(*options.candidateInfoRemaining, item.ID)
		}
	}
	for index, item := range items {
		if options.candidateInfoRemaining != nil && actiondispatch.BatchLimit(batchStarted, now(), index) {
			return
		}
		if ctx.Err() != nil || r.isUserStopped(position.ID) {
			return
		}
		if options.candidateInfoRemaining != nil {
			remaining := []string{}
			for _, item := range items[index+1:] {
				remaining = append(remaining, item.ID)
			}
			*options.candidateInfoRemaining = remaining
		}
		if options.LocalRunID == "" && names[item.CandidateName] > 1 {
			r.positionLog(position.ID, "warning", "索要名单有多个同名 ID，未自动交换："+item.CandidateName)
			continue
		}
		active := false
		for action, enabled := range item.Actions {
			if enabled && wanted[action] && item.Results[action] != "requested" && item.Results[action] != "satisfied" {
				active = true
			}
		}
		if !active {
			continue
		}
		delayErr := exec.Delay(ctx, "查看下一位索要候选人前", randomFloatRange(1, 2))
		if delayErr != nil || ctx.Err() != nil || r.isUserStopped(position.ID) || (options.candidateInfoRemaining != nil && actiondispatch.BatchLimit(batchStarted, now(), index)) {
			if options.candidateInfoRemaining != nil {
				*options.candidateInfoRemaining = append([]string{item.ID}, *options.candidateInfoRemaining...)
			}
			return
		}
		var conversation platformcore.ReplyConversation
		var err error
		if options.LocalRunID != "" {
			scope := platformcore.ReplyHash("profile:" + positionProfileName(position))
			checkpoint, checkpointErr := r.db.LoadActionCheckpoint(ctx, options.LocalRunID)
			if checkpointErr != nil && !errors.Is(checkpointErr, sql.ErrNoRows) {
				r.positionLog(position.ID, "warning", "索要账号检查点读取失败")
				continue
			}
			if _, requiresProof := runtime.(platformcore.AccountIdentityRuntime); requiresProof && (checkpointErr != nil || !checkpoint.AccountBound) {
				r.positionLog(position.ID, "warning", "索要账号尚未核对，名单保留")
				continue
			}
			if checkpoint.AccountBound {
				scope = checkpoint.ProfileScope
			}
			identity, lookupErr := r.verifiedCandidateIdentity(ctx, exec, runtime, scope, position.PlatformID, item.CandidateID, item.CandidateName)
			identityLocator, supported := runtime.(platformcore.IdentityConversationLocator)
			if lookupErr != nil || identity.Status != "verified" || !supported {
				r.positionLog(position.ID, "warning", "索要身份尚未核对，名单保留："+item.CandidateName)
				continue
			}
			conversation, err = identityLocator.LocateReplyConversationByID(ctx, exec, item.CandidateName, identity.ConversationID)
		} else {
			conversation, err = locator.LocateReplyConversation(ctx, exec, item.CandidateName)
		}
		if err != nil {
			r.positionLog(position.ID, "warning", "索要候选人定位失败："+err.Error())
			continue
		}
		current, err := locator.ReadOpenedReplyContext(ctx, exec, target, conversation)
		if err != nil {
			r.positionLog(position.ID, "warning", "索要会话核对失败："+err.Error())
			continue
		}
		if err := r.reconcileCandidatePageState(ctx, position, options, item.CandidateID, item.CandidateName, candidateStateFromReply(current)); err != nil {
			r.positionLog(position.ID, "warning", "索要前页面状态暂未同步："+err.Error())
		}
		replied := false
		for _, message := range current.Messages {
			if message.Direction == "inbound" {
				replied = true
			}
		}
		waitingActions := []string{}
		if !replied {
			for _, action := range []string{"phone", "wechat", "resume"} {
				if item.Actions[action] && wanted[action] && item.Results[action] != "requested" && item.Results[action] != "satisfied" && item.Results[action] != "unknown" && item.Results[action] != "sending" {
					waitingActions = append(waitingActions, platformcore.CandidateInfoActionLabel(action))
				}
			}
			if len(waitingActions) > 0 {
				r.positionLog(position.ID, "info", "索要继续等待候选人回复："+item.CandidateName+"，暂未执行："+strings.Join(waitingActions, "、"))
			}
		}
		for _, action := range []string{"phone", "wechat", "resume"} {
			if !item.Actions[action] || !wanted[action] || item.Results[action] == "requested" || item.Results[action] == "satisfied" {
				continue
			}
			if item.Results[action] == "unknown" || item.Results[action] == "sending" {
				confirmed, err := operator.InspectCandidateInfoRequest(ctx, exec, target, conversation, action)
				if err == nil && confirmed {
					if err := r.db.SaveCandidateInfoResult(item.ID, action, "satisfied"); err == nil {
						r.syncCandidateInfoFeedback(ctx, position, options, item, action, "satisfied")
					}
				}
				continue // 未确认的历史发送只检查证据，不再点击。
			}
			if !replied {
				continue
			}
			if err := exec.Delay(ctx, "索要"+platformcore.CandidateInfoActionLabel(action)+"操作前", randomFloatRange(1, 2)); err != nil {
				return
			}
			if ctx.Err() != nil || r.isUserStopped(position.ID) {
				return
			}
			prepared, err := operator.PrepareCandidateInfoRequest(ctx, exec, target, conversation, action)
			if err != nil {
				r.positionLog(position.ID, "warning", fmt.Sprintf("索要%s保留待处理：%s，%v", platformcore.CandidateInfoActionLabel(action), item.CandidateName, err))
				continue
			}
			if prepared.AlreadyDone {
				if err := r.db.SaveCandidateInfoResult(item.ID, action, "satisfied"); err == nil {
					r.syncCandidateInfoFeedback(ctx, position, options, item, action, "satisfied")
				}
				continue
			}
			var claimErr error
			if options.LocalRunID != "" {
				claimErr = r.db.ClaimCandidateInfoForRun(ctx, item.ID, action, options.LocalRunID)
			} else {
				claimErr = r.db.ClaimCandidateInfoAction(item.ID, action)
			}
			if claimErr != nil {
				_ = operator.CancelCandidateInfoRequest(ctx, exec, target, conversation, prepared)
				continue
			}
			submitState, sendErr := operator.SubmitCandidateInfoRequest(ctx, exec, target, conversation, prepared)
			state := "unknown"
			if sendErr == nil && (submitState == "requested" || submitState == "satisfied") {
				state = submitState
			}
			cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
			_ = operator.CancelCandidateInfoRequest(cleanup, exec, target, conversation, prepared)
			if err := r.db.SaveCandidateInfoResult(item.ID, action, state); err != nil {
				r.positionLog(position.ID, "warning", "索要发送状态保存失败，停止后续索要："+err.Error())
				cancel()
				return
			}
			r.syncCandidateInfoFeedback(cleanup, position, options, item, action, state)
			cancel()
			r.positionLog(position.ID, "info", fmt.Sprintf("索要%s结果：候选人=%s，状态=%s，错误=%v", platformcore.CandidateInfoActionLabel(action), item.CandidateName, candidateInfoStateLabel(state), sendErr))
		}
	}
}

// candidateInfoStateLabel 展示确认结果，不把未知发送当成成功。
func candidateInfoStateLabel(state string) string {
	switch state {
	case "requested":
		return "已确认请求发出"
	case "satisfied":
		return "已有请求或已获取，已跳过"
	default:
		return "结果未确认，不会自动重发"
	}
}
