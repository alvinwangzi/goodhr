// 本文件提供候选人扫描记录的 HTTP 接口。
// 本地程序在打招呼和自动回复流程中上报候选人扫描结果，前端通过列表接口查看岗位下的扫描记录。
package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"
)

// candidateScreeningUpsertRequest 表示批量上报扫描记录的请求体。
type candidateScreeningUpsertRequest struct {
	Items []CandidateScreeningUpsert `json:"items"`
}

// UpsertScreenings 批量上报候选人扫描记录。
// 路径格式：/api/positions/{positionID}/screenings
func (s *PositionExecutionService) UpsertScreenings(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	session, ok := s.currentSession(w, r)
	if !ok {
		return
	}
	if s.screeningStore == nil {
		writeError(w, http.StatusInternalServerError, "screening store is not ready")
		return
	}
	positionID := positionSubresourceScreeningID(r.URL.Path)
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
	var req candidateScreeningUpsertRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json body")
		return
	}
	if len(req.Items) == 0 {
		writeError(w, http.StatusBadRequest, "items 不能为空")
		return
	}
	saved := make([]CandidateScreening, 0, len(req.Items))
	for _, item := range req.Items {
		if err := validateScreeningUpsert(item); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		// 打招呼流程（source='greeting' 且 status='passed'）上报时自动标记 greeted_at，
		// 其他流程（auto_reply / re_greet）不上报 greeted_at，避免覆盖原值。
		if item.Source == "greeting" && item.Status == "passed" {
			item.SetGreetedAt = true
		}
		result, err := s.screeningStore.UpsertScreening(r.Context(), item, position.ID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to upsert screening: "+err.Error())
			return
		}
		saved = append(saved, *result)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":    true,
		"items": saved,
	})
}

// FindScreening 查询单个候选人的扫描记录。
// 路径格式：/api/positions/{positionID}/screenings/find?platform=boss&candidate_id=xxx 或 ?platform=boss&name=张三
func (s *PositionExecutionService) FindScreening(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	session, ok := s.currentSession(w, r)
	if !ok {
		return
	}
	if s.screeningStore == nil {
		writeError(w, http.StatusInternalServerError, "screening store is not ready")
		return
	}
	positionID := positionSubresourceScreeningID(r.URL.Path)
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
	platform := strings.TrimSpace(r.URL.Query().Get("platform"))
	candidateID := strings.TrimSpace(r.URL.Query().Get("candidate_id"))
	candidateName := strings.TrimSpace(r.URL.Query().Get("name"))
	if platform == "" || (candidateID == "" && candidateName == "") {
		writeError(w, http.StatusBadRequest, "platform 和 candidate_id（或 name）不能为空")
		return
	}
	var item *CandidateScreening
	if candidateID != "" {
		item, err = s.screeningStore.FindScreening(r.Context(), position.ID, platform, candidateID)
	} else {
		item, err = s.screeningStore.FindScreeningByName(r.Context(), position.ID, platform, candidateName)
	}
	if errors.Is(err, ErrNotFound) {
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":   true,
			"item": nil,
		})
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to find screening")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":   true,
		"item": item,
	})
}

// ListScreenings 按岗位分页返回扫描记录。
// 路径格式：/api/positions/{positionID}/screenings?limit=20&offset=0
func (s *PositionExecutionService) ListScreenings(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	session, ok := s.currentSession(w, r)
	if !ok {
		return
	}
	if s.screeningStore == nil {
		writeError(w, http.StatusInternalServerError, "screening store is not ready")
		return
	}
	positionID := positionSubresourceScreeningID(r.URL.Path)
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
	limit := 20
	if v := strings.TrimSpace(r.URL.Query().Get("limit")); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			limit = n
		}
	}
	offset := 0
	if v := strings.TrimSpace(r.URL.Query().Get("offset")); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			offset = n
		}
	}
	items, total, err := s.screeningStore.ListScreeningsByPosition(r.Context(), position.ID, limit, offset)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list screenings")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":     true,
		"items":  items,
		"total":  total,
		"limit":  limit,
		"offset": offset,
	})
}

// reGreetCandidatesRequest 表示查询复打招呼候选名单的请求体。
type reGreetCandidatesRequest struct {
	Platform           string `json:"platform"`
	TimeRangeDays      int    `json:"time_range_days"`
	IntervalMinMinutes int    `json:"interval_min_minutes"`
	MaxCount           int    `json:"max_count"`
}

// ListReGreetCandidates 查询复打招呼候选名单。
// 路径格式：/api/positions/{positionID}/re-greet-candidates
func (s *PositionExecutionService) ListReGreetCandidates(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	session, ok := s.currentSession(w, r)
	if !ok {
		return
	}
	if s.screeningStore == nil {
		writeError(w, http.StatusInternalServerError, "screening store is not ready")
		return
	}
	positionID := positionSubresourceReGreetID(r.URL.Path)
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
	var req reGreetCandidatesRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json body")
		return
	}
	platform := strings.TrimSpace(req.Platform)
	if platform == "" {
		platform = "boss"
	}
	if req.TimeRangeDays <= 0 {
		req.TimeRangeDays = 7
	}
	if req.IntervalMinMinutes <= 0 {
		req.IntervalMinMinutes = 30
	}
	if req.MaxCount <= 0 {
		req.MaxCount = 1
	}
	items, err := s.screeningStore.ListReGreetCandidates(r.Context(), position.ID, platform, req.TimeRangeDays, req.IntervalMinMinutes, req.MaxCount)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list re-greet candidates: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":    true,
		"items": items,
	})
}

