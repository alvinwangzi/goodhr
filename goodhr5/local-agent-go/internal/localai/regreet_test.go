// 本文件验证 HRPlus 复打 AI 配置开关、提示词和输出校验，使用模拟 HTTP 服务。
package localai

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"goodhr5/local-agent-go/internal/localdb"
)

// TestGenerateReGreetRefusalSwitch 验证拒绝检测开关控制提示词和决策，候选人姓名不会被当作岗位名。
func TestGenerateReGreetRefusalSwitch(t *testing.T) {
	for _, skip := range []bool{false, true} {
		t.Run(map[bool]string{true: "关闭拒绝检测", false: "开启拒绝检测"}[skip], func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body struct {
					Messages []struct{ Content string } `json:"messages"`
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				if len(body.Messages) != 2 || !strings.Contains(body.Messages[0].Content, "岗位复打规则") || !strings.Contains(body.Messages[1].Content, "候选人：测试候选人") || strings.Contains(body.Messages[1].Content, "岗位：测试候选人") {
					t.Error("复打配置或动态信息不正确")
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"content": `{"should_send":true,"message":"再次沟通岗位","is_refused":true,"refuse_reason":"不考虑"}`}}}})
			}))
			defer server.Close()
			client := New(localdb.AIConfig{BaseURL: server.URL, APIKey: "test", Model: "test"})
			decision, err := client.GenerateReGreet(t.Context(), ReGreetRequest{ReGreetPrompt: "岗位复打规则", CandidateName: "测试候选人", SkipRefusedCheck: skip})
			if err != nil {
				t.Fatal(err)
			}
			if decision.ShouldSend != skip || decision.IsRefused == skip {
				t.Fatalf("拒绝开关未生效：%+v", decision)
			}
		})
	}
}
