// 本文件按预期真实会话 ID 定位 Boss 候选人，姓名仅用于辅助核对，禁止猜测跨入口映射。
package boss

import (
	"context"
	"goodhr5/local-agent-go/internal/platformcore"
	"strings"
)

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