// reportReGreetRequest 表示复打招呼结果上报的请求体。
type reportReGreetRequest struct {
	Platform            string   `json:"platform"`
	PlatformCandidateID string   `json:"platform_candidate_id"`
	CandidateName       string   `json:"candidate_name"`
	Success             bool     `json:"success"`
	Reason              string   `json:"reason,omitempty"`
	SkippedIDs          []string `json:"skipped_ids,omitempty"`
	MessageText         string   `json:"message_text,omitempty"`
	RunID               string   `json:"run_id,omitempty"`
}

// ReportReGreet 上报单个候选人的复打招呼结果。
// 成功时云端将 last_re_greeted_at 更新为 now() 并对 re_greet_count +1；失败时仅记录日志不更新计数。
// 路径格式：/api/positions/{positionID}/re-greet-report
func (s *PositionExecutionService) ReportReGreet(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	session, ok := s.currentSession(w, r)
	if !ok {
		return
	}
	if s.screeningStore == nil {
		writeError(w, http.StatusInternalServerError, "screening store is not ready")
		return
	}
	positionID := positionSubresourceReGreetID(r.URL.Path)
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
	var req reportReGreetRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json body")
		return
	}
	platform := strings.TrimSpace(req.Platform)
	if platform == "" {
		platform = "boss"
	}
	candidateID := strings.TrimSpace(req.PlatformCandidateID)
	if candidateID == "" {
		writeError(w, http.StatusBadRequest, "platform_candidate_id 不能为空")
		return
	}
	if utf8.RuneCountInString(req.MessageText) > 200 || utf8.RuneCountInString(req.Reason) > 500 {
		writeError(w, http.StatusBadRequest, "复打内容或原因过长")
		return
	}
	// 拒绝上报不存在或其他岗位的候选人，记录归属只使用稳定 ID。
	screening, findErr := s.screeningStore.FindScreening(r.Context(), position.ID, platform, candidateID)
	if errors.Is(findErr, ErrNotFound) {
		writeError(w, http.StatusNotFound, "候选人不在当前岗位名单中")
		return
	}
	if findErr != nil {
		writeError(w, http.StatusInternalServerError, "无法读取候选人记录")
		return
	}
	runID := strings.TrimSpace(req.RunID)
	if runID != "" {
		if s.runStore == nil {
			writeError(w, http.StatusServiceUnavailable, "无法核对执行任务记录")
			return
		}
		run, err := s.runStore.TaskRunByID(tenantID, runID)
		if err != nil || run.PositionID != position.ID {
			writeError(w, http.StatusBadRequest, "执行任务记录不属于当前岗位")
			return
		}
	}
	var affected int64
	if req.Success {
		affected, err = s.screeningStore.MarkReGreetDone(r.Context(), position.ID, platform, []string{candidateID})
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to mark re-greet done: "+err.Error())
			return
		}
	}
	// 复用岗位日志保存所有结果；候选人已入简历库时，同时写入其事件时间线。
	eventType := "re_greet_failed"
	if req.Success {
		eventType = "re_greeted_sent"
	} else if strings.HasPrefix(req.Reason, "skipped_") || req.Reason == "ai_skip" || req.Reason == "context_changed" {
		eventType = "re_greet_" + req.Reason
	}
	message := fmt.Sprintf("复打结果：候选人=%s，平台ID=%s，事件=%s，原因=%s", screening.CandidateName, candidateID, eventType, req.Reason)
	if req.Success && req.MessageText != "" {
		message += "，发送内容=" + req.MessageText
	}
	if err := s.positionLogs.WriteLog(position.ID, position.UserEmail, "info", message); err != nil {
		writeError(w, http.StatusInternalServerError, "复打结果日志保存失败")
		return
	}
	if s.candidateStore != nil {
		e, err := s.candidateStore.FindEngagementByPlatformCandidate(position.ID, platform, candidateID)
		if err == nil {
			_, err = s.candidateStore.SaveCandidateEvent(CandidateEvent{CandidateID: e.CandidateID, EngagementID: e.ID, TaskID: runID, PositionID: position.ID, PlatformID: platform, EventType: eventType, Reason: req.Reason, MessageText: req.MessageText, Metadata: map[string]any{"source": "re_greet", "platform_candidate_id": candidateID}})
		}
		if err != nil && !errors.Is(err, ErrNotFound) {
			writeError(w, http.StatusInternalServerError, "复打候选人事件保存失败")
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":       true,
		"affected": affected,
	})
}

// positionSubresourceReGreetID 从复打招呼相关路径中提取岗位 ID。
// 支持 /api/positions/{id}/re-greet-candidates 与 /api/positions/{id}/re-greet-report 两种形式。
func positionSubresourceReGreetID(path string) string {
	text := strings.Trim(strings.TrimPrefix(path, "/api/positions/"), "/")
	parts := strings.Split(text, "/")
	if len(parts) >= 2 && (parts[1] == "re-greet-candidates" || parts[1] == "re-greet-report") {
		return strings.TrimSpace(parts[0])
	}
	return ""
}
