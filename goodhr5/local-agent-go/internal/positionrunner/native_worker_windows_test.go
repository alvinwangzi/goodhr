// 本文件在 Windows 使用真实 Go 岗位入口、SQLite、完整 Worker 和现有 CloakBrowser 验证受控岗位流程，不连接实际招聘网站。
package positionrunner

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"goodhr5/local-agent-go/internal/browser"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// nativeHTTPWorker 只传输实际 Worker HTTP 请求，不替换平台页面或岗位调度行为。
type nativeHTTPWorker struct{ base string }

// Start 核对测试进程真实健康状态，不伪造浏览器已启动。
func (w nativeHTTPWorker) Start(ctx context.Context) (browser.WorkerStatus, error) {
	request, _ := http.NewRequestWithContext(ctx, http.MethodGet, w.base+"/health", nil)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return browser.WorkerStatus{}, err
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return browser.WorkerStatus{}, fmt.Errorf("Worker 健康状态不正确")
	}
	return browser.WorkerStatus{Running: true, BaseURL: w.base}, nil
}

// Call 使用同一标准 HTTP 路径，不增加网络重试或改变参数。
func (w nativeHTTPWorker) Call(ctx context.Context, route string, payload any) (map[string]any, error) {
	return w.CallOnce(ctx, route, payload)
}

// CallOnce 执行真实一次调用，错误保留给岗位执行器处理。
func (w nativeHTTPWorker) CallOnce(ctx context.Context, route string, payload any) (map[string]any, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, w.base+route, bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	var result map[string]any
	if err = json.NewDecoder(response.Body).Decode(&result); err != nil {
		return nil, err
	}
	if response.StatusCode >= 400 {
		return nil, fmt.Errorf("%s", result["msg"])
	}
	return result, nil
}

// TestNativeWorkerEmptyPositionEnd 验证从已有 Start 入口启动后绑定账号、完成消息检查并真正结束，不长期挂起。
func TestNativeWorkerEmptyPositionEnd(t *testing.T) {
	runNativeWorkerPosition(t, "empty-job")
}

// TestNativeWorkerReplyPositionEnd 验证真实消息读取、模型 HTTP、标准输入发送、确认与本次计数。
func TestNativeWorkerReplyPositionEnd(t *testing.T) { runNativeWorkerPosition(t, "reply-job") }

// TestNativeWorkerStopDuringGeneration 验证真实页面读取后、AI 返回前停止，之后不输入、不发送且释放运行。
func TestNativeWorkerStopDuringGeneration(t *testing.T) { runNativeWorkerPosition(t, "stop-reply-job") }

