// Package app 文件作用：验证健康响应能力标志与启动参数中任务类型的透传。
package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"goodhr5/local-agent-go/internal/browser"
	"goodhr5/local-agent-go/internal/config"
	"goodhr5/local-agent-go/internal/localdb"
	"goodhr5/local-agent-go/internal/ocr"
	"goodhr5/local-agent-go/internal/positionrunner"
	"goodhr5/local-agent-go/internal/runtime"
)

// newCapabilityTestServer 构造只用于健康与参数解析测试的本地服务。
func newCapabilityTestServer(t *testing.T) *Server {
	t.Helper()
	cfg := &config.Config{
		Port:           18765,
		DataDir:        t.TempDir(),
		LogsDir:        t.TempDir(),
		ProfilesDir:    t.TempDir(),
		DownloadsDir:   t.TempDir(),
		ScreenshotsDir: t.TempDir(),
	}
	db, err := localdb.Open(cfg)
	if err != nil {
		t.Fatalf("打开本地数据库失败：%v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	runtimeManager := runtime.NewManager(cfg)
	return &Server{
		cfg:     cfg,
		runtime: runtimeManager,
		worker:  browser.NewWorkerManager(runtimeManager),
		ocr:     ocr.New(cfg),
		db:      db,
		runner:  positionrunner.New(db, browser.NewWorkerManager(runtimeManager), ocr.New(cfg), cfg.ProfilesDir, cfg.DownloadsDir, cfg.ScreenshotsDir, cfg.DataDir, cfg.CloudAPIBase),
	}
}

// TestHandleHealthExposesAutoReplyCapability 验证健康响应包含能力标志；老程序缺该字段时前端必须视为不支持。
func TestHandleHealthExposesAutoReplyCapability(t *testing.T) {
	s := newCapabilityTestServer(t)
	recorder := httptest.NewRecorder()
	s.handleHealth(recorder, httptest.NewRequest(http.MethodGet, "/health", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("健康响应状态错误：%d", recorder.Code)
	}
	var payload struct {
		Data struct {
			Capabilities map[string]any `json:"capabilities"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("健康响应不是合法 JSON：%v", err)
	}
	if payload.Data.Capabilities == nil || payload.Data.Capabilities["auto_reply"] != true {
		t.Fatalf("健康响应缺少 auto_reply 能力标志：%s", recorder.Body.String())
	}
}

// TestStartOptionsFromPayloadCarriesTaskType 验证启动请求中的任务类型和打招呼开关被完整透传。
func TestStartOptionsFromPayloadCarriesTaskType(t *testing.T) {
	s := newCapabilityTestServer(t)
	options := s.startOptionsFromPayload(map[string]any{
		"task_type":    "auto_reply",
		"enable_greet": false,
	}, "machine-1")
	if options.TaskType != "auto_reply" {
		t.Fatalf("任务类型未透传：%q", options.TaskType)
	}
	if options.EnableGreet {
		t.Fatal("明确关闭的打招呼开关被默认值覆盖")
	}
	if options.MachineID != "machine-1" {
		t.Fatalf("设备编号未透传：%q", options.MachineID)
	}
	options = s.startOptionsFromPayload(map[string]any{}, "")
	if options.TaskType != "" {
		t.Fatalf("省略任务类型时应保持为空由运行器规范成打招呼：%q", options.TaskType)
	}
	if !options.EnableGreet {
		t.Fatal("打招呼开关缺失时应使用默认 true")
	}
}
