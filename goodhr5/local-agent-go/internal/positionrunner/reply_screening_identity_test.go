// 本文件验证 HRPlus 回复前的历史评分须与当前真实会话 ID 一致，不能用同名候选人的评分绕过资料核对。
package positionrunner

import (
	"context"
	"encoding/json"
	"goodhr5/local-agent-go/internal/cloudapi"
	"goodhr5/local-agent-go/internal/platformcore"
	"net/http"
	"net/http/httptest"
	"testing"
)

// screeningIdentityFixture 模拟只读映射证明，记录调用是否试图按姓名搜索或切换页面。
type screeningIdentityFixture struct {
	*replyFixture
	conversationID string
	lookupName     string
}

// ResolveCandidateConversationID 返回记录对应的实际会话，姓名参数应为空，避免改变当前面板。
func (f *screeningIdentityFixture) ResolveCandidateConversationID(_ context.Context, _ platformcore.Executor, _ string, name string) (string, string, error) {
	f.lookupName = name
	return f.conversationID, "controlled-id-proof", nil
}

// TestReplyHistoricalScreeningIdentity 验证匹配时复用评分，不匹配时对当前候选人重新核对。
func TestReplyHistoricalScreeningIdentity(t *testing.T) {
	for _, matched := range []bool{false, true} {
		t.Run(map[bool]string{false: "同名不同ID", true: "明确匹配ID"}[matched], func(t *testing.T) {
			flow, fixture, current := newReplyFlowFixture(t)
			flow.legacyScope = "old-scope"
			flow.positionSnapshot = map[string]any{"name": "Go"}
			current.Conversation.Name = "张三"
			identity := &screeningIdentityFixture{replyFixture: fixture, conversationID: "other-conversation"}
			if matched {
				identity.conversationID = current.Conversation.ID
			}
			flow.runtime = identity
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/api/positions/position1/screenings/find" {
					_ = json.NewEncoder(w).Encode(map[string]any{"item": map[string]any{"position_id": "position1", "platform": "boss", "platform_candidate_id": "opaque", "candidate_name": "张三", "score": 90}})
					return
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
			}))
			defer server.Close()
			flow.cloudClient = cloudapi.New(server.URL)
			flow.token = "fixture-token"
			outcome, err := flow.process(t.Context(), current)
			if err != nil || outcome != "sent" {
				t.Fatalf("outcome=%s err=%v", outcome, err)
			}
			if identity.lookupName != "" {
				t.Fatal("评分核对按姓名切换了当前面板")
			}
			if matched && fixture.reviewCalls != 0 {
				t.Fatal("已匹配ID没有复用评分")
			}
			if !matched && (fixture.reviewCalls != 1 || fixture.generations != 0) {
				t.Fatalf("误用了同名人的高分 review=%d generate=%d", fixture.reviewCalls, fixture.generations)
			}
		})
	}
}
