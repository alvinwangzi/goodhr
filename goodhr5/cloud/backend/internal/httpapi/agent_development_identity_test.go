// 本文件验证 HRPlus 开发绑定冲突后仍使用物理电脑领取计划，内部替代键不成为执行目标。
package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// testDevelopmentPhysicalDevice 验证同电脑两账号、重复绑定、重建包装及原物理设备的计划许可。
func testDevelopmentPhysicalDevice(t *testing.T, inner AgentStore, plans ExecutionPlanStore) {
	t.Helper()
	id, _ := newExecutionPlanID()
	physical := stableAgentMachineIDPrefix + strings.ReplaceAll(id, "-", "")[:16]
	alpha, beta := "alpha-"+id+"@example.com", "beta-"+id+"@example.com"
	if _, err := inner.SaveBinding(AgentBinding{UserEmail: alpha, MachineID: physical}); err != nil {
		t.Fatal(err)
	}
	wrapped := NewPermissiveAgentStore(inner)
	for range 2 {
		binding, err := wrapped.SaveBinding(AgentBinding{UserEmail: beta, MachineID: physical, AgentVersion: "test-version", PublicKey: "fixture-public-key"})
		if err != nil || binding.MachineID != physical {
			t.Fatal("绑定回执改变物理编号", binding, err)
		}
	}
	stored, err := inner.ListBindings(beta)
	if err != nil || len(stored) != 1 || stored[0].MachineID == physical {
		t.Fatal("重复绑定积累替代记录或修改严格占用", stored, err)
	}
	restored := NewPermissiveAgentStore(inner)
	choices, err := restored.ListBindings(beta)
	if err != nil || len(choices) != 1 || choices[0].MachineID != physical {
		t.Fatal("重建后物理映射丢失", choices, err)
	}
	current, err := restored.CurrentBinding(beta)
	if err != nil || current.MachineID != physical {
		t.Fatal("最近设备仍为存储别名", current, err)
	}
	if _, err := inner.SaveBinding(AgentBinding{UserEmail: beta, MachineID: physical}); err == nil {
		t.Fatal("开发映射放宽严格绑定限制")
	} else {
		var conflict *AgentBindingConflictError
		if !errors.As(err, &conflict) {
			t.Fatal(err)
		}
	}
	config := postgresPlanConfig()
	tenantID := ""
	var tasks TaskRunStore
	var position Position
	switch store := plans.(type) {
	case *MemoryExecutionPlanStore:
		store.positions = NewMemoryPositionStore()
		store.taskRuns = NewMemoryTaskRunStore()
		tasks = store.taskRuns
		position, err = store.positions.SavePosition(Position{UserEmail: beta, Name: "开发设备测试岗位", PlatformID: "boss"})
	case *PostgresExecutionPlanStore:
		tenant, tenantErr := NewPostgresTenantStore(store.db).GetOrCreateTenant(beta)
		if tenantErr != nil {
			t.Fatal(tenantErr)
		}
		tenantID = tenant.ID
		position, err = NewPostgresPositionStore(store.db).SavePosition(Position{UserEmail: beta, Name: "开发设备测试岗位", PlatformID: "boss"})
		tasks = NewPostgresTaskRunStore(store.db)
	}
	if err != nil {
		t.Fatal(err)
	}
	for index := range config.Items {
		config.Items[index].PositionID = position.ID
	}
	plan, err := plans.Save(t.Context(), ExecutionPlan{TenantID: tenantID, UserEmail: beta, MachineID: choices[0].MachineID, Config: config}, 0)
	if err != nil {
		t.Fatal(err)
	}
	plan, err = plans.Intent(t.Context(), tenantID, beta, plan.ID, planIntentFixture(t, "arm"))
	if err != nil {
		t.Fatal(err)
	}
	claim := planRunClaimFixture(t, plan)
	claim.MachineID = physical
	permit, err := plans.ClaimRun(t.Context(), tenantID, beta, claim)
	if err != nil || permit.Run.ID != claim.RunID || permit.Run.Snapshot.Items[0].ID != plan.Config.Items[0].ID {
		t.Fatal("物理电脑无法领取原计划", err)
	}
	prepared, err := plans.PrepareItemTask(t.Context(), tenantID, beta, itemTaskRequestFixture(t, claim, permit.Run.Items[0].ID))
	if err != nil {
		t.Fatal("原物理设备无法准备执行项", err)
	}
	task, err := tasks.TaskRunByID(tenantID, prepared.Run.Items[0].TaskRunID)
	if err != nil || task.MachineID != physical || task.Status != "starting" {
		t.Fatal("执行项任务使用存储别名或提前运行", task, err)
	}
	server := mustNewServer(t)
	server.agent.store = restored
	routes := server.Routes()
	token := loginForTest(t, routes, beta)
	raw, _ := json.Marshal(map[string]string{"machine_id": physical, "public_key": "fixture-public-key"})
	for _, path := range []string{"/api/agents/bind", "/api/agents/current", "/api/agents/bindings"} {
		method := http.MethodGet
		if strings.HasSuffix(path, "/bind") {
			method = http.MethodPost
		}
		req := httptest.NewRequest(method, path, bytes.NewReader(raw))
		req.Header.Set("Authorization", "Bearer "+token)
		response := httptest.NewRecorder()
		routes.ServeHTTP(response, req)
		var result struct {
			Agent struct {
				MachineID string `json:"machine_id"`
			} `json:"agent"`
			Agents []struct {
				MachineID string `json:"machine_id"`
			} `json:"agents"`
		}
		if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &result) != nil {
			t.Fatal("HTTP 回执与本地会话物理编号不一致", path, response.Body.String())
		}
		if path == "/api/agents/bindings" {
			if len(result.Agents) != 1 || result.Agents[0].MachineID != physical {
				t.Fatal("网页设备选择仍使用内部键", response.Body.String())
			}
		} else if result.Agent.MachineID != physical {
			t.Fatal("绑定和最近设备仍使用内部键", response.Body.String())
		}
	}
}

