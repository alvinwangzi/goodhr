// 本文件编排 HRPlus 首次招呼后的追加问候，复用真实会话 ID、发送意图和确认能力，不使用全局弹窗的姓名作为身份。
package positionrunner

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"goodhr5/local-agent-go/internal/cloudapi"
	"goodhr5/local-agent-go/internal/localdb"
	"goodhr5/local-agent-go/internal/platformcore"
	"strings"
	"time"
)

// sendVerifiedGreetFollowup 在同一通道内完成切换、ID 定位、持久发送与确认，最后恢复推荐页面。
func (r *Runner) sendVerifiedGreetFollowup(ctx context.Context, position localdb.Position, options StartOptions, runtime platformcore.Runtime, exec platformExecutor, cfg cloudapi.PlatformConfig, candidate map[string]any, message string) (resultErr error) {
	preparer, pok := runtime.(platformcore.CandidateFollowupPagePreparer)
	page, rok := runtime.(platformcore.AutoReplyRuntime)
	sender, sok := runtime.(platformcore.ReGreetRuntime)
	resumer, cok := runtime.(platformcore.RecommendationResumer)
	locator, lok := runtime.(platformcore.IdentityConversationLocator)
	if !pok || !rok || !sok || !cok || !lok {
		return fmt.Errorf("平台尚未支持真实 ID 追加问候，保留待核对")
	}
	checkpoint, err := r.db.LoadActionCheckpoint(ctx, options.LocalRunID)
	if err != nil {
		return err
	}
	if !checkpoint.AccountBound {
		return fmt.Errorf("追加问候账号尚未核对")
	}
	id := stringFromMap(candidate, "id")
	name := candidateLogName(candidate)
	anchors := appendCompletedAnchor(checkpoint.Anchors, id)
	cursor, captureErr := resumer.CaptureRecommendationCursor(ctx, exec, anchors)
	if captureErr != nil {
		cursor = platformcore.RecommendationCursor{Reason: "followup_anchor_unavailable"}
	}
	entered := false
	defer func() {
		if !entered {
			return
		}
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
		defer cancel()
		if err := resumer.ReturnToRecommendation(cleanup, exec); err != nil {
			if resultErr == nil {
				resultErr = err
			}
			return
		}
		matched, _, err := resumer.CheckRecommendationCursor(cleanup, exec, cursor)
		if err != nil || !matched {
			if err := resumer.RewindRecommendation(cleanup, exec); err != nil && resultErr == nil {
				resultErr = err
			}
		}
	}()
	if err := preparer.PrepareCandidateFollowup(ctx, exec); err != nil {
		return err
	}
	entered = true
	target, err := page.ResolveReplyTarget(ctx, exec, positionPositionName(position))
	if err != nil {
		return err
	}
	identity, err := r.verifiedCandidateIdentity(ctx, exec, runtime, checkpoint.ProfileScope, position.PlatformID, id, name)
	if err != nil {
		return err
	}
	conversation, err := locator.LocateReplyConversationByID(ctx, exec, name, identity.ConversationID)
	if err != nil {
		return err
	}
	before, err := sender.ReadOpenedReplyContext(ctx, exec, target, conversation)
	if err != nil {
		return err
	}
	if strings.TrimSpace(before.Draft) != "" {
		return platformcore.ErrReplyUnsafe
	}
	raw, _ := json.Marshal(before.Messages)
	key := localdb.AutoReplyRecord{ProfileScope: checkpoint.ProfileScope, Platform: position.PlatformID, ConversationID: "post_greet:" + conversation.ID, InboundFingerprint: platformcore.ReplyHash(position.ID + ":" + id), PositionID: position.ID, RunID: options.CloudRunID, ContextFingerprint: platformcore.ReplyHash(string(raw)), ReplyFingerprint: platformcore.ReplyHash(message)}
	existing, lookupErr := r.db.FindAutoReply(ctx, key)
	if lookupErr != nil && !errors.Is(lookupErr, sql.ErrNoRows) {
		return lookupErr
	}
	if lookupErr == nil && (existing.Status == "sent" || existing.Status == "sending" || existing.Status == "unknown") {
		return fmt.Errorf("追加问候已有发送或待核对记录，不重复发送")
	}
	record, err := r.db.PrepareAutoReply(ctx, key)
	if err != nil {
		return err
	}
	var stageErr error
	if stager, supported := runtime.(platformcore.CandidateFollowupMessageStager); supported {
		stageErr = stager.StageCandidateFollowup(ctx, exec, target, conversation, before, message)
	} else {
		stageErr = sender.StageReGreet(ctx, exec, target, conversation, before, message)
	}
	if stageErr != nil {
		return stageErr
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := r.db.TransitionAutoReply(ctx, record.ID, "prepared", "sending", ""); err != nil {
		return err
	}
	if err := sender.SendReGreet(ctx, exec, target, conversation, before, message); err != nil {
		_ = r.db.TransitionAutoReply(context.WithoutCancel(ctx), record.ID, "sending", "unknown", "followup_send_unknown")
		return err
	}
	confirmed, err := sender.ConfirmReGreet(ctx, exec, target, conversation, before, message)
	if err != nil || !confirmed {
		_ = r.db.TransitionAutoReply(context.WithoutCancel(ctx), record.ID, "sending", "unknown", "followup_unconfirmed")
		return fmt.Errorf("追加问候发送结果待核对")
	}
	return r.db.TransitionAutoReply(context.WithoutCancel(ctx), record.ID, "sending", "sent", "")
}
