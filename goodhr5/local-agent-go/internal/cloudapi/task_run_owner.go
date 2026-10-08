// 本文件通过已有认证与执行任务查询接口核对旧收据的所有者，仅核对原事实，不创建任务或发送消息。
package cloudapi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"strings"
)

// SessionOwnerScope 为认证接口确认的所有者生成作用域，隔离不同云端地址。
func SessionOwnerScope(base, owner string) string {
	sum := sha256.Sum256([]byte("cloud-owner:" + strings.TrimRight(strings.TrimSpace(base), "/") + "\n" + strings.ToLower(strings.TrimSpace(owner))))
	return hex.EncodeToString(sum[:])
}

// VerifyTaskRunOwner 核对当前令牌、原任务、岗位、平台和机器，旧收据不能仅凭默认目录取得归属。
func (c *Client) VerifyTaskRunOwner(ctx context.Context, token, runID, positionID, platform, machineID, ownerScope string) (bool, error) {
	if runID == "" || positionID == "" || platform == "" || machineID == "" || ownerScope == "" {
		return false, nil
	}
	owner, err := c.SessionOwner(ctx, token)
	if err != nil {
		return false, err
	}
	if SessionOwnerScope(c.BaseURL, owner) != ownerScope {
		return false, nil
	}
	payload, status, err := c.getAuthed(ctx, token, "/api/task-runs/"+url.PathEscape(runID))
	if err != nil {
		return false, err
	}
	if status == 401 || status == 403 {
		return false, AuthExpiredError{Message: "原收据的任务权限尚未核对，请重新登录"}
	}
	if status == 404 {
		return false, nil
	}
	if status >= 400 {
		return false, fmt.Errorf("原任务归属核对失败")
	}
	if data, ok := payload["data"].(map[string]any); ok {
		payload = data
	}
	run, _ := payload["run"].(map[string]any)
	field := func(key string) string { value, _ := run[key].(string); return value }
	return field("id") == runID && field("position_id") == positionID && field("platform_id") == platform && field("machine_id") == machineID && strings.EqualFold(field("user_email"), owner), nil
}
