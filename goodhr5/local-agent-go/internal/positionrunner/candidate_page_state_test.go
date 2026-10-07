// 本文件验证公共首次招呼流程读取页面事实后同步状态并跳过，模拟页面边界而不访问招聘网站。
package positionrunner

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"goodhr5/local-agent-go/internal/cloudapi"
	"goodhr5/local-agent-go/internal/platformcore"
)

// observedCandidateRuntime 复用既有平台测试能力，提供人工操作形成的确定页面状态。
type observedCandidateRuntime struct {
	candidateInfoSuccessRuntime
	state platformcore.CandidatePageState
}

// ReadCandidateState 返回模拟页面事实，不点击招呼按钮。
func (r *observedCandidateRuntime) ReadCandidateState(context.Context, platformcore.Executor, cloudapi.PlatformConfig, platformcore.Candidate) (platformcore.CandidatePageState, error) {
	return r.state, nil
}

// TestFirstGreetReconcilesManualAction 验证已沟通或已收简历时不发送，并且只同步稳定候选人 ID 的正向事实。
func TestFirstGreetReconcilesManualAction(t *testing.T) {
	for _, resume := range []string{"unknown", "received"} {
		t.Run(resume, func(t *testing.T) {
			var observed cloudapi.ScreeningRecord
			cloud := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				if req.Method == http.MethodGet {
					_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "item": nil})
					return
				}
				var payload struct {
					Items []cloudapi.ScreeningRecord `json:"items"`
				}
				_ = json.NewDecoder(req.Body).Decode(&payload)
				if len(payload.Items) == 1 {
					observed = payload.Items[0]
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
			}))
			defer cloud.Close()
			r, db := newTestRunnerWithDB(t, &fakeWorker{})
			position, err := db.CreatePosition(map[string]any{"name": "岗位", "platform_id": "boss"})
			if err != nil {
				t.Fatal(err)
			}
			runtime := &observedCandidateRuntime{state: platformcore.CandidatePageState{ContactObserved: true, ResumeStatus: resume}}
			candidate := map[string]any{"id": "stable-id", "candidate_name": "测试候选人", "status": "passed", "ai_greet_score": 88.0}
			greeted, failed, skipped, err := r.consumeCandidateForGreet(t.Context(), position, runtime, platformExecutor{runner: r, positionID: position.ID}, nil, candidate, 0, StartOptions{CloudAPIBase: cloud.URL, Token: "test"})
			if err != nil || greeted != 0 || failed != 0 || skipped != 1 || runtime.greetCalls != 0 {
				t.Fatalf("人工操作后仍发送或计入成功：%d %d %d %v", greeted, failed, skipped, err)
			}
			if observed.PlatformCandidateID != "stable-id" || observed.Source != "platform_observation" || !observed.ContactObserved || candidate["greeted_at"] != nil {
				t.Fatalf("同步错误或伪造时间：%+v %v", observed, candidate)
			}
		})
	}
}
