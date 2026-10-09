// 本文件使用真实 Windows 加密请求与隔离 HTTP 云端验证 HRPlus 补传服务登录、撤销及关闭生命周期。
package app

import (
	"context"
	"encoding/json"
	"goodhr5/local-agent-go/internal/cloudapi"
	"goodhr5/local-agent-go/internal/planmodel"
	"goodhr5/local-agent-go/internal/planoperations"
	"goodhr5/local-agent-go/internal/protectedsession"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// planUploadFixture 保存真实加密原释放和后台登录文件，不直接给予当前进程登录授权。
func planUploadFixture(t *testing.T, intercept func(http.ResponseWriter, *http.Request)) (*Server, protectedsession.Session, string, string, *atomic.Int32, *atomic.Bool) {
	t.Helper()
	s := newCapabilityTestServer(t)
	raw, err := os.ReadFile(filepath.Join("..", "planmodel", "testdata", "permit.json"))
	if err != nil {
		t.Fatal(err)
	}
	var permit planmodel.Permit
	if err = json.Unmarshal(raw, &permit); err != nil {
		t.Fatal(err)
	}
	posts := &atomic.Int32{}
	expired := &atomic.Bool{}
	cloud := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer fixture-plan-auth" {
			t.Error("补传没有使用原登录证明")
		}
		if r.URL.Path == "/api/auth/me" {
			if expired.Load() {
				w.WriteHeader(401)
				_ = json.NewEncoder(w).Encode(map[string]any{"error": "登录过期"})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "user": map[string]any{"email": "fixture@example.com", "tenant_id": "fixture-team"}})
			return
		}
		if r.URL.Path != "/api/execution-plan-runs/"+permit.Run.ID+"/release" {
			t.Error("后台访问了非状态路径", r.URL.Path)
		}
		posts.Add(1)
		var update cloudapi.PlanRunUpdateRequest
		if err := json.NewDecoder(r.Body).Decode(&update); err != nil {
			t.Error(err)
		}
		if intercept != nil {
			intercept(w, r)
			return
		}
		result := permit
		result.Run.Sequence = update.Sequence
		result.Run.State = update.State
		result.Owner.MachineID = update.MachineID
		result.Owner.State = "released"
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "permit": result})
	}))
	t.Cleanup(cloud.Close)
	s.cfg.CloudAPIBase = cloud.URL
	machine, err := s.ensureMachineID()
	if err != nil {
		t.Fatal(err)
	}
	value := protectedsession.Session{CloudBase: cloud.URL, UserEmail: "fixture@example.com", TenantID: "fixture-team", MachineID: machine, Token: "fixture-plan-auth"}
	if err = protectedsession.New(s.cfg.DataDir).Save(value); err != nil {
		t.Fatal(err)
	}
	scope := cloudapi.SessionOwnerScope(cloud.URL, value.UserEmail)
	request := "60000000-0000-0000-0000-000000000009"
	update := cloudapi.PlanRunUpdateRequest{RunID: permit.Run.ID, Action: "release", RequestID: request, OwnerID: permit.Owner.OwnerID, MachineID: machine, Credential: "fixture-plan-execution-secret-0123456789", Sequence: 2, State: "waiting_window", CleanupConfirmed: true}
	if _, err = planoperations.New(s.db).StageUpdate(t.Context(), scope, permit.Run.PlanID, update); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.runner.ClearReGreetUploadSession)
	return s, value, scope, request, posts, expired
}

// TestPlanUploadServiceRestoresLogin 验证没有登录不发送，启动恢复已核对登录后自动补传并可取消循环。
func TestPlanUploadServiceRestoresLogin(t *testing.T) {
	s, _, scope, request, posts, _ := planUploadFixture(t, nil)
	if sent, err := s.processPlanUploads(t.Context(), planoperations.New(s.db)); err != nil || sent || posts.Load() != 0 {
		t.Fatal("未登录发送了旧请求", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { defer close(done); s.runPlanUploads(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Error("补传循环关闭失败")
		}
	})
	s.restoreProtectedSession(t.Context())
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		operation, err := s.db.PlanOperation(t.Context(), scope, request)
		if err != nil {
			t.Fatal(err)
		}
		if operation.State == "confirmed" {
			break
		}
		select {
		case <-deadline.C:
			t.Fatal("恢复登录后未自动补传")
		case <-ticker.C:
		}
	}
	if posts.Load() != 1 {
		t.Fatalf("原请求上传次数=%d", posts.Load())
	}
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("关闭后补传没有停止")
	}
}

