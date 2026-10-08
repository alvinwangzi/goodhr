// 本文件在候选人处理前绑定 HRPlus 云端所有者与真实招聘账号，并保持同一运行的账号不可变。
package positionrunner

import (
	"context"
	"fmt"
	"goodhr5/local-agent-go/internal/cloudapi"
	"goodhr5/local-agent-go/internal/localdb"
	"goodhr5/local-agent-go/internal/platformcore"
	"strings"
)

// CloudOwnerScope 为已验证云端所有者计算不可逆作用域，包含接口地址以隔离不同环境。
func CloudOwnerScope(base, owner string) string {
	return cloudapi.SessionOwnerScope(base, owner)
}

// bindPlatformAccountScope 在页面准备后、处理任何候选人前原子绑定账号；同一运行只核对，不再刷新页面。
func (r *Runner) bindPlatformAccountScope(ctx context.Context, exec platformExecutor, runtime any, position localdb.Position, options StartOptions) (string, bool, error) {
	legacy := platformcore.ReplyHash("profile:" + safePathName(positionProfileName(position)))
	provider, supported := runtime.(platformcore.AccountIdentityRuntime)
	if !supported || options.LocalRunID == "" {
		return legacy, false, nil
	}
	checkpoint, err := r.db.LoadActionCheckpoint(ctx, options.LocalRunID)
	if err != nil {
		return "", false, err
	}
	accountID, err := provider.ObserveAccountIdentity(ctx, exec, !checkpoint.AccountBound)
	if err != nil {
		return "", false, err
	}
	base := firstNonEmptyString(options.CloudAPIBase, r.cloudAPIBase)
	owner, err := cloudapi.New(base).SessionOwner(ctx, options.Token)
	if err != nil {
		return "", false, err
	}
	ownerScope := CloudOwnerScope(base, owner)
	scope := platformcore.ReplyHash("m1-account:" + legacy + ":" + position.PlatformID + ":" + ownerScope + ":" + accountID)
	if _, err = r.db.BindActionRunAccount(ctx, options.LocalRunID, scope, ownerScope); err != nil {
		return "", false, err
	}
	r.mu.Lock()
	if state := r.running[position.ID]; state != nil {
		if state.expectedPlatformAccountID != "" && state.expectedPlatformAccountID != accountID {
			r.mu.Unlock()
			return "", false, fmt.Errorf("本次运行登录账号已变化")
		}
		state.expectedPlatformAccountID = accountID
		state.expectedAccountPlatform = strings.ToLower(strings.TrimSpace(position.PlatformID))
	}
	r.mu.Unlock()
	return scope, true, nil
}
