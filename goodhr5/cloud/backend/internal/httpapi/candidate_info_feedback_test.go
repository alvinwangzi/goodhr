// 本文件验证分项索要反馈按稳定 ID 保存、重复补报不重复记录，以及电话和微信的 Pro 权限。
package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
)

// TestCandidateInfoFeedbackDedup 验证同名档案不会错绑，电话结果不会更改简历索要时间，重复反馈只有一条事件。
func TestCandidateInfoFeedbackDedup(t *testing.T) {
	srv := mustNewServer(t)
	routes := srv.Routes()
	email := "contact-feedback@example.com"
	token := loginForTest(t, routes, email)
	id := createPositionWithConfigForTest(t, routes, token, "Go", `{"mode_default":"keyword"}`)
	store := srv.positionExecution.candidateStore
	for _, externalID := range []string{"a", "b"} {
		p, err := store.SaveCandidateProfile(CandidateProfileInput{UserEmail: email, PlatformID: "boss", PlatformCandidateID: externalID, CandidateName: "同名"})
		if err != nil {
			t.Fatal(err)
		}
		_, err = store.UpsertCandidateEngagement(CandidateEngagement{CandidateID: p.ID, PositionID: id, PlatformID: "boss", UserEmail: email})
		if err != nil {
			t.Fatal(err)
		}
	}
	body := `{"info_results":[{"request_id":"one","candidate_id":"a","candidate_name":"同名","action":"phone","state":"requested"}]}`
	for i := 0; i < 2; i++ {
		resp := postPositionExecutionForTest(t, routes, token, "/api/positions/"+id+"/resume-requests", body)
		if resp.Code != http.StatusOK {
			t.Fatalf("反馈失败：%s", resp.Body.String())
		}
	}
	tenant, _ := srv.positionExecution.getTenantInfo(email)
	e, err := store.FindEngagementByPlatformCandidate(id, "boss", "a")
	if err != nil {
		t.Fatal(err)
	}
	p, err := store.GetPositionCandidate(tenant, e.CandidateID, e.ID, email, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Events) != 1 || p.Events[0].EventType != "phone_requested" || p.ResumeRequestedAt != nil {
		t.Fatalf("电话反馈被当成简历或重复写入：%+v", p)
	}
	other, _ := store.FindEngagementByPlatformCandidate(id, "boss", "b")
	value, _ := store.GetPositionCandidate(tenant, other.CandidateID, other.ID, email, false)
	if len(value.Events) != 0 {
		t.Fatal("同名档案反馈错绑")
	}
}

// TestContactSelectionRequiresPro 验证两项新增配置不会绕过现有 Pro 保存权限。
func TestContactSelectionRequiresPro(t *testing.T) {
	for _, key := range []string{"request_phone", "request_wechat"} {
		t.Run(key, func(t *testing.T) {
			srv := mustNewServer(t)
			routes := srv.Routes()
			email := "plus-contact@example.com"
			token := loginForTest(t, routes, email)
			_, _ = srv.positionExecution.subscriptions.AdjustSubscriptionDays(email, memberTypePlus, 30)
			resp := postPositionExecutionForTest(t, routes, token, "/api/positions", fmt.Sprintf(`{"name":"Go","keywords":["Go"],"common_config":{"mode_default":"keyword","%s":true}}`, key))
			if resp.Code != http.StatusForbidden {
				t.Fatalf("Plus 保存了 Pro 索要配置：%d %s", resp.Code, resp.Body.String())
			}
		})
	}
}

// TestContactSelectionPersists 验证 Pro 可以独立保存电话和微信，简历保持未勾选。
func TestContactSelectionPersists(t *testing.T) {
	srv := mustNewServer(t)
	routes := srv.Routes()
	token := loginForTest(t, routes, "pro-contact@example.com")
	resp := postPositionExecutionForTest(t, routes, token, "/api/positions", `{"name":"Go","keywords":["Go"],"common_config":{"mode_default":"keyword","request_phone":true,"request_wechat":true,"request_resume":false}}`)
	if resp.Code != http.StatusOK {
		t.Fatalf("保存失败：%s", resp.Body.String())
	}
	var payload struct {
		Position struct {
			Common map[string]any `json:"common_config"`
		} `json:"position"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if payload.Position.Common["request_phone"] != true || payload.Position.Common["request_wechat"] != true || payload.Position.Common["request_resume"] != false {
		t.Fatalf("勾选保存错误：%+v", payload.Position.Common)
	}
}