// TestPlanUploadServiceRevokesInFlight 验证退出或新登录验证开始后在途请求取消，旧回执保持待核对。
func TestPlanUploadServiceRevokesInFlight(t *testing.T) {
	for _, action := range []string{"logout", "new_login"} {
		t.Run(action, func(t *testing.T) {
			entered, cancelled := make(chan struct{}), make(chan struct{})
			release := make(chan struct{})
			s, value, scope, request, posts, _ := planUploadFixture(t, func(w http.ResponseWriter, r *http.Request) {
				close(entered)
				select {
				case <-r.Context().Done():
					close(cancelled)
				case <-release:
				}
			})
			t.Cleanup(func() { close(release) })
			sequence := s.beginProtectedSessionChange()
			if err := s.commitProtectedSession(t.Context(), sequence, value); err != nil {
				t.Fatal(err)
			}
			done := make(chan struct{})
			go func() { defer close(done); _, _ = s.processPlanUploads(t.Context(), planoperations.New(s.db)) }()
			select {
			case <-entered:
			case <-time.After(5 * time.Second):
				t.Fatal("未进入真实 HTTP 上传")
			}
			if action == "logout" {
				recorder := httptest.NewRecorder()
				s.handleSessionUnbind(recorder, httptest.NewRequest(http.MethodPost, "/api/v1/session/unbind", nil))
				if recorder.Code != 200 {
					t.Fatal(recorder.Body.String())
				}
			} else {
				s.beginProtectedSessionChange()
			}
			status := httptest.NewRecorder()
			s.handleProtectedSessionStatus(status, httptest.NewRequest(http.MethodGet, "/api/v1/session/status", nil))
			if !strings.Contains(status.Body.String(), `"verified":false`) {
				t.Fatal("撤销或待核对登录仍显示已核对", status.Body.String())
			}
			select {
			case <-cancelled:
			case <-time.After(3 * time.Second):
				t.Fatal("授权变化未取消在途请求")
			}
			select {
			case <-done:
			case <-time.After(3 * time.Second):
				t.Fatal("旧上传没有退出")
			}
			operation, err := s.db.PlanOperation(t.Context(), scope, request)
			if err != nil || operation.State != "pending" {
				t.Fatal("旧授权确认了不明确回执", err)
			}
			if sent, err := s.processPlanUploads(t.Context(), planoperations.New(s.db)); err != nil || sent || posts.Load() != 1 {
				t.Fatal("旧授权再次上传", err)
			}
		})
	}
}

// TestPlanUploadExpiredSession 验证已观察到的过期登录撤销授权，原事实保留并在同账号新核对后恢复。
func TestPlanUploadExpiredSession(t *testing.T) {
	s, value, scope, request, posts, expired := planUploadFixture(t, nil)
	s.restoreProtectedSession(t.Context())
	expired.Store(true)
	if sent, err := s.processPlanUploads(t.Context(), planoperations.New(s.db)); err == nil || sent || posts.Load() != 0 {
		t.Fatal("过期登录仍发送状态", err)
	}
	s.sessionMu.Lock()
	blocked := s.sessionBlocked && s.sessionCurrent == nil
	s.sessionMu.Unlock()
	if !blocked {
		t.Fatal("登录过期仍保留执行授权")
	}
	op, err := s.db.PlanOperation(t.Context(), scope, request)
	if err != nil || op.State != "pending" {
		t.Fatal("登录过期删除原事实", err)
	}
	expired.Store(false)
	sequence := s.beginProtectedSessionChange()
	if err = s.commitProtectedSession(t.Context(), sequence, value); err != nil {
		t.Fatal(err)
	}
	if sent, err := s.processPlanUploads(t.Context(), planoperations.New(s.db)); err != nil || !sent || posts.Load() != 1 {
		t.Fatal("同账号重新核对后未恢复原请求", err)
	}
	current := s.sessionCurrent
	s.invalidateExpiredPlanSession(sequence-1, current)
	if s.sessionCurrent != current {
		t.Fatal("迟到过期结果撤销了新授权")
	}
}
