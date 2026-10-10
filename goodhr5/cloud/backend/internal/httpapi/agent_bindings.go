// 本文件提供 HRPlus 当前账号的有效绑定电脑列表，连接记录不代表设备当前在线，不暴露公钥或其他账号。
package httpapi

import (
	"context"
	"net/http"
	"sort"
	"strings"
	"time"
)

// ListBindings 复制当前账号有效绑定，按最近连接和设备编号稳定排序。
func (s *MemoryAgentStore) ListBindings(email string) ([]AgentBinding, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	items := []AgentBinding{}
	for _, binding := range s.bindings {
		if binding.BindStatus == "active" && strings.EqualFold(binding.UserEmail, strings.TrimSpace(email)) {
			items = append(items, binding)
		}
	}
	sort.Slice(items, func(i, j int) bool {
		if !items[i].LastSeenAt.Equal(items[j].LastSeenAt) {
			return items[i].LastSeenAt.After(items[j].LastSeenAt)
		}
		return items[i].MachineID < items[j].MachineID
	})
	return items, nil
}

// ListBindings 只读取原有效绑定，不创建用户、不把最近连接视为在线证明。
func (s *PostgresAgentStore) ListBindings(email string) ([]AgentBinding, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	rows, err := s.db.QueryContext(ctx, `SELECT la.machine_id,la.agent_version,la.bind_status,COALESCE(la.last_seen_at,la.created_at),la.created_at FROM local_agents la JOIN users u ON u.id=la.user_id WHERE u.email=$1 AND la.bind_status='active' ORDER BY la.last_seen_at DESC NULLS LAST,la.machine_id`, strings.ToLower(strings.TrimSpace(email)))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []AgentBinding{}
	for rows.Next() {
		binding := AgentBinding{UserEmail: strings.ToLower(strings.TrimSpace(email))}
		if err := rows.Scan(&binding.MachineID, &binding.AgentVersion, &binding.BindStatus, &binding.LastSeenAt, &binding.CreatedAt); err != nil {
			return nil, err
		}
		items = append(items, binding)
	}
	return items, rows.Err()
}

// ListBindings 开发包装也只返回实际保存的绑定，不伪造设备列表。
func (s *permissiveAgentStore) ListBindings(email string) ([]AgentBinding, error) {
	stored, err := s.inner.ListBindings(email)
	if err != nil {
		return nil, err
	}
	items := []AgentBinding{}
	seen := map[string]bool{}
	for _, binding := range stored {
		physical, ok := physicalDevelopmentBinding(binding)
		if !ok || seen[physical.MachineID] {
			continue
		}
		seen[physical.MachineID] = true
		items = append(items, physical)
	}
	return items, nil
}

// Bindings 返回当前登录账号全部有效绑定，输出只含选择电脑所需信息。
func (s *AgentService) Bindings(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, 405, "电脑列表只支持读取")
		return
	}
	session, ok := s.currentSession(w, r)
	if !ok {
		return
	}
	items, err := s.store.ListBindings(session.Email)
	if err != nil {
		writeError(w, 500, "绑定电脑暂时无法读取，请稍后刷新")
		return
	}
	result := make([]map[string]any, 0, len(items))
	for _, binding := range items {
		result = append(result, map[string]any{"machine_id": binding.MachineID, "agent_version": binding.AgentVersion, "last_seen_at": binding.LastSeenAt})
	}
	writeJSON(w, 200, map[string]any{"ok": true, "agents": result})
}
