// 本文件使用只读测试服务器验证 HRPlus 计划许可身份、原请求重试、精确整数和错误分类。
package cloudapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"goodhr5/local-agent-go/internal/planmodel"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// cloudPlanFixture 读取安全许可夹具，不包含真实登录令牌或候选人资料。
func cloudPlanFixture(t *testing.T) planmodel.Permit {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "planmodel", "testdata", "permit.json"))
	if err != nil {
		t.Fatal(err)
	}
	var p planmodel.Permit
	if err = json.Unmarshal(raw, &p); err != nil {
		t.Fatal(err)
	}
	return p
}

// TestExecutionPlanClient 验证请求身份不变、许可匹配及大于浮点精确范围的状态和计数。
func TestExecutionPlanClient(t *testing.T) {
	permit := cloudPlanFixture(t)
	huge := int64(1<<53) + 17
	plan := planmodel.Plan{ID: permit.Run.PlanID, UserEmail: "fixture@example.com", MachineID: permit.Owner.MachineID, Version: 1, StateSequence: huge, ActivationID: permit.Run.ActivationID, State: "enabled", Config: permit.Run.Snapshot}
	plan.CreatedAt = time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC)
	plan.UpdatedAt = plan.CreatedAt
	claims := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer fixture-token" {
			t.Error("登录凭证没有使用请求头")
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/execution-plans":
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "plans": []planmodel.Plan{plan}})
		case "/api/execution-plan-runs/claim":
			var input PlanClaimRequest
			if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
				t.Error(err)
			}
			if input.RequestID != "60000000-0000-0000-0000-000000000001" || input.RunID != permit.Run.ID || input.Credential != strings.Repeat("fixture-secret", 4) || !input.LocalReserved {
				t.Error("重试改变原请求事实")
			}
			claims++
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "permit": permit})
		case "/api/execution-plan-runs/" + permit.Run.ID + "/status":
			var input PlanRunUpdateRequest
			if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
				t.Error(err)
			}
			if input.Sequence != huge {
				t.Error("上报序号丢失精度")
			}
			permit.Run.Sequence = input.Sequence
			permit.Run.State = input.State
			permit.Owner.State = "running"
			started := time.Now().UTC()
			permit.Run.StartedAt = &started
			permit.Run.Items[0].State = "running"
			permit.Run.Items[0].Actions["auto_reply"] = planmodel.ActionProgress{State: "active", Count: huge}
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "permit": permit})
		case "/api/execution-plan-runs/" + permit.Run.ID:
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "run": permit.Run})
		default:
			t.Error("错误计划路径", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	client := New(server.URL)
	plans, err := client.ListExecutionPlans(t.Context(), "fixture-token")
	if err != nil || len(plans) != 1 || plans[0].StateSequence != huge {
		t.Fatal("计划序号精度丢失", err)
	}
	claim := PlanClaimRequest{PlanID: permit.Run.PlanID, ActivationID: permit.Run.ActivationID, ExpectedVersion: 1, ExecutionDate: permit.Run.ExecutionDate, RunID: permit.Run.ID, RequestID: "60000000-0000-0000-0000-000000000001", MachineID: permit.Owner.MachineID, OwnerID: permit.Owner.OwnerID, Credential: strings.Repeat("fixture-secret", 4), LocalReserved: true}
	for i := 0; i < 2; i++ {
		if _, err = client.ClaimExecutionPlanRun(t.Context(), "fixture-token", claim); err != nil {
			t.Fatal(err)
		}
	}
	if claims != 2 {
		t.Fatal("原请求没有实际重试")
	}
	update := PlanRunUpdateRequest{RunID: permit.Run.ID, Action: "status", RequestID: "60000000-0000-0000-0000-000000000002", OwnerID: permit.Owner.OwnerID, MachineID: permit.Owner.MachineID, Credential: claim.Credential, Sequence: huge, State: "running"}
	result, err := client.UpdateExecutionPlanRun(t.Context(), "fixture-token", update)
	if err != nil || result.Run.Sequence != huge || result.Run.Items[0].Actions["auto_reply"].Count != huge {
		t.Fatal("许可序号或计数丢失精度", err)
	}
	saved, err := client.ExecutionPlanRun(t.Context(), "fixture-token", permit.Run.ID)
	if err != nil || saved.Sequence != huge {
		t.Fatal(err)
	}
	for _, value := range []any{claim, update} {
		for _, format := range []string{"%v", "%+v", "%#v"} {
			if strings.Contains(fmt.Sprintf(format, value), claim.Credential) {
				t.Fatal("日志输出凭证")
			}
		}
	}
}

// TestExecutionPlanClientRejectsAmbiguous 验证泛成功、不匹配占用与可分类等待都不会成为启动许可。
func TestExecutionPlanClientRejectsAmbiguous(t *testing.T) {
	p := cloudPlanFixture(t)
	input := PlanClaimRequest{PlanID: p.Run.PlanID, ActivationID: p.Run.ActivationID, ExpectedVersion: 1, ExecutionDate: p.Run.ExecutionDate, RunID: p.Run.ID, RequestID: "60000000-0000-0000-0000-000000000001", OwnerID: p.Owner.OwnerID, MachineID: p.Owner.MachineID, Credential: strings.Repeat("fixture-secret", 4), LocalReserved: true}
	for _, scenario := range []string{"generic", "wrong_owner", "busy", "expired"} {
		t.Run(scenario, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch scenario {
				case "generic":
					_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
				case "wrong_owner":
					p.Owner.MachineID = "other-machine"
					_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "permit": p})
				case "busy":
					w.WriteHeader(409)
					_ = json.NewEncoder(w).Encode(map[string]any{"error": "账号正在收尾", "error_code": "EXECUTION_BUSY"})
				case "expired":
					w.WriteHeader(401)
					_ = json.NewEncoder(w).Encode(map[string]any{"error": "登录过期"})
				}
			}))
			defer server.Close()
			_, err := New(server.URL).ClaimExecutionPlanRun(t.Context(), "fixture-token", input)
			if err == nil {
				t.Fatal("不明确响应被当成许可")
			}
			if scenario == "busy" {
				var classified PlanRequestError
				if !errors.As(err, &classified) || classified.Code != "EXECUTION_BUSY" || classified.Status != 409 {
					t.Fatal("等待错误分类丢失", err)
				}
			}
			if scenario == "expired" {
				var expired AuthExpiredError
				if !errors.As(err, &expired) {
					t.Fatal("过期未走统一登录错误", err)
				}
			}
		})
	}
}
