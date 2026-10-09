// 本文件验证 HRPlus 手动纯打招呼必须得到云端许可，账号占用冲突或错误时不启动浏览器。
package positionrunner

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestGreetingStartRequiresCloudPermit 验证跨设备占用拒绝及云端错误不会被当作警告继续执行。
func TestGreetingStartRequiresCloudPermit(t *testing.T) {
	for _, status := range []int{http.StatusConflict, http.StatusServiceUnavailable, http.StatusOK} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			requested := false
			cloud := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
				var value any
				switch request.URL.Path {
				case "/api/positions/blocked":
					value = map[string]any{"position": map[string]any{"id": "blocked", "name": "fixture", "platform_id": "boss", "common_config": map[string]any{"mode_default": "keyword", "detail_mode": "keyword"}}}
				case "/api/subscription/status":
					value = map[string]any{"subscription": map[string]any{"active": true}}
				case "/api/config/user-preferences":
					value = map[string]any{"config": map[string]any{}}
				case "/api/platforms/config/":
					value = map[string]any{"configs": []map[string]any{{"config_key": "platform.boss", "config_value": `{"id":"boss","auth":{"pages":[{"url":"https://www.zhipin.com/web/chat/recommend","entry":true}]}}`}}}
				case "/api/positions/blocked/status":
					requested = true
					w.WriteHeader(status)
					value = map[string]any{"error": "账号已被执行计划占用"}
				default:
					t.Error("启动拒绝后仍访问其他路径", request.URL.Path)
					w.WriteHeader(404)
					return
				}
				_ = json.NewEncoder(w).Encode(value)
			}))
			defer cloud.Close()
			db := openRunnerTestDB(t)
			worker := &fakeWorker{}
			runner := newTestRunner(t, db, worker)
			if _, err := runner.Start(t.Context(), "blocked", StartOptions{Token: "fixture-token", CloudAPIBase: cloud.URL, MachineID: "fixture-machine", TaskType: "greeting"}); err == nil {
				t.Fatal("云端拒绝仍成功启动")
			}
			if !requested {
				t.Fatal("没有到达真实云端许可请求")
			}
			if len(worker.calls) != 0 {
				t.Fatal("拒绝后启动了浏览器", worker.calls)
			}
			runner.mu.Lock()
			occupied := len(runner.running) > 0 || runner.browserLease != nil
			runner.mu.Unlock()
			if occupied {
				t.Fatal("被拒绝启动遗留本地执行权")
			}
			saved, err := db.GetPosition("blocked")
			if err != nil || saved.Status != "stopped" {
				t.Fatal("被拒绝启动仍显示运行中", err)
			}
		})
	}
}
