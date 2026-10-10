// 本文件仅在显式联调开关下开放隔离测试云端，供 HRPlus 真实本地 SDK 验证 PostgreSQL 原报告。
package httpapi

import (
	"bufio"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
)

// TestExecutionPlanSDKBridgeHelper 初始化专用数据库与虚构账号，通过真实路由处理 SDK，结束后删除自建数据库。
func TestExecutionPlanSDKBridgeHelper(t *testing.T) {
	if os.Getenv("HRPLUS_M2_SDK_BRIDGE_HELPER") != "1" {
		t.Skip("仅用于显式跨模块联调")
	}
	output := os.Getenv("HRPLUS_M2_SDK_BRIDGE_FILE")
	if output == "" {
		t.Fatal("缺少隔离联调文件")
	}
	t.Setenv("GOODHR_APP_ENV", "dev")
	t.Setenv("GOODHR_PG_DSN", "")
	t.Setenv("GOODHR_REDIS_ADDR", "")
	server := mustNewServer(t)
	store := reportRecoveryPostgres(t)
	server.auth.tenantStore = NewPostgresTenantStore(store.(*PostgresExecutionPlanStore).db)
	server.executionPlans.agents = NewPostgresAgentStore(store.(*PostgresExecutionPlanStore).db)
	server.executionPlans.store = store
	server.executionPlans.execution.mailer = DevMailer{}
	email := "m2-sdk-bridge@example.com"
	token := loginForTest(t, server.Routes(), email)
	tenant, err := server.auth.tenantStore.GetOrCreateTenant(email)
	if err != nil {
		t.Fatal(err)
	}
	machineA, machineB := "goodhr-device-v1-sdk-bridge-A", "goodhr-device-v1-sdk-bridge-B"
	for _, machine := range []string{machineA, machineB} {
		if _, err := server.executionPlans.agents.SaveBinding(AgentBinding{UserEmail: email, MachineID: machine, BindStatus: "active"}); err != nil {
			t.Fatal(err)
		}
	}
	plan, summary := endedReportFixtureOwned(t, store, email, machineA, tenant.ID)
	var mutationMu sync.Mutex
	mux := http.NewServeMux()
	mux.HandleFunc("/__fixture/switch-device", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.Header.Get("Authorization") != "Bearer "+token {
			w.WriteHeader(403)
			return
		}
		mutationMu.Lock()
		defer mutationMu.Unlock()
		request, _ := newExecutionPlanID()
		stopped, err := store.Intent(r.Context(), tenant.ID, email, plan.ID, ExecutionPlanIntent{RequestID: request, Action: "stop", ExpectedVersion: plan.Version, ActivationID: plan.ActivationID})
		if err != nil || stopped.StopRequested {
			w.WriteHeader(409)
			return
		}
		stopped.MachineID = machineB
		stopped.Config.Name = "B 电脑的新计划"
		updated, err := store.Save(r.Context(), stopped, stopped.Version)
		if err != nil {
			w.WriteHeader(500)
			return
		}
		plan = updated
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
	})
	mux.HandleFunc("/__fixture/delete", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.Header.Get("Authorization") != "Bearer "+token {
			w.WriteHeader(403)
			return
		}
		mutationMu.Lock()
		defer mutationMu.Unlock()
		if err := store.Delete(r.Context(), tenant.ID, email, plan.ID, plan.Version); err != nil {
			w.WriteHeader(500)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
	})
	mux.Handle("/", server.Routes())
	httpServer := httptest.NewServer(mux)
	defer httpServer.Close()
	data, err := json.Marshal(map[string]any{"base_url": httpServer.URL, "token": token, "email": email, "machine_a": machineA, "machine_b": machineB, "summary": summary})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(output, data, 0o600); err != nil {
		t.Fatal(err)
	}
	// 父测试关闭标准输入后正常退出，确保专用数据库清理完成。
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		if scanner.Text() == "done" {
			break
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
}
