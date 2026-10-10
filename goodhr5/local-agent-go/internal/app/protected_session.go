// 本文件接入 HRPlus 本地受保护会话的绑定、恢复、退出和用户切换，身份以云端核对为准。
package app

import (
	"context"
	"errors"
	"goodhr5/local-agent-go/internal/cloudapi"
	"goodhr5/local-agent-go/internal/positionrunner"
	"goodhr5/local-agent-go/internal/protectedsession"
	"goodhr5/local-agent-go/internal/response"
	"net/http"
	"os"
	"strings"
	"time"
)

// beginProtectedSessionChange 使旧恢复或绑定结果失效，不把未通过核对的新用户写入当前身份。
func (s *Server) beginProtectedSessionChange() uint64 {
	s.sessionMu.Lock()
	s.sessionVersion++
	sequence := s.sessionVersion
	cancel := s.planUploadCancel
	s.planUploadCancel = nil
	s.sessionMu.Unlock()
	if cancel != nil {
		cancel()
	}
	s.signalPlanExecutions()
	return sequence
}

// commitProtectedSession 保证同一会话序号有效，切换前等待原页面收尾，再加密保存。
func (s *Server) commitProtectedSession(ctx context.Context, sequence uint64, value protectedsession.Session) error {
	s.sessionOpMu.Lock()
	defer s.sessionOpMu.Unlock()
	s.sessionMu.Lock()
	if sequence != s.sessionVersion {
		s.sessionMu.Unlock()
		return errors.New("登录连接已变化")
	}
	old := s.sessionCurrent
	s.sessionMu.Unlock()
	if old == nil || old.UserEmail != value.UserEmail || old.TenantID != value.TenantID || old.CloudBase != value.CloudBase {
		stopCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		if err := s.runner.StopAllAndWait(stopCtx, "登录用户已切换，原用户任务停止"); err != nil {
			return err
		}
	}
	s.sessionMu.Lock()
	defer s.sessionMu.Unlock()
	if sequence != s.sessionVersion {
		return errors.New("登录连接已变化")
	}
	if protectedsession.Available() {
		if err := protectedsession.New(s.cfg.DataDir).Save(value); err != nil {
			return err
		}
	}
	copy := value
	s.sessionCurrent = &copy
	s.sessionCommittedVersion = sequence
	s.sessionBlocked = false
	s.runner.BindReGreetUploadSession(value.Token, positionrunner.CloudOwnerScope(value.CloudBase, value.UserEmail), value.CloudBase)
	s.signalPlanUploads()
	s.signalPlanExecutions()
	return nil
}

// restoreProtectedSession 由本地服务启动恢复；网页关闭不影响恢复，旧地址或设备不使用旧凭证。
func (s *Server) restoreProtectedSession(parent context.Context) {
	s.sessionMu.Lock()
	if s.sessionVersion != 0 || s.sessionBlocked || s.sessionCurrent != nil {
		s.sessionMu.Unlock()
		return
	}
	sequence := s.sessionVersion
	s.sessionMu.Unlock()
	saved, err := protectedsession.New(s.cfg.DataDir).Load()
	if err != nil {
		return
	}
	if saved.CloudBase != strings.TrimRight(s.cfg.CloudAPIBase, "/") {
		return
	}
	machine, err := s.ensureMachineID()
	if err != nil || machine != saved.MachineID {
		return
	}
	ctx, cancel := context.WithTimeout(parent, 10*time.Second)
	defer cancel()
	identity, err := cloudapi.New(saved.CloudBase).SessionIdentity(ctx, saved.Token)
	if err != nil || identity.UserEmail != saved.UserEmail || (saved.TenantID != "" && identity.TenantID != saved.TenantID) {
		return
	}
	saved.TenantID = identity.TenantID
	if err = s.commitProtectedSession(ctx, sequence, saved); err != nil {
		return
	}
}

// handleSessionUnbind 先使所有旧验证结果失效，再清除凭证和运行；不因云端暂不可达保留登录。
func (s *Server) handleSessionUnbind(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		response.Error(w, 405, "此接口只支持退出登录")
		return
	}
	s.sessionOpMu.Lock()
	defer s.sessionOpMu.Unlock()
	s.sessionMu.Lock()
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
	err := protectedsession.New(s.cfg.DataDir).Clear()
	s.runner.StopAll("用户退出登录，停止原用户任务")
	s.signalPlanExecutions()
	if err != nil && !os.IsNotExist(err) {
		response.Error(w, 500, "受保护会话清除失败，请重试")
		return
	}
	response.Success(w, map[string]any{"unbound": true, "settling": true})
}

// handleProtectedSessionStatus 只返回已核对元数据，不返回令牌、密文或解密内容。
func (s *Server) handleProtectedSessionStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		response.Error(w, 405, "此接口只支持读取登录状态")
		return
	}
	s.sessionMu.Lock()
	defer s.sessionMu.Unlock()
	verified := s.sessionCurrent != nil && !s.sessionBlocked && s.sessionVersion == s.sessionCommittedVersion
	data := map[string]any{"verified": verified, "sequence": s.sessionVersion}
	data["protected_persistence"] = protectedsession.Available()
	if value := s.sessionCurrent; value != nil {
		data["user_email"] = value.UserEmail
		data["tenant_id"] = value.TenantID
		data["machine_id"] = value.MachineID
	}
	response.Success(w, data)
}
