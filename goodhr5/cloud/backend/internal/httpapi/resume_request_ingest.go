// 本文件作用：接收本地程序岗位收尾后的"求简历"结果补报——
// 按岗位与候选人姓名定位简历库记录，逐人落 resume_requested 事件并更新索要时间，
// 供简历库"进展时间线"和执行任务"索要名单"展示。独立于 SaveLocalCandidate，避免重复累加岗位统计。
package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// notifyResumeRequestsRequest 表示本地程序补报"求简历"结果的请求体。
type notifyResumeRequestsRequest struct {
	RunID string   `json:"run_id"` // 执行任务记录 ID，事件归组到本次岗位运行
	Names []string `json:"names"`  // 本轮完成求简历的候选人姓名
}

// NotifyResumeRequests 接收本地程序岗位收尾后的"求简历"补报。
// w 为响应对象，r 为请求对象；路径格式为 /api/positions/{positionID}/resume-requests。
func (s *PositionExecutionService) NotifyResumeRequests(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	session, ok := s.currentSession(w, r)
	if !ok {
		return
	}
	if s.candidateStore == nil || s.store == nil {
		writeError(w, http.StatusInternalServerError, "candidate store is not ready")
		return
	}
	positionID := positionSubresourceID(r.URL.Path, "resume-requests")
	if positionID == "" {
		writeError(w, http.StatusBadRequest, "position id is required")
		return
	}
	tenantID, isAdmin := s.getTenantInfo(session.Email)
	position, err := s.store.PositionByID(tenantID, session.Email, positionID, isAdmin)
	if errors.Is(err, ErrNotFound) {
		writeError(w, http.StatusNotFound, "position not found")
		return
	} else if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load position")
		return
	}
	var req notifyResumeRequestsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json body")
		return
	}
	names := make([]string, 0, len(req.Names))
	seen := map[string]bool{}
	for _, name := range req.Names {
		name = strings.TrimSpace(name)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		names = append(names, name)
	}
	if len(names) == 0 {
		writeError(w, http.StatusBadRequest, "names is required")
		return
	}
	runID := strings.TrimSpace(req.RunID)
	engagements, err := s.candidateStore.FindEngagementsByPositionAndNames(position.ID, names)
	if err != nil {
		s.writeCandidateIngestLog(position.ID, position.UserEmail, "warning", "求简历补报：查找候选人失败，原因="+err.Error())
		writeError(w, http.StatusInternalServerError, "failed to find candidates")
		return
	}
	now := time.Now().UTC()
	matched := make([]string, 0, len(names))
	missing := make([]string, 0)
	for _, name := range names {
		engagement, exist := engagements[name]
		if !exist {
			missing = append(missing, name)
			s.writeCandidateIngestLog(position.ID, position.UserEmail, "warning", "求简历补报：候选人不在简历库中，姓名="+name)
			continue
		}
		_, _ = s.candidateStore.SaveCandidateEvent(CandidateEvent{
			CandidateID:  engagement.CandidateID,
			EngagementID: engagement.ID,
			TaskID:       runID,
			PositionID:   position.ID,
			PlatformID:   position.PlatformID,
			EventType:    "resume_requested",
			Metadata:     map[string]any{"source": "local-agent-go"},
		})
		// 状态保持原值（传空由存储层保持），只更新索要时间。
		_ = s.candidateStore.UpdateCandidateEngagementStatus(engagement.ID, "", nil, nil, &now)
		matched = append(matched, name)
	}
	s.writeCandidateIngestLog(position.ID, position.UserEmail, "info", fmt.Sprintf(
		"求简历补报：本次补报=%d，未匹配=%d", len(matched), len(missing),
	))
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":      true,
		"matched": matched,
		"missing": missing,
	})
}
