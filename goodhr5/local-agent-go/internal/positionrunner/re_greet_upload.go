// 本文件在当前进程登录有效时推进 HRPlus 复打补传，不依赖岗位运行或招聘页面执行权。
package positionrunner

import (
	"context"
	"strings"
	"time"

	"goodhr5/local-agent-go/internal/platformcore"
	"goodhr5/local-agent-go/internal/regreetupload"
)

// BindReGreetUploadSession 接收当前进程登录证明；有所有者时只恢复已核对作用域，旧调用保留默认作用域兼容。
// M1 不持久化令牌；程序重启后等待页面重新完成登录绑定。
func (r *Runner) BindReGreetUploadSession(token string, verifiedOwnerScope ...string) {
	r.bindReGreetUploadSession(token, verifiedOwnerScope, nil)
}

// ClearReGreetUploadSession 退出或切换用户时清除上传授权并使迟到的旧登录证明失效。
func (r *Runner) ClearReGreetUploadSession() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.uploadSessionVersion++
	r.uploadToken = ""
	r.uploadOwnerScope = ""
	r.uploadAPIBase = ""
}

// ReGreetUploadSessionVersion 在云端核对前读取当前登录版本，不返回或持久化令牌。
func (r *Runner) ReGreetUploadSessionVersion() uint64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.uploadSessionVersion
}

// BindVerifiedReGreetUploadSession 仅提交仍对应核对前版本的结果，迟到的旧任务或登录请求不能覆盖新会话。
func (r *Runner) BindVerifiedReGreetUploadSession(token, ownerScope string, expectedVersion uint64, verifiedAPIBase ...string) {
	base := r.cloudAPIBase
	if len(verifiedAPIBase) > 0 {
		base = verifiedAPIBase[0]
	}
	r.bindReGreetUploadSession(token, []string{ownerScope, strings.TrimRight(strings.TrimSpace(base), "/")}, &expectedVersion)
}

// bindReGreetUploadSession 原子保存进程内登录证明，并只恢复该所有者已核对的待补传作用域。
func (r *Runner) bindReGreetUploadSession(token string, verifiedOwnerScope []string, expectedVersion *uint64) {
	if token == "" {
		return
	}
	r.mu.Lock()
	if expectedVersion != nil && r.uploadSessionVersion != *expectedVersion {
		r.mu.Unlock()
		return
	}
	r.uploadSessionVersion++
	sessionChanged := r.uploadToken != token
	if len(verifiedOwnerScope) > 0 {
		sessionChanged = sessionChanged || r.uploadOwnerScope != verifiedOwnerScope[0]
		r.uploadOwnerScope = verifiedOwnerScope[0]
		if len(verifiedOwnerScope) > 1 {
			r.uploadAPIBase = verifiedOwnerScope[1]
		} else {
			r.uploadAPIBase = r.cloudAPIBase
		}
	} else if r.uploadToken != token {
		r.uploadOwnerScope = ""
		r.uploadAPIBase = ""
	}
	r.uploadToken = token
	ownerScope := r.uploadOwnerScope
	r.mu.Unlock()
	scopes, err := r.reGreetUploadScopes(context.Background(), ownerScope)
	if err != nil {
		return
	}
	for _, scope := range scopes {
		if err := r.db.ResumeReGreetUploads(context.Background(), scope); err != nil {
			return
		}
	}
	if ownerScope != "" && sessionChanged {
		if err := r.db.ResumeReGreetUploads(context.Background(), platformcore.ReplyHash("default")); err != nil {
			return
		}
	}
	r.ensureReGreetUploader()
}

// reGreetUploadScopes 只领取当前云端所有者已核对的作用域，旧调用保持原默认作用域，不猜测旧记录归属。
func (r *Runner) reGreetUploadScopes(ctx context.Context, ownerScope string) ([]string, error) {
	if ownerScope == "" {
		return []string{platformcore.ReplyHash("default")}, nil
	}
	return r.db.AccountScopesForOwner(ctx, ownerScope)
}

// ensureReGreetUploader 保证补传工作只有一个实例，任务停止不会取消已确认事实的补传。
func (r *Runner) ensureReGreetUploader() {
	r.mu.Lock()
	if r.uploadActive || r.uploadToken == "" {
		r.mu.Unlock()
		return
	}
	r.uploadActive = true
	r.mu.Unlock()
	go func() {
		restartIfRebound := false
		defer func() {
			r.mu.Lock()
			r.uploadActive = false
			restart := restartIfRebound && r.uploadToken != ""
			r.mu.Unlock()
			if restart {
				r.ensureReGreetUploader()
			}
		}()
		uploader := regreetupload.New(r.db)
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			r.mu.Lock()
			token := r.uploadToken
			ownerScope := r.uploadOwnerScope
			apiBase := r.uploadAPIBase
			r.mu.Unlock()
			if token == "" {
				restartIfRebound = true
				return
			}
			scopes, err := r.reGreetUploadScopes(context.Background(), ownerScope)
			if err != nil {
				return
			}
			for _, scope := range scopes {
				if _, err := uploader.Flush(context.Background(), scope, token); err != nil {
					return
				}
			}
			if ownerScope != "" {
				if _, err := uploader.FlushLegacyOwned(context.Background(), platformcore.ReplyHash("default"), token, ownerScope, apiBase); err != nil {
					return
				}
			}
			r.mu.Lock()
			currentToken := r.uploadToken
			currentOwnerScope := r.uploadOwnerScope
			r.mu.Unlock()
			if (currentToken != token || currentOwnerScope != ownerScope) && currentToken != "" {
				currentScopes, err := r.reGreetUploadScopes(context.Background(), currentOwnerScope)
				if err != nil {
					return
				}
				for _, scope := range currentScopes {
					if err := r.db.ResumeReGreetUploads(context.Background(), scope); err != nil {
						return
					}
				}
				if currentOwnerScope != "" {
					if err := r.db.ResumeReGreetUploads(context.Background(), platformcore.ReplyHash("default")); err != nil {
						return
					}
				}
			}
			// 队列暂空也保留当前进程的单一补传器，避免末条入队与退出竞争造成遗漏。
			<-ticker.C
		}
	}()
}
