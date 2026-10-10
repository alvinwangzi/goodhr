// 本文件只在 HRPlus 内存开发模式的旧手动运行身份变化后通知计划页面，不把统计心跳当新状态。
package httpapi

import (
	"encoding/json"
	"net/http"
	"sort"
)

// legacyExecutionFingerprint 在固定锁顺序下读取账号的真实运行身份，不读取候选人或登录凭证。
func (s *MemoryExecutionPlanStore) legacyExecutionFingerprint(email string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.positions == nil {
		return ""
	}
	s.positions.mu.Lock()
	defer s.positions.mu.Unlock()
	if s.taskRuns != nil {
		s.taskRuns.mu.Lock()
		defer s.taskRuns.mu.Unlock()
	}
	values := []string{}
	for _, position := range s.positions.positions {
		if position.UserEmail == email && position.Status == "running" {
			values = append(values, "position/"+position.ID)
		}
	}
	if s.taskRuns != nil {
		for _, task := range s.taskRuns.runs {
			if task.UserEmail == email && (task.Status == "running" || task.Status == "starting") {
				values = append(values, "task/"+task.ID+"/"+task.MachineID+"/"+task.Status)
			}
		}
	}
	sort.Strings(values)
	raw, _ := json.Marshal(values)
	return string(raw)
}

// notifyLegacyPositionMutation 比较成功修改前后的运行事实，拒绝、读取及同状态心跳不发变更提示。
func (s *ExecutionPlanService) notifyLegacyPositionMutation(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		memory, ok := s.store.(*MemoryExecutionPlanStore)
		if !ok || s.eventDSN != "" || r.Method == http.MethodGet {
			next(w, r)
			return
		}
		tenant, email, valid := s.identity(w, r)
		if !valid {
			return
		}
		before := memory.legacyExecutionFingerprint(email)
		result := &planEventResponse{ResponseWriter: w, status: 200}
		next(result, r)
		if result.status >= 200 && result.status < 300 && before != memory.legacyExecutionFingerprint(email) {
			s.events.publish(planEventScope(tenant, email))
		}
	}
}
