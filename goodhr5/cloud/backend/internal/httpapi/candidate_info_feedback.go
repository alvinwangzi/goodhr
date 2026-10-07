// 本文件复用索要结果入口，按稳定 ID 保存电话、微信、简历各项反馈，不存联系方式正文或增加打招呼统计。
package httpapi

import (
	"crypto/sha256"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// CandidateInfoFeedback 表示一个索要动作的确定或未知结果，不包含实际电话、微信号。
type CandidateInfoFeedback struct {
	RequestID     string `json:"request_id"`
	CandidateID   string `json:"candidate_id"`
	CandidateName string `json:"candidate_name"`
	Action        string `json:"action"`
	State         string `json:"state"`
}

// candidateInfoEventID 把岗位、档案、意图、动作及状态绑定成固定事件 ID，重复补报不会增加事件。
func candidateInfoEventID(positionID, candidateID string, info CandidateInfoFeedback) string {
	sum := sha256.Sum256([]byte(strings.Join([]string{positionID, candidateID, info.RequestID, info.Action, info.State}, "|")))
	return fmt.Sprintf("%x-%x-%x-%x-%x", sum[0:4], sum[4:6], sum[6:8], sum[8:10], sum[10:16])
}

// saveCandidateInfoFeedback 校验岗位与运行归属后保存事件；仅简历请求更新索要时间，未匹配档案保留待补报。
func (s *PositionExecutionService) saveCandidateInfoFeedback(w http.ResponseWriter, r *http.Request, position Position, tenantID, runID string, items []CandidateInfoFeedback) {
	if len(items) > 100 {
		writeError(w, http.StatusBadRequest, "索要反馈数量过多")
		return
	}
	if runID != "" {
		if s.runStore == nil {
			writeError(w, http.StatusServiceUnavailable, "运行记录暂时不可核对")
			return
		}
		run, err := s.runStore.TaskRunByID(tenantID, runID)
		if err != nil || run.PositionID != position.ID {
			writeError(w, http.StatusBadRequest, "运行记录不属于当前岗位")
			return
		}
	}
	for _, item := range items {
		if strings.TrimSpace(item.RequestID) == "" || len(item.RequestID) > 100 || strings.TrimSpace(item.CandidateID) == "" || len(item.CandidateID) > 256 || item.Action != "phone" && item.Action != "wechat" && item.Action != "resume" || item.State != "requested" && item.State != "unknown" && item.State != "satisfied" {
			writeError(w, http.StatusBadRequest, "索要反馈身份、动作或状态不完整")
			return
		}
	}
	saved := 0
	for _, item := range items {
		e, err := s.candidateStore.FindEngagementByPlatformCandidate(position.ID, position.PlatformID, item.CandidateID)
		if err != nil {
			s.writeCandidateIngestLog(position.ID, position.UserEmail, "warning", "索要结果档案暂未匹配，等待按稳定 ID 补报："+item.CandidateID)
			continue
		}
		eventType := item.Action + "_request_" + item.State
		if item.State == "requested" {
			eventType = item.Action + "_requested"
		}
		_, err = s.candidateStore.SaveCandidateEvent(CandidateEvent{ID: candidateInfoEventID(position.ID, e.CandidateID, item), CandidateID: e.CandidateID, EngagementID: e.ID, TaskID: runID, PositionID: position.ID, PlatformID: position.PlatformID, EventType: eventType, Metadata: map[string]any{"source": "candidate_info_request", "request_id": item.RequestID, "state": item.State, "action": item.Action}})
		if err != nil {
			writeError(w, http.StatusInternalServerError, "索要事件暂未保存，请重试")
			return
		}
		if item.Action == "resume" && item.State == "requested" && e.ResumeRequestedAt == nil {
			now := time.Now().UTC()
			if err := s.candidateStore.UpdateCandidateEngagementStatus(e.ID, "", nil, nil, &now); err != nil {
				writeError(w, http.StatusInternalServerError, "简历索要时间暂未保存，请重试")
				return
			}
		}
		s.writeCandidateIngestLog(position.ID, position.UserEmail, "info", fmt.Sprintf("索要结果：候选人=%s，动作=%s，状态=%s", item.CandidateName, item.Action, item.State))
		saved++
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "saved": saved, "expected": len(items)})
}
