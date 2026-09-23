// 本文件提供候选人扫描记录的 HTTP 接口。
// 本地程序在打招呼和自动回复流程中上报候选人扫描结果，前端通过列表接口查看岗位下的扫描记录。
package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
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
