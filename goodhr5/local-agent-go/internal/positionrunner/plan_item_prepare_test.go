// 本文件用受控云端验证 HRPlus 计划项复用 M1 准备且不独立领取账号或操作招聘页面。
package positionrunner

import (
	"encoding/json"
	"goodhr5/local-agent-go/internal/cloudapi"
	"goodhr5/local-agent-go/internal/planmodel"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
)

// TestBeginPlanItemUsesParent 验证准备后继承原 TaskRun 与 M1 检查点，许可失效时不附加子项或操作页面。
func TestBeginPlanItemUsesParent(t *testing.T) {
	for _, scenario := range []string{"valid", "missing_task", "other_owner", "lost_authority"} {
		t.Run(scenario, func(t *testing.T) {
			raw, err := os.ReadFile(filepath.Join("..", "planmodel", "testdata", "permit.json"))
			if err != nil {
				t.Fatal(err)
			}
			var permit planmodel.Permit
			if err = json.Unmarshal(raw, &permit); err != nil {
				t.Fatal(err)
			}
			permit.Run.Snapshot.Items[0].Actions = []string{"greeting"}
			permit.Run.Snapshot.Items[0].PrioritizeReply = false
			permit.Run.Items[0].Snapshot = permit.Run.Snapshot.Items[0]
			permit.Run.Items[0].Actions = map[string]planmodel.ActionProgress{"greeting": {State: "pending"}}
			permit.Run.Items[0].TaskRunID = "70000000-0000-0000-0000-000000000001"
			if scenario == "missing_task" {
				permit.Run.Items[0].TaskRunID = ""
			}
			var current atomic.Bool
			current.Store(true)
			cloud := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
				var value any
				switch request.URL.Path {
				case "/api/auth/me":
					value = map[string]any{"ok": true, "user": map[string]any{"email": "fixture@example.com"}}
				case "/api/positions/same-job":
					value = map[string]any{"position": map[string]any{"id": "same-job", "name": "fixture", "platform_id": "boss", "common_config": map[string]any{"mode_default": "keyword", "detail_mode": "keyword"}}}
				case "/api/subscription/status":
					value = map[string]any{"subscription": map[string]any{"active": true}}
				case "/api/config/user-preferences":
					if scenario == "lost_authority" {
						current.Store(false)
					}
					value = map[string]any{"config": map[string]any{}}
				case "/api/platforms/config/":
					value = map[string]any{"configs": []map[string]any{{"config_key": "platform.boss", "config_value": `{"id":"boss","auth":{"pages":[{"url":"https://www.zhipin.com/web/chat/recommend","entry":true}]}}`}}}
				default:
					t.Error("计划项准备调用了独立启动或其他路径", request.URL.Path)
					w.WriteHeader(404)
					return
				}
				_ = json.NewEncoder(w).Encode(value)
			}))
			defer cloud.Close()
			worker := &fakeWorker{}
			runner := newTestRunner(t, openRunnerTestDB(t), worker)
			parent, err := runner.ReservePlanBrowser(t.Context(), permit.Run.ID)
			if err != nil {
				t.Fatal(err)
			}
			defer parent.Release(true)
			scope := cloudapi.SessionOwnerScope(cloud.URL, "fixture@example.com")
			if scenario == "other_owner" {
				scope = "other-owner"
			}
			child, snapshot, err := runner.BeginPlanItem(t.Context(), parent, scope, permit, StartOptions{Token: "fixture-token", CloudAPIBase: cloud.URL, MachineID: permit.Owner.MachineID, TaskType: "auto_reply"}, current.Load)
			if scenario == "valid" {
				if err != nil || child == nil || snapshot.Options.LocalRunID != permit.Run.Items[0].ID || snapshot.Options.TaskType != "greeting" || snapshot.Options.CloudRunID != permit.Run.Items[0].TaskRunID {
					t.Fatal("未使用原项、原任务和计划动作", err)
				}
				if err = child.ReleaseAfterCleanup(true); err != nil {
					t.Fatal(err)
				}
			} else if err == nil || child != nil {
				t.Fatal("不完整许可仍附加了子项")
			}
			if len(worker.calls) != 0 {
				t.Fatal("配置准备提前操作招聘页面", worker.calls)
			}
			if !parent.Valid() {
				t.Fatal("配置准备释放了父占用")
			}
		})
	}
}
