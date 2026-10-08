// 本文件按预期真实会话 ID 定位 Boss 候选人，姓名仅用于辅助核对，禁止猜测跨入口映射。
package boss

import (
	"context"
	"fmt"
	"goodhr5/local-agent-go/internal/platformcore"
	"strings"
)

// ObserveAccountIdentity 读取正常加载响应中的登录用户 ID，缺失时不得用显示姓名或目录名代替。
func (r *Runtime) ObserveAccountIdentity(ctx context.Context, exec platformcore.Executor, refreshIfMissing bool) (string, error) {
	result, err := exec.Post(ctx, "/api/v1/boss/account/identity", map[string]any{"refresh_if_missing": refreshIfMissing})
	if err != nil {
		return "", err
	}
	data := workerDataMap(result)
	if !boolFromMap(data, "verified") || stringFromMap(data, "account_id") == "" {
		return "", fmt.Errorf("无法核对 Boss 登录账号，请确认已登录后重新开始")
	}
	return stringFromMap(data, "account_id"), nil
}

// ResolveCandidateConversationID 只接受被动响应成对 ID 与当前真实聊天行的联合核对结果。
func (r *Runtime) ResolveCandidateConversationID(ctx context.Context, exec platformcore.Executor, recommendationID, candidateName string) (string, string, error) {
	result, err := exec.Post(ctx, "/api/v1/boss/candidates/identity", map[string]any{"recommendation_id": recommendationID, "candidate_name": candidateName})
	if err != nil {
		return "", "", err
	}
	data := workerDataMap(result)
	id, source := stringFromMap(data, "conversation_id"), stringFromMap(data, "source")
	if !boolFromMap(data, "verified") || stringFromMap(data, "recommendation_id") != recommendationID || id == "" || source == "" {
		return "", "", platformcore.ErrReplyUnsafe
	}
	return id, source, nil
}

// LocateReplyConversationByID 使用已有聊天搜索接口定位指定 ID，返回前再次独立核对唯一选中项。
func (r *Runtime) LocateReplyConversationByID(ctx context.Context, exec platformcore.Executor, name, expectedID string) (platformcore.ReplyConversation, error) {
	if strings.TrimSpace(name) == "" || strings.TrimSpace(expectedID) == "" {
		return platformcore.ReplyConversation{}, platformcore.ErrReplyUnsafe
	}
	if err := r.AutoReplyAvailable(); err != nil {
		return platformcore.ReplyConversation{}, err
	}
	result, err := exec.Post(ctx, "/api/v1/boss/chat/search-session", map[string]any{"candidate_name": name, "conversation_id": expectedID})
	if err != nil {
		return platformcore.ReplyConversation{}, err
	}
	data := workerDataMap(result)
	if !boolFromMap(data, "found") || stringFromMap(data, "conversation_id") != expectedID || stringFromMap(data, "panel_name") != name {
		return platformcore.ReplyConversation{}, platformcore.ErrReplyUnsafe
	}
	if err := r.verifySelectedConversation(ctx, exec, expectedID); err != nil {
		return platformcore.ReplyConversation{}, err
	}
	return platformcore.ReplyConversation{ID: expectedID, Name: name, ObservedResumeStatus: stringFromMap(data, "resume_status")}, nil
}
