// 本文件作用：验证云端"求简历"补报接口的姓名匹配、事件落库和参数校验。
package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// TestNotifyResumeRequestsWritesEvents 验证补报按姓名匹配候选人并落 resume_requested 事件。
func TestNotifyResumeRequestsWritesEvents(t *testing.T) {
	server := mustNewServer(t)
	routes := server.Routes()
	token := loginForTest(t, routes, "resume-request@example.com")
	positionID := createPositionForTest(t, routes, token)

	// 先入库两个候选人，保证补报时有简历库记录可匹配。
	for _, name := range []string{"张三", "李四"} {
		body := `{"candidate_name":"` + name + `","status":"greeted","platform_id":"boss"}`
		req := httptest.NewRequest(http.MethodPost, "/api/positions/"+positionID+"/candidates", bytes.NewBufferString(body))
		req.Header.Set("Authorization", "Bearer "+token)
		resp := httptest.NewRecorder()
		routes.ServeHTTP(resp, req)
		if resp.Code != http.StatusOK {
			t.Fatalf("保存候选人 %s 失败：code=%d body=%s", name, resp.Code, resp.Body.String())
		}
	}

	// 补报：张三完成求简历，王五不在简历库中应列入 missing。
	req := httptest.NewRequest(http.MethodPost, "/api/positions/"+positionID+"/resume-requests", bytes.NewBufferString(`{"run_id":"run-1","names":["张三","王五"]}`))
	req.Header.Set("Authorization", "Bearer "+token)
	resp := httptest.NewRecorder()
	routes.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("resume-requests code = %d, body = %s", resp.Code, resp.Body.String())
	}
	var payload struct {
		OK      bool     `json:"ok"`
		Matched []string `json:"matched"`
		Missing []string `json:"missing"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if !payload.OK || len(payload.Matched) != 1 || payload.Matched[0] != "张三" || len(payload.Missing) != 1 || payload.Missing[0] != "王五" {
		t.Fatalf("payload = %+v", payload)
	}

	// 验证张三落了带执行任务 ID 的 resume_requested 事件。
	store, ok := server.positionExecution.candidateStore.(*MemoryCandidateStore)
	if !ok {
		t.Fatal("测试环境应使用内存候选人存储")
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	candidateID := ""
	for id, profile := range store.profiles {
		if profile.CandidateName == "张三" {
			candidateID = id
		}
	}
	if candidateID == "" {
		t.Fatal("没有找到张三的简历库记录")
	}
	resumeEvents := 0
	for _, event := range store.events[candidateID] {
		if event.EventType == "resume_requested" && event.TaskID == "run-1" && event.PositionID == positionID {
			resumeEvents++
		}
	}
	if resumeEvents != 1 {
		t.Fatalf("resume_requested 事件数 = %d", resumeEvents)
	}
}

// TestResumeTrackingCreatesAndAdvances 验证意向立即入库、重复与乱序同步不重复建档或回退状态。
func TestResumeTrackingCreatesAndTrackingAdvances(t *testing.T) {
	server := mustNewServer(t)
	routes := server.Routes()
	token := loginForTest(t, routes, "resume-tracking@example.com")
	positionID := createPositionForTest(t, routes, token)
	base := time.Now().UTC().Add(-time.Minute)
	post := func(state, failure string, revision int) {
		t.Helper()
		body, _ := json.Marshal(map[string]any{"candidate": map[string]any{
			"id": "chat-profile-a-person-1", "candidate_name": "测试候选人", "state": state,
			"score": 85, "reason": "匹配岗位", "error": failure,
			"updated_at": base.Add(time.Duration(revision)*time.Second).Format(time.RFC3339Nano),
		}})
		req := httptest.NewRequest(http.MethodPost, "/api/positions/"+positionID+"/resume-requests", bytes.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+token)
		resp := httptest.NewRecorder()
		routes.ServeHTTP(resp, req)
		if resp.Code != http.StatusOK { t.Fatalf("跟踪同步失败：%d %s", resp.Code, resp.Body.String()) }
	}
	read := func(wantState, wantError string) map[string]any {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/api/candidates?position_id="+positionID, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		resp := httptest.NewRecorder()
		routes.ServeHTTP(resp, req)
		var body struct { Candidates []map[string]any `json:"candidates"`; Total int `json:"total"` }
		if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil { t.Fatal(err) }
		if resp.Code != 200 || body.Total != 1 || len(body.Candidates) != 1 { t.Fatalf("简历库应有唯一档案：%s", resp.Body.String()) }
		item := body.Candidates[0]
		if item["resume_state"] != wantState || item["resume_error"] != wantError { t.Fatalf("进度不正确：%+v", item) }
		return item
	}
	post("pending", "", 1)
	first := read("pending", "")
	if first["resume_requested_at"] != nil { t.Fatal("仅决定索要，不能标记已索要") }
	post("pending", "索要确认失败", 2)
	read("pending", "索要确认失败")
	post("requested", "", 3)
	read("requested", "")
	post("received", "下载失败", 4)
	read("received", "下载失败")
	post("downloaded", "", 5)
	post("downloaded", "", 5)
	post("pending", "过期失败", 2)
	last := read("downloaded", "")
	if last["id"] != first["id"] { t.Fatal("重复同步重新创建了候选人") }
	store := server.positionExecution.candidateStore.(*MemoryCandidateStore)
	store.mu.Lock()
	defer store.mu.Unlock()
	if len(store.engagements) != 1 { t.Fatalf("岗位关系重复：%d", len(store.engagements)) }
	if len(store.events[first["id"].(string)]) != 5 { t.Fatal("事件重复或缺失") }
}

// TestResumeTrackingRejectsInvalidIdentity 验证只允许明确身份、有效状态和时间的跟踪记录。
func TestResumeTrackingRejectsInvalidIdentity(t *testing.T) {
	server := mustNewServer(t)
	routes := server.Routes()
	token := loginForTest(t, routes, "resume-tracking-validation@example.com")
	positionID := createPositionForTest(t, routes, token)
	for _, candidate := range []map[string]any{
		{"candidate_name": "同名", "state": "pending", "updated_at": time.Now().Format(time.RFC3339Nano)},
		{"id": "one", "state": "scanned", "updated_at": time.Now().Format(time.RFC3339Nano)},
		{"id": "one", "state": "pending"},
	} {
		body, _ := json.Marshal(map[string]any{"candidate": candidate})
		req := httptest.NewRequest(http.MethodPost, "/api/positions/"+positionID+"/resume-requests", bytes.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+token)
		resp := httptest.NewRecorder()
		routes.ServeHTTP(resp, req)
		if resp.Code != http.StatusBadRequest { t.Fatalf("应拒绝不完整身份或状态：%d", resp.Code) }
	}
}

// TestNotifyResumeRequestsRejectsEmptyNames 验证名单为空时返回参数错误。
func TestNotifyResumeRequestsRejectsEmptyNames(t *testing.T) {
	server := mustNewServer(t)
	routes := server.Routes()
	token := loginForTest(t, routes, "resume-request-empty@example.com")
	positionID := createPositionForTest(t, routes, token)
	req := httptest.NewRequest(http.MethodPost, "/api/positions/"+positionID+"/resume-requests", bytes.NewBufferString(`{"run_id":"run-1","names":[]}`))
	req.Header.Set("Authorization", "Bearer "+token)
	resp := httptest.NewRecorder()
	routes.ServeHTTP(resp, req)
	if resp.Code != http.StatusBadRequest {
		t.Fatalf("code = %d, body = %s", resp.Code, resp.Body.String())
	}
}
