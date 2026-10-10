// 本文件提供 HRPlus 网页计划状态的只读主动通知，通知只要求重读事实，不携带启动命令或凭证。
package httpapi

import (
	"crypto/md5"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/lib/pq"
)

// planEventHub 合并同账号的变更提示；慢网页不会阻塞任务上报，重新连接必须重读事实。
type planEventHub struct {
	mu       sync.Mutex
	watchers map[string]map[chan struct{}]struct{}
}

// planEventScope 创建仅用于匹配的作用域摘要，不用它授予权限。
func planEventScope(tenant, email string) string {
	return fmt.Sprintf("%x", md5.Sum([]byte(tenant+"\n"+email)))
}

// subscribe 在发送 ready 前注册提示，避免首次读取和订阅之间漏掉变更。
func (h *planEventHub) subscribe(scope string) (chan struct{}, func(), bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.watchers == nil {
		h.watchers = make(map[string]map[chan struct{}]struct{})
	}
	if h.watchers[scope] == nil {
		h.watchers[scope] = make(map[chan struct{}]struct{})
	}
	if len(h.watchers[scope]) >= 8 {
		return nil, nil, false
	}
	ch := make(chan struct{}, 1)
	h.watchers[scope][ch] = struct{}{}
	return ch, func() {
		h.mu.Lock()
		defer h.mu.Unlock()
		delete(h.watchers[scope], ch)
		if len(h.watchers[scope]) == 0 {
			delete(h.watchers, scope)
		}
	}, true
}

// publish 非阻塞合并变更通知，不把其他用户的编号或状态广播给网页。
func (h *planEventHub) publish(scope string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.watchers[scope] {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

// planEventResponse 捕获写入结果，失败和只读请求不能产生成功变更提示。
type planEventResponse struct {
	http.ResponseWriter
	status int
}

// WriteHeader 保存状态码后交给原响应。
func (w *planEventResponse) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

// notifyMutation 在内存模式的成功写接口之后发提示；数据库模式使用提交后 NOTIFY。
func (s *ExecutionPlanService) notifyMutation(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet || s.eventDSN != "" {
			next(w, r)
			return
		}
		tenant, email, ok := s.identity(w, r)
		if !ok {
			return
		}
		result := &planEventResponse{ResponseWriter: w, status: 200}
		next(result, r)
		if result.status >= 200 && result.status < 300 {
			s.events.publish(planEventScope(tenant, email))
		}
	}
}

// eventScopeValid 发送任何变更或保活前重核真实会话和团队，失效时直接关闭原订阅。
func (s *ExecutionPlanService) eventScopeValid(r *http.Request, scope string) bool {
	session, err := s.positions.auth.SessionFromRequest(r)
	if err != nil {
		return false
	}
	tenant := ""
	if s.positions.auth.tenantStore != nil {
		value, err := s.positions.auth.tenantStore.GetOrCreateTenant(session.Email)
		if err != nil {
			return false
		}
		tenant = value.ID
	}
	return planEventScope(tenant, session.Email) == scope
}

// Events 使用请求头认证建立只读 SSE；连接或重连后先发送 ready，让页面重新加载云端事实。
func (s *ExecutionPlanService) Events(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, 405, "状态订阅只支持读取")
		return
	}
	tenant, email, ok := s.identity(w, r)
	if !ok {
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, 503, "状态订阅暂时不可用")
		return
	}
	scope := planEventScope(tenant, email)
	changes, unsubscribe, ok := s.events.subscribe(scope)
	if !ok {
		writeError(w, 429, "打开的计划页面过多，请关闭暂时不用的页面")
		return
	}
	defer unsubscribe()
	var notifications <-chan *pq.Notification
	if s.eventDSN != "" {
		listener := pq.NewListener(s.eventDSN, time.Second, 30*time.Second, nil)
		defer listener.Close()
		finished := make(chan struct{})
		defer close(finished)
		go func() {
			select {
			case <-r.Context().Done():
				_ = listener.Close()
			case <-finished:
			}
		}()
		if err := listener.Listen("hrplus_plan_changes"); err != nil {
			writeError(w, 503, "状态订阅暂时无法连接")
			return
		}
		notifications = listener.Notify
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache, no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(5 * time.Second))
	if _, err := fmt.Fprint(w, "event: ready\ndata: {}\n\n"); err != nil {
		return
	}
	flusher.Flush()
	heartbeat := time.NewTicker(20 * time.Second)
	defer heartbeat.Stop()
	for {
		frame := "event: changed\ndata: {}\n\n"
		select {
		case <-r.Context().Done():
			return
		case <-changes:
		case notification, open := <-notifications:
			if !open {
				return
			}
			if notification != nil && notification.Extra != scope {
				continue
			}
		case <-heartbeat.C:
			frame = ": heartbeat\n\n"
		}
		if !s.eventScopeValid(r, scope) {
			return
		}
		_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(5 * time.Second))
		if _, err := fmt.Fprint(w, frame); err != nil {
			return
		}
		flusher.Flush()
	}
}
