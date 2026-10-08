// 本文件在当前进程登录有效时推进 HRPlus 复打补传，不依赖岗位运行或招聘页面执行权。
package positionrunner

import (
	"context"
	"time"

	"goodhr5/local-agent-go/internal/platformcore"
	"goodhr5/local-agent-go/internal/regreetupload"
)

// BindReGreetUploadSession 仅在设备绑定成功后接收当前令牌，恢复默认 Profile 的原收据补传。
// M1 不持久化令牌；程序重启后等待页面重新完成登录绑定。
func (r *Runner) BindReGreetUploadSession(token string) {
	if token == "" {
		return
	}
	scope := platformcore.ReplyHash("default")
	r.mu.Lock()
	r.uploadToken = token
	r.mu.Unlock()
	if err := r.db.ResumeReGreetUploads(context.Background(), scope); err != nil {
		return
	}
	r.ensureReGreetUploader()
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
		defer func() { r.mu.Lock(); r.uploadActive = false; r.mu.Unlock() }()
		uploader := regreetupload.New(r.db)
		scope := platformcore.ReplyHash("default")
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			r.mu.Lock()
			token := r.uploadToken
			r.mu.Unlock()
			if token == "" {
				return
			}
			if _, err := uploader.Flush(context.Background(), scope, token); err != nil {
				return
			}
			r.mu.Lock()
			currentToken := r.uploadToken
			r.mu.Unlock()
			if currentToken != token && currentToken != "" {
				if err := r.db.ResumeReGreetUploads(context.Background(), scope); err != nil {
					return
				}
			}
			// 队列暂空也保留当前进程的单一补传器，避免末条入队与退出竞争造成遗漏。
			<-ticker.C
		}
	}()
}
