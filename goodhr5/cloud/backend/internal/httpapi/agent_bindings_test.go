// 本文件验证 HRPlus 设备选择只读取当前账号的实际有效绑定，内存与独立 PostgreSQL 使用同一契约。
package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// testBindingChoices 验证多电脑顺序、已解绑过滤、账号隔离和副作用隔离。
func testBindingChoices(t *testing.T, store AgentStore) {
	t.Helper()
	id, _ := newExecutionPlanID()
	email := id + "@example.com"
	for _, binding := range []AgentBinding{{UserEmail: email, MachineID: "computer-A", PublicKey: "private-fixture-key", AgentVersion: "1"}, {UserEmail: email, MachineID: "computer-B", AgentVersion: "2"}, {UserEmail: email, MachineID: "old-computer", BindStatus: "disabled"}, {UserEmail: "other-" + email, MachineID: "other-computer"}} {
		if _, err := store.SaveBinding(binding); err != nil {
			t.Fatal(err)
		}
	}
	items, err := store.ListBindings(strings.ToUpper(email))
	if err != nil || len(items) != 2 || items[0].MachineID != "computer-B" {
		t.Fatal("未保留有效多电脑或顺序错误", items, err)
	}
	items[0].MachineID = "modified"
	again, err := store.ListBindings(email)
	if err != nil || len(again) != 2 || again[0].MachineID != "computer-B" {
		t.Fatal("修改读取结果改变绑定", err)
	}
	missing, err := store.ListBindings("missing-" + email)
	if err != nil || missing == nil || len(missing) != 0 {
		t.Fatal("无绑定没有返回空列表", missing, err)
	}
	wrapped, err := NewPermissiveAgentStore(store).ListBindings(email)
	if err != nil || len(wrapped) != 2 {
		t.Fatal("开发包装伪造电脑列表", wrapped, err)
	}
	server := mustNewServer(t)
	server.agent.store = store
	routes := server.Routes()
	token := loginForTest(t, routes, email)
	req := httptest.NewRequest(http.MethodGet, "/api/agents/bindings?email=other-"+email, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	response := httptest.NewRecorder()
	routes.ServeHTTP(response, req)
	var result struct {
		Agents []struct {
			MachineID string `json:"machine_id"`
		} `json:"agents"`
	}
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &result) != nil || len(result.Agents) != 2 {
		t.Fatal("列表接口未返回真实账号电脑", response.Body.String())
	}
	for _, hidden := range []string{"other-computer", "public_key", "user_email", "private-fixture-key", "online"} {
		if strings.Contains(response.Body.String(), hidden) {
			t.Fatal("接口泄露数据或虚构在线状态", hidden)
		}
	}
	unauth := httptest.NewRecorder()
	routes.ServeHTTP(unauth, httptest.NewRequest(http.MethodGet, "/api/agents/bindings", nil))
	if unauth.Code != 401 {
		t.Fatal("未登录仍可读取电脑")
	}
}

// TestAgentBindingChoicesStores 对内存与真实独立数据库重复验证电脑列表契约。
func TestAgentBindingChoicesStores(t *testing.T) {
	t.Run("memory", func(t *testing.T) {
		store := NewMemoryAgentStore()
		step := 0
		store.now = func() time.Time { step++; return time.Date(2026, 10, 10, 9, 0, step, 0, time.UTC) }
		testBindingChoices(t, store)
	})
	t.Run("postgres", func(t *testing.T) { testBindingChoices(t, NewPostgresAgentStore(planPostgresFixture(t))) })
}
