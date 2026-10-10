// 本文件验证 HRPlus 进度日志读取的真实登录、原运行/任务、分页和切换账号保护，不调用招聘页面。
package app

import (
	"encoding/json"
	"goodhr5/local-agent-go/internal/cloudapi"
	"goodhr5/local-agent-go/internal/localdb"
	"goodhr5/local-agent-go/internal/planmodel"
	"goodhr5/local-agent-go/internal/protectedsession"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

// TestPlanItemLogsHTTPOrigin 检查请求头、当前团队、原执行项和返回原 TaskRun，读取不发写命令。
func TestPlanItemLogsHTTPOrigin(t *testing.T) {
	s := newCapabilityTestServer(t)
	raw, err := os.ReadFile(filepath.Join("..", "planmodel", "testdata", "permit.json"))
	if err != nil {
		t.Fatal(err)
	}
	var permit planmodel.Permit
	if json.Unmarshal(raw, &permit) != nil {
		t.Fatal("夹具无效")
	}
	run := permit.Run
	run.Items[0].TaskRunID = "70000000-0000-0000-0000-000000000001"
	if err = run.Validate(); err != nil {
		t.Fatal(err)
	}
	changedTeam := &atomic.Bool{}
	cloud := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.Header.Get("Authorization") != "Bearer fixture-log-token" {
			t.Error("日志读取发写命令或使用其他登录")
		}
		if r.URL.Path == "/api/auth/me" {
			tenant := "fixture-team"
			if changedTeam.Load() {
				tenant = "other-team"
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"user": map[string]any{"email": "fixture@example.com", "tenant_id": tenant}})
			return
		}
		if r.URL.Path == "/api/execution-plan-runs/"+run.ID {
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "run": run})
			return
		}
		t.Error("日志读取访问了其他路径", r.URL.Path)
		w.WriteHeader(404)
	}))
	defer cloud.Close()
	identity := protectedsession.Session{CloudBase: cloud.URL, UserEmail: "fixture@example.com", TenantID: "fixture-team", MachineID: "A", Token: "fixture-log-token"}
	if err = s.commitProtectedSession(t.Context(), 0, identity); err != nil {
		t.Fatal(err)
	}
	scope := cloudapi.SessionOwnerScope(cloud.URL, identity.UserEmail)
	cp, err := s.db.EnsurePlanActionRun(t.Context(), localdb.ActionCheckpoint{PlanRunID: run.ID, ItemRunID: run.Items[0].ID, CloudRunID: run.Items[0].TaskRunID, OwnerScope: scope, PositionID: run.Items[0].Snapshot.PositionID, Platform: "boss", ProfileScope: "fixture", TaskType: "greeting"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.AddPlanItemLog(t.Context(), scope, cp.RunID, "info", "本项原进度"); err != nil {
		t.Fatal(err)
	}
	path := "/api/v1/local/execution-plan-runs/" + run.ID + "/items/" + run.Items[0].ID + "/logs"
	call := func(url, token string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodGet, url, nil)
		if token != "" {
			request.Header.Set("Authorization", "Bearer "+token)
		}
		result := httptest.NewRecorder()
		s.handlePlanItemLogs(result, request)
		return result
	}
	response := call(path, identity.Token)
	if response.Code != 200 || !strings.Contains(response.Body.String(), "本项原进度") || !strings.Contains(response.Body.String(), cp.CloudRunID) || strings.Contains(response.Body.String(), scope) {
		t.Fatal("原日志回执错误", response.Body.String())
	}
	if response = call(path+"?token="+identity.Token, ""); response.Code != 403 {
		t.Fatal("查询串被当登录")
	}
	if response = call(path, "other-token"); response.Code != 403 {
		t.Fatal("另一网页登录可读日志")
	}
	if response = call(path+"?before=bad", identity.Token); response.Code != 400 {
		t.Fatal("非法分页接受")
	}
	changedTeam.Store(true)
	if response = call(path, identity.Token); response.Code != 403 || strings.Contains(response.Body.String(), "本项原进度") {
		t.Fatal("团队变化后泄露旧日志")
	}
}
