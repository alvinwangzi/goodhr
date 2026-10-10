// 本文件通过真实 HTTP 路由验证 HRPlus 计划保存的登录、设备、岗位和版本边界，不执行浏览器任务。
package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestExecutionPlanPlatformCapabilities 验证四平台已开放打招呼、仅 Boss 消息、关闭与未知平台的 HTTP 保存边界。
func TestExecutionPlanPlatformCapabilities(t *testing.T) {
	for _, platform := range []string{"boss", "hliepin", "liepin", "zhaopin", "unknown"} {
		t.Run(platform, func(t *testing.T) {
			server := mustNewServer(t)
			routes := server.Routes()
			email := "capability-" + platform + "@example.com"
			token := loginForTest(t, routes, email)
			bindPositionDeviceForTest(t, routes, token)
			if _, err := server.positions.subscriptions.AdjustSubscriptionDays(email, memberTypePro, 30); err != nil {
				t.Fatal(err)
			}
			position, err := server.positions.store.SavePosition(Position{UserEmail: email, PlatformID: platform, Name: "HRPlus 虚构平台岗位", CommonConfig: map[string]any{"mode_default": "keyword"}})
			if err != nil {
				t.Fatal(err)
			}
			for _, action := range []string{"greeting", "auto_reply", "re_greet", "closed"} {
				open := action != "closed"
				body, _ := json.Marshal(map[string]any{"id": platform, "open": open})
				if err := server.positions.systemConfigs.Save(SystemConfig{ConfigKey: "platform." + platform, ConfigValue: string(body), Enabled: true}); err != nil {
					t.Fatal(err)
				}
				config := validPlanConfig()
				config.Items = config.Items[:1]
				config.Items[0].PositionID = position.ID
				config.Items[0].PrioritizeReply = false
				selected := action
				if !open {
					selected = "greeting"
				}
				config.Items[0].Actions = []string{selected}
				raw, _ := json.Marshal(map[string]any{"machine_id": positionTestMachineID, "expected_version": 0, "config": config})
				request := httptest.NewRequest(http.MethodPost, "/api/execution-plans", bytes.NewReader(raw))
				request.Header.Set("Authorization", "Bearer "+token)
				response := httptest.NewRecorder()
				routes.ServeHTTP(response, request)
				allowed := platform != "unknown" && open && (selected == "greeting" || platform == "boss")
				if allowed && response.Code != http.StatusOK {
					t.Fatalf("%s/%s 应可保存：%s", platform, action, response.Body.String())
				}
				if !allowed && (response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "平台")) {
					t.Fatalf("%s/%s 未明确拒绝：%s", platform, action, response.Body.String())
				}
			}
		})
	}
}

