// 本文件将 HRPlus 计划状态补传接入本地服务生命周期，不申请页面执行权或自动重启领取请求。
package app

import (
	"context"
	"errors"
	"goodhr5/local-agent-go/internal/cloudapi"
	"goodhr5/local-agent-go/internal/planoperations"
	"goodhr5/local-agent-go/internal/protectedsession"
	"log"
	"time"
)

// signalPlanUploads 在已核对登录发布后唤醒补传，重复通知合并，五秒检查作为兜底。
func (s *Server) signalPlanUploads() {
	select {
	case s.planUploadWake <- struct{}{}:
	default:
	}
}

// runPlanUploads 只在当前服务有效期间上传原状态；无登录不读密文，重复失败不刷屏。
func (s *Server) runPlanUploads(ctx context.Context) {
	store := planoperations.New(s.db)
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	previousFailure := ""
	for {
		if ctx.Err() != nil {
			return
		}
		sent, err := s.processPlanUploads(ctx, store)
		if err != nil && !errors.Is(err, context.Canceled) {
			message := err.Error()
			if message != previousFailure {
				log.Printf("[执行计划补传] 原请求保留待核对：%s", message)
				previousFailure = message
			}
		} else if sent || err == nil {
			previousFailure = ""
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-s.planUploadWake:
		}
	}
}

// processPlanUploads 绑定本次已提交的登录代次，退出或新登录验证会取消在途请求。
func (s *Server) processPlanUploads(parent context.Context, store *planoperations.Store) (bool, error) {
	s.sessionMu.Lock()
	if s.sessionCurrent == nil || s.sessionBlocked || s.sessionVersion != s.sessionCommittedVersion || s.planUploadCancel != nil {
		s.sessionMu.Unlock()
		return false, nil
	}
	identity := s.sessionCurrent
	sequence := s.sessionVersion
	ctx, cancel := context.WithTimeout(parent, 15*time.Second)
	s.planUploadEpoch++
	epoch := s.planUploadEpoch
	s.planUploadCancel = cancel
	s.sessionMu.Unlock()
	defer func() {
		cancel()
		s.sessionMu.Lock()
		if s.planUploadEpoch == epoch {
			s.planUploadCancel = nil
		}
		s.sessionMu.Unlock()
	}()
	validLocked := func() bool {
		return !s.sessionBlocked && s.sessionCurrent == identity && s.sessionVersion == sequence && s.sessionCommittedVersion == sequence
	}
	authority := planoperations.Authority{Token: identity.Token, OwnerScope: cloudapi.SessionOwnerScope(identity.CloudBase, identity.UserEmail)}
	authority.StillCurrent = func() bool { s.sessionMu.Lock(); defer s.sessionMu.Unlock(); return validLocked() }
	authority.ConfirmCurrent = func(confirm func() error) error {
		s.sessionMu.Lock()
		defer s.sessionMu.Unlock()
		if !validLocked() {
			return errors.New("计划补传登录证明已变化，保留原回执待核对")
		}
		return confirm()
	}
	client := cloudapi.New(identity.CloudBase)
	sentAny := false
	for i := 0; i < 8; i++ {
		sent, err := store.UploadNext(ctx, client, authority)
		if err == nil && !sent {
			sent, err = store.ResolveNextCleanup(ctx, client, authority)
		}
		if err == nil && !sent {
			sent, err = store.UploadNextStop(ctx, client, authority)
		}
		if err != nil {
			var expired cloudapi.AuthExpiredError
			if errors.As(err, &expired) {
				s.invalidateExpiredPlanSession(sequence, identity)
			}
			return sentAny, err
		}
		if !sent {
			return sentAny, nil
		}
		sentAny = true
		s.signalPlanExecutions()
	}
	return sentAny, nil
}

// invalidateExpiredPlanSession 只撤销仍对应原代次的过期授权，不让迟到错误影响新登录用户。
func (s *Server) invalidateExpiredPlanSession(sequence uint64, expected *protectedsession.Session) {
	// 登录切换正在等待页面收尾时，不能在后台等待同一把锁并阻断收尾；下一轮会重试或旧代次已失效。
	if !s.sessionOpMu.TryLock() {
		return
	}
	defer s.sessionOpMu.Unlock()
	s.sessionMu.Lock()
	if s.sessionVersion != sequence || s.sessionCurrent != expected {
		s.sessionMu.Unlock()
		return
	}
	s.sessionVersion++
	s.sessionCurrent = nil
	s.sessionBlocked = true
	cancel := s.planUploadCancel
	s.planUploadCancel = nil
	s.sessionMu.Unlock()
	if cancel != nil {
		cancel()
	}
	s.runner.ClearReGreetUploadSession()
	s.runner.StopAll("登录状态已失效，停止原账号任务")
	s.signalPlanExecutions()
}