// runNativeWorkerPosition 运行共用的独立 Windows 验收环境，模式只决定虚构页面数据。
func runNativeWorkerPosition(t *testing.T, mode string) {
	if os.Getenv("HRPLUS_M1_NATIVE_WORKER_TEST") != "1" {
		t.Skip("需要显式启用 Windows 真实 Worker 受控验收")
	}
	root, err := filepath.Abs(filepath.Join("..", "..", "worker-node"))
	if err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(os.Getenv("APPDATA"), "HRPlus", "runtime", "cloakbrowser", "chrome.exe")
	if _, err = os.Stat(binary); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	_ = listener.Close()
	directory := t.TempDir()
	logFile, err := os.Create(filepath.Join(directory, "worker.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer logFile.Close()
	preload := filepath.ToSlash(filepath.Join(root, "test", "fixtures", "worker-native-preload.mjs"))
	command := exec.Command("node", "--import", "file:///"+preload, filepath.Join(root, "src", "index.js"))
	command.Dir = root
	command.Stdout = logFile
	command.Stderr = logFile
	fixtureMode := mode
	if mode == "stop-reply-job" {
		fixtureMode = "reply-job"
	}
	command.Env = append(os.Environ(), fmt.Sprintf("GOODHR_WORKER_ADDR=127.0.0.1:%d", port), fmt.Sprintf("GOODHR_WORKER_PORT_END=%d", port), "CLOAKBROWSER_BINARY_PATH="+binary, "HRPLUS_M1_FIXTURE_MODE="+fixtureMode, "HRPLUS_M1_FIXTURE_LEDGER="+filepath.Join(directory, "ledger.json"))
	if err = command.Start(); err != nil {
		t.Fatal(err)
	}
	worker := nativeHTTPWorker{base: fmt.Sprintf("http://127.0.0.1:%d", port)}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = worker.CallOnce(ctx, "/api/v1/browser/stop", map[string]any{})
		_ = command.Process.Kill()
		_ = command.Wait()
	}()
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err = worker.Start(t.Context()); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal(err)
		}
		time.Sleep(100 * time.Millisecond)
	}
	generationStarted := make(chan struct{}, 1)
	releaseGeneration := make(chan struct{})
	var releaseOnce sync.Once
	finishGeneration := func() { releaseOnce.Do(func() { close(releaseGeneration) }) }
	ai := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if mode == "stop-reply-job" {
			select {
			case generationStarted <- struct{}{}:
			default:
			}
			select {
			case <-r.Context().Done():
				return
			case <-releaseGeneration:
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"content": `{"action":"reply","text":"岗位仍在招聘，欢迎沟通。","reason":"回答岗位状态","request_resume":false}`}}}})
	}))
	defer ai.Close()
	defer finishGeneration()
	var runCounter atomic.Int32
	cloud := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var result any
		switch r.URL.Path {
		case "/api/auth/me":
			result = map[string]any{"user": map[string]any{"email": "fixture@example.com"}}
		case "/api/subscription/status":
			result = map[string]any{"subscription": map[string]any{"active": true, "allow_auto_reply": true}}
		case "/api/config/user-preferences":
			result = map[string]any{"config": map[string]any{}}
		case "/api/config/effective-ai":
			result = map[string]any{"config": map[string]any{"base_url": ai.URL, "api_key": "fixture-key", "model": "fixture"}}
		case "/api/positions/native-position/screenings/find":
			result = map[string]any{"item": map[string]any{"id": "screen-A", "position_id": "native-position", "platform": "boss", "platform_candidate_id": "opaque-A", "candidate_name": "同名候选人 A", "score": 90}}
		case "/api/positions/native-position/resume-requests", "/api/positions/native-position/screenings":
			result = map[string]any{"ok": true}
		case "/api/positions/native-position":
			result = map[string]any{"position": map[string]any{"id": "native-position", "name": "Go", "platform_id": "boss", "mode": "keyword", "position": map[string]any{"name": "Go"}}}
		case "/api/positions/native-position/status":
			var status struct {
				Status string `json:"status"`
				RunID  string `json:"run_id"`
			}
			_ = json.NewDecoder(r.Body).Decode(&status)
			runID := status.RunID
			if runID == "" {
				index := runCounter.Add(1)
				runID = "native-run"
				if index > 1 {
					runID = fmt.Sprintf("native-run-%d", index)
				}
			}
			result = map[string]any{"ok": true, "status": status.Status, "run_id": runID, "task_run": map[string]any{"id": runID}}
		default:
			t.Errorf("unexpected cloud path %s", r.URL.Path)
			w.WriteHeader(404)
			return
		}
		_ = json.NewEncoder(w).Encode(result)
	}))
	defer cloud.Close()
	db := openRunnerTestDB(t)
	runner := newTestRunner(t, db, worker)
	if _, err = runner.Start(t.Context(), "native-position", StartOptions{CloudAPIBase: cloud.URL, Token: "fixture-token", MachineID: "fixture-machine", TaskType: "auto_reply", PrioritizeReply: true}); err != nil {
		t.Fatal(err)
	}
	expectedStatus := "completed"
	if mode == "stop-reply-job" {
		select {
		case <-generationStarted:
		case <-time.After(35 * time.Second):
			t.Fatal("真实流程未到达 AI 边界")
		}
		if _, err = runner.Stop("native-position"); err != nil {
			t.Fatal(err)
		}
		finishGeneration()
		expectedStatus = "stopped"
	}
	deadline = time.Now().Add(40 * time.Second)
	for {
		position, readErr := db.GetPosition("native-position")
		if readErr != nil {
			t.Fatal(readErr)
		}
		if position.Status == expectedStatus {
			break
		}
		if position.Status == "failed" || time.Now().After(deadline) {
			raw, _ := os.ReadFile(filepath.Join(directory, "worker.log"))
			t.Fatalf("任务未正常结束 status=%s Worker=%s", position.Status, strings.TrimSpace(string(raw)))
		}
		time.Sleep(100 * time.Millisecond)
	}
	checkpoint, err := db.LatestActionCheckpoint(t.Context(), "native-position")
	if err != nil || !checkpoint.AccountBound || checkpoint.CloudRunID != "native-run" || checkpoint.LastMessageCheck.IsZero() {
		t.Fatalf("实际检查点缺少账号或检查事实 %+v %v", checkpoint, err)
	}
	deadline = time.Now().Add(3 * time.Second)
	for runner.IsRunning("native-position") && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if runner.IsRunning("native-position") {
		t.Fatal("当前工作结束后仍占用运行")
	}
	if mode == "reply-job" {
		if checkpoint.Replied != 1 {
			t.Fatalf("真实回复未计入本次结果 %+v", checkpoint)
		}
		raw, readErr := os.ReadFile(filepath.Join(directory, "ledger.json"))
		if readErr != nil {
			t.Fatal(readErr)
		}
		var ledger struct {
			Clicks int `json:"clicks"`
		}
		if err = json.Unmarshal(raw, &ledger); err != nil {
			t.Fatal(err)
		}
		if ledger.Clicks != 1 {
			t.Fatalf("虚构页面重复发送或未发送 clicks=%d", ledger.Clicks)
		}
	}
	if mode == "stop-reply-job" {
		time.Sleep(500 * time.Millisecond)
		raw, readErr := os.ReadFile(filepath.Join(directory, "ledger.json"))
		if readErr != nil {
			t.Fatal(readErr)
		}
		var ledger struct {
			Clicks int `json:"clicks"`
		}
		if err = json.Unmarshal(raw, &ledger); err != nil {
			t.Fatal(err)
		}
		if ledger.Clicks != 0 || checkpoint.Replied != 0 {
			t.Fatalf("停止后仍发送或误记成功 clicks=%d replied=%d", ledger.Clicks, checkpoint.Replied)
		}
		draftResult, draftErr := worker.CallOnce(t.Context(), "/api/v1/page/extract-text", map[string]any{"no_script": true, "selector_spec": map[string]any{"selectors": []string{".boss-chat-editor-input"}}, "editable": true})
		if draftErr != nil {
			t.Fatal(draftErr)
		}
		draftData, _ := draftResult["data"].(map[string]any)
		if draftData["text"] != "" {
			t.Fatalf("停止后仍输入了草稿 %v", draftData["text"])
		}
		originalRunID := checkpoint.RunID
		if _, err = runner.Start(t.Context(), "native-position", StartOptions{CloudAPIBase: cloud.URL, Token: "fixture-token", MachineID: "fixture-machine", TaskType: "auto_reply", PrioritizeReply: true}); err != nil {
			t.Fatal(err)
		}
		deadline = time.Now().Add(35 * time.Second)
		for {
			position, readErr := db.GetPosition("native-position")
			if readErr != nil {
				t.Fatal(readErr)
			}
			if position.Status == "completed" {
				break
			}
			if position.Status == "failed" || time.Now().After(deadline) {
				t.Fatalf("停止后重新开始未结束 status=%s", position.Status)
			}
			time.Sleep(100 * time.Millisecond)
		}
		second, readErr := db.LatestActionCheckpoint(t.Context(), "native-position")
		if readErr != nil || second.RunID == originalRunID || second.CloudRunID == checkpoint.CloudRunID || second.Replied != 1 {
			t.Fatalf("重新开始混用了旧运行 %+v %v", second, readErr)
		}
		raw, readErr = os.ReadFile(filepath.Join(directory, "ledger.json"))
		if readErr != nil {
			t.Fatal(readErr)
		}
		if err = json.Unmarshal(raw, &ledger); err != nil {
			t.Fatal(err)
		}
		if ledger.Clicks != 1 {
			t.Fatalf("停止与重新开始造成重复发送 clicks=%d", ledger.Clicks)
		}
	}
}
