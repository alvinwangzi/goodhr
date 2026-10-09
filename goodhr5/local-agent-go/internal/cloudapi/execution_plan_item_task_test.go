// 本文件用受控 HTTP 验证 HRPlus 执行项准备身份、重试及凭证隐藏，不访问招聘页面。
package cloudapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestPrepareExecutionPlanItemTask 验证重复准备使用同一请求和父占用，错误许可不能提供任务编号。
func TestPrepareExecutionPlanItemTask(t *testing.T) {
	for _, scenario := range []string{"valid", "generic", "wrong_item", "missing_task", "wrong_machine", "released", "finished"} {
		t.Run(scenario, func(t *testing.T) {
			permit := cloudPlanFixture(t)
			input := PlanItemTaskRequest{RunID: permit.Run.ID, ItemRunID: permit.Run.Items[0].ID, RequestID: "60000000-0000-0000-0000-000000000003", OwnerID: permit.Owner.OwnerID, MachineID: permit.Owner.MachineID, Credential: strings.Repeat("fixture-secret", 4)}
			permit.Run.Items[0].TaskRunID = "70000000-0000-0000-0000-000000000001"
			permit.Run.Sequence++
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Method != http.MethodPost || r.URL.Path != "/api/execution-plan-runs/"+input.RunID+"/items/"+input.ItemRunID+"/prepare" || r.Header.Get("Authorization") != "Bearer fixture-token" {
					t.Error("没有使用原执行项路径或登录请求头")
				}
				var sent PlanItemTaskRequest
				if err := json.NewDecoder(r.Body).Decode(&sent); err != nil {
					t.Error(err)
				}
				if sent.RequestID != input.RequestID || sent.Credential != input.Credential || sent.OwnerID != input.OwnerID || sent.MachineID != input.MachineID {
					t.Error("重试改变原准备请求")
				}
				switch scenario {
				case "generic":
					_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
					return
				case "wrong_item":
					permit.Run.CurrentItem = 1
				case "missing_task":
					permit.Run.Items[0].TaskRunID = ""
				case "wrong_machine":
					permit.Owner.MachineID = "another-machine"
				case "released":
					permit.Owner.State = "released"
				case "finished":
					permit.Run.Items[0].State = "completed"
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "permit": permit})
			}))
			defer server.Close()
			for attempt := 0; attempt < 2; attempt++ {
				result, err := New(server.URL).PrepareExecutionPlanItemTask(t.Context(), "fixture-token", input)
				if scenario == "valid" {
					if err != nil || result.Run.Items[0].TaskRunID != permit.Run.Items[0].TaskRunID {
						t.Fatal("没有返回独立岗位任务", err)
					}
				} else if err == nil {
					t.Fatal("错误准备结果被当作许可")
				}
			}
			if calls != 2 {
				t.Fatal("没有实际重试原请求")
			}
			for _, format := range []string{"%v", "%+v", "%#v"} {
				if strings.Contains(fmt.Sprintf(format, input), input.Credential) {
					t.Fatal("日志输出了占用凭证")
				}
			}
		})
	}
}