// TestDevelopmentPhysicalDeviceStores 同一契约同时检查内存和独立 PostgreSQL 的持久替代键。
func TestDevelopmentPhysicalDeviceStores(t *testing.T) {
	t.Run("memory", func(t *testing.T) {
		testDevelopmentPhysicalDevice(t, NewMemoryAgentStore(), NewMemoryExecutionPlanStore())
	})
	t.Run("postgres", func(t *testing.T) {
		db := planPostgresFixture(t)
		testDevelopmentPhysicalDevice(t, NewPostgresAgentStore(db), NewPostgresExecutionPlanStore(db))
	})
}

// TestDevelopmentBindingUnknownKeys 验证错误账号、损坏编码及旧时间戳别名不能被猜成真实电脑。
func TestDevelopmentBindingUnknownKeys(t *testing.T) {
	physical := stableAgentMachineIDPrefix + "0123456789abcdef"
	binding := AgentBinding{UserEmail: "beta@example.com", MachineID: developmentBindingKey("beta@example.com", physical)}
	if resolved, ok := physicalDevelopmentBinding(binding); !ok || resolved.MachineID != physical {
		t.Fatal("原键不能还原")
	}
	for _, key := range []string{developmentBindingKey("other@example.com", physical), stableAgentMachineIDPrefix + "dev-123", stableAgentMachineIDPrefix + "dev2-%%%." + developmentBindingAccount(binding.UserEmail)} {
		binding.MachineID = key
		if _, ok := physicalDevelopmentBinding(binding); ok {
			t.Fatal("错误或旧键被猜成设备", key)
		}
	}
	inner := NewMemoryAgentStore()
	if _, err := inner.SaveBinding(AgentBinding{UserEmail: binding.UserEmail, MachineID: stableAgentMachineIDPrefix + "dev-123"}); err != nil {
		t.Fatal(err)
	}
	wrapped := NewPermissiveAgentStore(inner)
	choices, err := wrapped.ListBindings(binding.UserEmail)
	if err != nil || len(choices) != 0 {
		t.Fatal("旧未知编号成为执行电脑选项", choices, err)
	}
	if _, err := wrapped.CurrentBinding(binding.UserEmail); !errors.Is(err, ErrNotFound) {
		t.Fatal("旧未知编号假装可用", err)
	}
	stored, err := inner.ListBindings(binding.UserEmail)
	if err != nil || len(stored) != 1 || stored[0].MachineID != stableAgentMachineIDPrefix+"dev-123" {
		t.Fatal("读取改写旧设备归属", stored, err)
	}
}
