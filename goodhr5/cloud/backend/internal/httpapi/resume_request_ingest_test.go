// 本文件作用：验证云端"求简历"补报接口的姓名匹配、事件落库和参数校验。
package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
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
