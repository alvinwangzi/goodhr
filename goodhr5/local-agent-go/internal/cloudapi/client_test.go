// Package cloudapi 负责测试 Go 本地程序访问云端接口的能力。
package cloudapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestSyncTaskStatus 验证任务类型和运行所有权透传，自动回复必须得到明确许可。
func TestSyncTaskStatus(t *testing.T) {
 for _, body := range []string{`{"ok":true,"status":"running","run_id":"run-1"}`, `{"ok":true,"status":"running"}`, `{"ok":false,"status":"running","run_id":"run-1"}`, `{"ok":true,"status":"stopped","run_id":"run-1"}`} {
  t.Run(body, func(t *testing.T) {
   server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
    var got map[string]any
    _ = json.NewDecoder(r.Body).Decode(&got)
    if got["task_type"] != "auto_reply" || got["run_id"] != "run-1" || got["run_greeted_count"] != float64(0) { t.Errorf("任务参数错误：%v", got) }
    _, _ = w.Write([]byte(body))
   }))
   defer server.Close()
   _, err := New(server.URL).SyncTaskStatus(t.Context(), "token", "position", TaskStatusRequest{Status:"running", TaskType:"auto_reply", RunID:"run-1", Greeted:99})
   if (err == nil) != (body == `{"ok":true,"status":"running","run_id":"run-1"}`) { t.Fatalf("许可判定错误：%v", err) }
  })
 }
}

// TestFetchPlatformConfig 验证公开平台配置读取和 JSON 字符串解码。
func TestFetchPlatformConfig(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/platforms/config/" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok": true,
			"configs": []map[string]any{
				{
					"config_key":   "platform.boss",
					"config_value": `{"name":"Boss直聘","selectors":{"card":".job-card"}}`,
				},
			},
		})
	}))
	defer server.Close()

	client := New(server.URL)
	config, err := client.FetchPlatformConfig(t.Context(), "boss")
	if err != nil {
		t.Fatal(err)
	}
	if config["id"] != "boss" || config["name"] != "Boss直聘" {
		t.Fatalf("config = %+v", config)
	}
}

// TestFetchSubscription 验证会员状态读取会携带登录令牌。
func TestFetchSubscription(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer token-1" {
			t.Fatalf("authorization = %q", r.Header.Get("Authorization"))
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok": true,
			"subscription": map[string]any{
				"active":      true,
				"member_type": "plus",
			},
		})
	}))
	defer server.Close()

	client := New(server.URL)
	subscription, err := client.FetchSubscription(t.Context(), "token-1")
	if err != nil {
		t.Fatal(err)
	}
	if subscription["active"] != true || subscription["member_type"] != "plus" {
		t.Fatalf("subscription = %+v", subscription)
	}
}

// TestFetchPlatformConfigError 验证常见英文错误会转成中文。
func TestFetchPlatformConfigError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": "failed to load system configs"})
	}))
	defer server.Close()

	client := New(server.URL)
	_, err := client.FetchPlatformConfig(t.Context(), "boss")
	if err == nil || err.Error() != "读取平台配置失败" {
		t.Fatalf("err = %v", err)
	}
}

// TestSyncPositionStatusReturnsNoticeResult 验证岗位完成同步会读取后台返回的邮件发送结果。
func TestSyncPositionStatusReturnsNoticeResult(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/positions/position-1/status" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer token-1" {
			t.Fatalf("authorization = %q", r.Header.Get("Authorization"))
		}
		var request struct {
			RunGreetedCount int    `json:"run_greeted_count"`
			MachineID       string `json:"machine_id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		if request.RunGreetedCount != 3 {
			t.Fatalf("run_greeted_count = %d", request.RunGreetedCount)
		}
		if request.MachineID != "goodhr-device-v1-test" {
			t.Fatalf("machine_id = %q", request.MachineID)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":          true,
			"status":      "completed",
			"notice_sent": true,
		})
	}))
	defer server.Close()

	client := New(server.URL)
	result, err := client.SyncPositionStatusWithCounts(t.Context(), "token-1", "position-1", "completed", "goodhr-device-v1-test", 3, 1)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "completed" || !result.NoticeSent {
		t.Fatalf("result = %+v", result)
	}
}

// TestNotifyResumeRequestedSendsRunAndNames 验证求简历补报按约定路径和报文发送。
func TestNotifyResumeRequestedSendsRunAndNames(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/positions/position-1/resume-requests" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer token-1" {
			t.Fatalf("authorization = %q", r.Header.Get("Authorization"))
		}
		var request struct {
			RunID string   `json:"run_id"`
			Names []string `json:"names"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		if request.RunID != "run-1" {
			t.Fatalf("run_id = %q", request.RunID)
		}
		if len(request.Names) != 2 || request.Names[0] != "赵永豪" || request.Names[1] != "程雨遥" {
			t.Fatalf("names = %v", request.Names)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "matched": request.Names, "missing": []string{}})
	}))
	defer server.Close()

	client := New(server.URL)
	if err := client.NotifyResumeRequested(t.Context(), "token-1", "position-1", "run-1", []string{"赵永豪", "程雨遥"}); err != nil {
		t.Fatal(err)
	}
}

// TestNotifyResumeRequestedRejectsEmptyInput 验证岗位或名单为空时直接报错不发请求。
func TestNotifyResumeRequestedRejectsEmptyInput(t *testing.T) {
	client := New("https://example.invalid")
	if err := client.NotifyResumeRequested(t.Context(), "token-1", "", "run-1", []string{"张三"}); err == nil {
		t.Fatal("岗位 ID 为空时应报错")
	}
	if err := client.NotifyResumeRequested(t.Context(), "token-1", "position-1", "run-1", []string{"  "}); err == nil {
		t.Fatal("名单为空时应报错")
	}
}