// TestExecutionPlanAPI 验证重复岗位保存、缺失绑定、未知字段、越权与版本冲突。
func TestExecutionPlanAPI(t *testing.T) {
	server := mustNewServer(t)
	routes := server.Routes()
	token := loginForTest(t, routes, "plan-api@example.com")
	bindPositionDeviceForTest(t, routes, token)
	if err := server.positions.systemConfigs.Save(SystemConfig{ConfigKey: "platform.boss", ConfigValue: `{"id":"boss","open":true}`, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	id := createPositionWithConfigForTest(t, routes, token, "计划岗位", `{"mode_default":"keyword"}`)
	config := validPlanConfig()
	for i := range config.Items {
		config.Items[i].PositionID = id
		config.Items[i].Actions = []string{"greeting"}
		config.Items[i].PrioritizeReply = false
	}
	request := map[string]any{"machine_id": positionTestMachineID, "expected_version": 0, "config": config}
	post := func(payload any) *httptest.ResponseRecorder {
		raw, _ := json.Marshal(payload)
		req := httptest.NewRequest(http.MethodPost, "/api/execution-plans", bytes.NewReader(raw))
		req.Header.Set("Authorization", "Bearer "+token)
		res := httptest.NewRecorder()
		routes.ServeHTTP(res, req)
		return res
	}
	res := post(request)
	if res.Code != 200 {
		t.Fatalf("保存失败 %s", res.Body.String())
	}
	var body struct {
		Plan ExecutionPlan `json:"plan"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Plan.State != "stopped" || len(body.Plan.Config.Items) != 2 {
		t.Fatal("保存即启动或合并了重复岗位")
	}
	intentPost := func(action string, input any) *httptest.ResponseRecorder {
		raw, _ := json.Marshal(input)
		req := httptest.NewRequest(http.MethodPost, "/api/execution-plans/"+body.Plan.ID+"/"+action, bytes.NewReader(raw))
		req.Header.Set("Authorization", "Bearer "+token)
		res := httptest.NewRecorder()
		routes.ServeHTTP(res, req)
		return res
	}
	arm := planIntentFixture(t, "arm")
	armedResponse := intentPost("arm", arm)
	if armedResponse.Code != 200 {
		t.Fatalf("启用失败 %s", armedResponse.Body.String())
	}
	var armed struct {
		Plan ExecutionPlan `json:"plan"`
	}
	if err := json.Unmarshal(armedResponse.Body.Bytes(), &armed); err != nil || armed.Plan.State != "enabled" {
		t.Fatal("启用结果不正确", err)
	}
	stop := planIntentFixture(t, "stop")
	stop.ActivationID = armed.Plan.ActivationID
	store := server.executionPlans.store.(*MemoryExecutionPlanStore)
	store.runs["stop-fixture"] = ExecutionPlanRun{PlanID: body.Plan.ID, ActivationID: armed.Plan.ActivationID, State: "waiting_window"}
	if stoppedResponse := intentPost("stop", stop); stoppedResponse.Code != 200 {
		t.Fatal("停止失败", stoppedResponse.Body.String())
	}
	confirmation := planStopConfirmationFixture(t, armed.Plan)
	wrongConfirmation := confirmation
	wrongConfirmation.MachineID = "other-device"
	if response := intentPost("confirm-stop", wrongConfirmation); response.Code != 403 {
		t.Fatal("其他电脑可确认收尾", response.Body.String())
	}
	wrongConfirmation = confirmation
	wrongConfirmation.CleanupConfirmed = false
	if response := intentPost("confirm-stop", wrongConfirmation); response.Code != 400 {
		t.Fatal("缺少收尾确认被接受", response.Body.String())
	}
	if response := intentPost("confirm-stop", confirmation); response.Code != 200 {
		t.Fatal("指定电脑无法确认收尾", response.Body.String())
	}
	request["id"] = body.Plan.ID
	request["expected_version"] = 0
	if res = post(request); res.Code != 409 {
		t.Fatalf("旧版本被接受 %s", res.Body.String())
	}
	request["expected_version"] = 1
	request["machine_id"] = "other-device"
	if res = post(request); res.Code != 403 {
		t.Fatal("未绑定设备可保存")
	}
	request["machine_id"] = positionTestMachineID
	request["token"] = "must-not-store"
	if res = post(request); res.Code != 400 {
		t.Fatal("凭证字段混入配置")
	}
	delete(request, "token")
	messageConfig := validPlanConfig()
	for i := range messageConfig.Items {
		messageConfig.Items[i].PositionID = id
	}
	request["config"] = messageConfig
	if _, err := server.positions.subscriptions.AdjustSubscriptionDays("plan-api@example.com", memberTypePlus, 30); err != nil {
		t.Fatal(err)
	}
	if res = post(request); res.Code != 403 {
		t.Fatalf("非 Pro 用户保存消息动作 %s", res.Body.String())
	}
	foreignPosition, err := server.positions.store.SavePosition(Position{UserEmail: "plan-other@example.com", PlatformID: "boss", Name: "其他用户岗位"})
	if err != nil {
		t.Fatal(err)
	}
	foreignConfig := validPlanConfig()
	foreignConfig.Items[0].PositionID = foreignPosition.ID
	foreignConfig.Items[0].Actions = []string{"greeting"}
	foreignConfig.Items[0].PrioritizeReply = false
	foreignConfig.Items[1].PositionID = id
	request["config"] = foreignConfig
	if res = post(request); res.Code != 403 {
		t.Fatal("其他所有者岗位能加入计划")
	}
	other := loginForTest(t, routes, "plan-other@example.com")
	req := httptest.NewRequest(http.MethodGet, "/api/execution-plans/"+body.Plan.ID, nil)
	req.Header.Set("Authorization", "Bearer "+other)
	response := httptest.NewRecorder()
	routes.ServeHTTP(response, req)
	if response.Code != 404 {
		t.Fatal("其他用户能读计划")
	}
	config.Items[0].PositionID = "missing"
	request["config"] = config
	if res = post(request); res.Code != 403 {
		t.Fatal("缺失岗位可保存")
	}
}
