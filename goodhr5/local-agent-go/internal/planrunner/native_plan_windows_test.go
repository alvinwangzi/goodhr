// 本文件显式使用 HRPlus 实际父计划/M1、Node Worker 与缓存浏览器验证两个岗位顺序，网站请求全部由虚构页面接管。
package planrunner

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"goodhr5/local-agent-go/internal/browser"
	"goodhr5/local-agent-go/internal/planmodel"
	"goodhr5/local-agent-go/internal/positionrunner"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

// nativePlanWorker 只传输标准 Worker HTTP，不伪造页面或候选人动作结果。
type nativePlanWorker struct{ base string }

// Start 读取本次 Worker 实际健康，不能从进程文件推测正在运行。
func (w nativePlanWorker) Start(ctx context.Context) (browser.WorkerStatus, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, w.base+"/health", nil)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return browser.WorkerStatus{}, err
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return browser.WorkerStatus{}, fmt.Errorf("隔离 Worker 尚未就绪")
	}
	return browser.WorkerStatus{Running: true, BaseURL: w.base}, nil
}

// Call 沿用同一次标准 HTTP 传输，不额外添加业务重试。
func (w nativePlanWorker) Call(ctx context.Context, path string, payload any) (map[string]any, error) {
	return w.CallOnce(ctx, path, payload)
}

// CallOnce 将原参数传递给真实 Worker，失败返回给实际 M1 编排。
func (w nativePlanWorker) CallOnce(ctx context.Context, path string, payload any) (map[string]any, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, w.base+path, bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	var result map[string]any
	if err = json.NewDecoder(res.Body).Decode(&result); err != nil {
		return nil, err
	}
	if res.StatusCode >= 400 {
		return nil, fmt.Errorf("Worker 实际调用未确认：%v", result["msg"])
	}
	return result, nil
}

// TestNativePlanTwoPositions 验证完整浏览器中 Java 后销售，原任务独立、原计数确认后整体结束。
func TestNativePlanTwoPositions(t *testing.T) {
	runNativePlanPositions(t, false)
}

// TestNativePlanFourItems 验证两个岗位先扫描后回复，四条任务原归属及消息激活顺序真实保留。
func TestNativePlanFourItems(t *testing.T) { runNativePlanPositions(t, true) }

// TestNativePlanAcrossWindows 验证实际第一项确认后原窗口结束，下午沿原计数继续第二岗位。
func TestNativePlanAcrossWindows(t *testing.T) { runNativePlanPositions(t, false, true) }

// TestNativePlanDayIncomplete 验证最后窗口结束只保存实际已完成动作，未开始第二项列入原报告。
func TestNativePlanDayIncomplete(t *testing.T) { runNativePlanPositions(t, false, true, true) }

// runNativePlanPositions 复用同一隔离原生环境，编排数量只来自原配置，不伪造成功返回。
func runNativePlanPositions(t *testing.T, messages bool, crossWindows ...bool) {
	if os.Getenv("HRPLUS_M2_NATIVE_PLAN_TEST") != "1" {
		t.Skip("需要显式启用原生多岗位受控验收")
	}
	root, err := filepath.Abs(filepath.Join("..", "..", "worker-node"))
	if err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(os.Getenv("APPDATA"), "HRPlus", "runtime", "cloakbrowser", "chrome.exe")
	if _, err := os.Stat(binary); err != nil {
		t.Fatal("缺少已缓存浏览器", err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	_ = listener.Close()
	directory := t.TempDir()
	ledger := filepath.Join(directory, "ledger.json")
	logFile, err := os.Create(filepath.Join(directory, "worker.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer logFile.Close()
	preload := filepath.ToSlash(filepath.Join(root, "test", "fixtures", "worker-native-preload.mjs"))
	cmd := exec.Command("node", "--import", "file:///"+preload, filepath.Join(root, "src", "index.js"))
	cmd.Dir = root
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.Env = append(os.Environ(), fmt.Sprintf("GOODHR_WORKER_ADDR=127.0.0.1:%d", port), fmt.Sprintf("GOODHR_WORKER_PORT_END=%d", port), "CLOAKBROWSER_BINARY_PATH="+binary, "HRPLUS_M1_FIXTURE_MODE=m2-plans", "HRPLUS_M1_FIXTURE_LEDGER="+ledger)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	worker := nativePlanWorker{base: fmt.Sprintf("http://127.0.0.1:%d", port)}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = worker.CallOnce(ctx, "/api/v1/browser/stop", map[string]any{})
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}()
	ready := false
	for attempts := 0; attempts < 100; attempts++ {
		if _, err := worker.Start(t.Context()); err == nil {
			ready = true
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !ready {
		t.Fatal("本次 Worker 实际健康未就绪")
	}
	if messages {
		ai := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"content": `{"action":"reply","text":"岗位仍在招聘，欢迎沟通。","reason":"夹具岗位问答","request_resume":false}`}}}})
		}))
		defer ai.Close()
		t.Setenv("HRPLUS_M2_NATIVE_AI_BASE", ai.URL)
	}
	c, plan, claim, authority, _, clock := acquireFixtureWithWorker(t, &atomic.Int32{}, worker, func(p *planmodel.Permit) {
		if messages {
			for index := 2; index < 4; index++ {
				config := p.Run.Snapshot.Items[index-2]
				config.ID = fmt.Sprintf("native-message-%d", index)
				config.Order = index
				config.Actions = []string{"auto_reply"}
				config.PrioritizeReply = true
				p.Run.Snapshot.Items = append(p.Run.Snapshot.Items, config)
				p.Run.Items = append(p.Run.Items, planmodel.ItemRun{ID: fmt.Sprintf("50000000-0000-0000-0000-%012d", index+1), ItemID: config.ID, Order: index, Snapshot: config, State: "pending", Actions: map[string]planmodel.ActionProgress{"auto_reply": {State: "pending"}}})
			}
		}
		for index := range p.Run.Snapshot.Items {
			position := "native-java"
			if index%2 == 1 {
				position = "native-sales"
			}
			p.Run.Snapshot.Items[index].PositionID = position
			action := "greeting"
			if index >= 2 {
				action = "auto_reply"
			}
			p.Run.Snapshot.Items[index].Actions = []string{action}
			p.Run.Snapshot.Items[index].PrioritizeReply = index >= 2
			p.Run.Items[index].Snapshot = p.Run.Snapshot.Items[index]
			p.Run.Items[index].Actions = map[string]planmodel.ActionProgress{action: {State: "pending"}}
		}
	})
	lastOnly := len(crossWindows) > 1 && crossWindows[1]
	if lastOnly {
		clock.Store(time.Date(2026, 10, 10, 5, 30, 0, 0, time.UTC).UnixNano())
	}
	held, err := c.Acquire(t.Context(), plan, claim, authority)
	if err != nil {
		t.Fatal(err)
	}
	cross := len(crossWindows) > 0 && crossWindows[0]
	if cross {
		if held.finishAt.Sub(held.window.End) < 3*time.Minute || held.finishAt.Sub(held.window.End) > 6*time.Minute {
			t.Fatal("原延后收尾不在三至六分钟")
		}
		c.now = func() time.Time {
			raw, _ := os.ReadFile(ledger)
			var observed struct {
				Greets []string `json:"greetOrder"`
			}
			if json.Unmarshal(raw, &observed) == nil && len(observed.Greets) == 1 {
				clock.Store(held.finishAt.UnixNano())
			}
			return time.Unix(0, clock.Load()).UTC()
		}
	}
	runtime := NewM1ExecutionRuntime(c, held, authority, positionrunner.StartOptions{PageReadyDelay: 1, DetailOpenProbability: 0, EnableGreet: true})
	defer func() {
		for id := range runtime.items {
			_ = runtime.CloseItem(context.Background(), id)
		}
		_ = held.Reservation.Release(true)
	}()
	result, err := NewExecutionLoop(runtime, authority.OwnerScope, c.now).Run(t.Context(), held.Permit)
	if lastOnly && err == nil {
		if result.Run.State != "incomplete" || result.Run.Items[0].Actions["greeting"].Count != 1 || result.Run.Items[1].TaskRunID != "" || held.Reservation.Valid() {
			t.Fatal("日末结算补造第二任务或丢失真实数量", result.Run)
		}
		report, err := c.db.BuildPlanReport(t.Context(), authority.OwnerScope, result.Run, c.now(), true)
		if err != nil {
			t.Fatal(err)
		}
		if report.Kind != "day_incomplete" || report.Items[0].Actions["greeting"].Confirmed != 1 || len(report.UnfinishedItemIDs) == 0 {
			t.Fatal("日末原报告不保留未完成项", report)
		}
		return
	}
	if cross && err == nil {
		if result.Run.State != "waiting_window" || result.Run.Items[0].Actions["greeting"].Count != 1 || result.Run.Items[1].TaskRunID != "" || held.Reservation.Valid() {
			t.Fatal("原生上午未保留原进度或提前开始第二岗位", result.Run)
		}
		originalRun, originalTask, originalOwner := result.Run.ID, result.Run.Items[0].TaskRunID, result.Run.OwnerID
		clock.Store(time.Date(2026, 10, 10, 5, 30, 0, 0, time.UTC).UnixNano())
		c.now = func() time.Time { return time.Unix(0, clock.Load()).UTC() }
		claim.OwnerID = "40000000-0000-0000-0000-000000000002"
		claim.RequestID = "60000000-0000-0000-0000-000000000009"
		held, err = c.Acquire(t.Context(), plan, claim, authority)
		if err != nil {
			t.Fatal(err)
		}
		if held.Permit.Run.ID != originalRun || held.Permit.Run.Items[0].TaskRunID != originalTask || held.Permit.Run.Items[0].Actions["greeting"].Count != 1 || held.Permit.Run.OwnerID == originalOwner {
			t.Fatal("原生下午重新生成原任务或计数", held.Permit.Run)
		}
		runtime = NewM1ExecutionRuntime(c, held, authority, positionrunner.StartOptions{PageReadyDelay: 1, EnableGreet: true})
		result, err = NewExecutionLoop(runtime, authority.OwnerScope, c.now).Run(t.Context(), held.Permit)
	}
	if err != nil {
		for _, selector := range []string{".fixture-menu", ".fixture-item", "body"} {
			diagnostic, readError := worker.CallOnce(t.Context(), "/api/v1/page/extract-text", map[string]any{"element": map[string]any{"selector": selector}, "timeout": 1000})
			t.Log("标准页面诊断", selector, diagnostic, readError)
		}
		for id := range runtime.items {
			entries, readErr := c.db.ListPlanItemLogs(t.Context(), authority.OwnerScope, held.Permit.Run.ID, id, 0, 200)
			if readErr == nil {
				for _, entry := range entries {
					if strings.Contains(entry.Message, "岗位") {
						t.Log(entry.Message)
					}
				}
			}
		}
		t.Fatal("原生多岗位执行失败", err)
	}
	expectedItems := 2
	if messages {
		expectedItems = 4
	}
	if result.Run.State != "completed" || result.Run.CurrentItem != expectedItems || result.Run.Items[0].TaskRunID == result.Run.Items[1].TaskRunID || result.Run.Items[0].Actions["greeting"].Count != 1 || result.Run.Items[1].Actions["greeting"].Count != 1 || held.Reservation.Valid() {
		for _, item := range result.Run.Items {
			entries, _ := c.db.ListPlanItemLogs(t.Context(), authority.OwnerScope, result.Run.ID, item.ID, 0, 200)
			for _, entry := range entries {
				if strings.Contains(entry.Message, "候选") || strings.Contains(entry.Message, "过滤") || strings.Contains(entry.Message, "关键词") {
					t.Log(entry.Message)
				}
			}
		}
		if raw, err := os.ReadFile(ledger); err == nil {
			t.Log("原生夹具实际请求", string(raw))
		}
		t.Fatal("原生多岗位未独立结束", result.Run)
	}
	raw, err := os.ReadFile(ledger)
	if err != nil {
		t.Fatal(err)
	}
	var evidence struct {
		Greets   []string `json:"greetOrder"`
		Timeline []string `json:"timeline"`
	}
	if json.Unmarshal(raw, &evidence) != nil || len(evidence.Greets) != 2 || evidence.Greets[0] != "java-person" || evidence.Greets[1] != "sales-person" {
		t.Fatal("真实点击顺序错误", string(raw))
	}
	if messages {
		if result.Run.Items[2].Actions["auto_reply"].Count != 1 || result.Run.Items[3].Actions["auto_reply"].Count != 1 {
			t.Fatal("消息确认未按原任务记录", result.Run)
		}
		seen := map[string]bool{}
		for _, item := range result.Run.Items {
			if seen[item.TaskRunID] {
				t.Fatal("四执行项覆盖原任务")
			}
			seen[item.TaskRunID] = true
		}
		wanted := []string{"greet:Java", "greet:销售", "reply:Java", "reply:销售"}
		actual := []string{}
		for _, event := range evidence.Timeline {
			if strings.HasPrefix(event, "greet:") || strings.HasPrefix(event, "reply:") {
				actual = append(actual, event)
			}
		}
		if fmt.Sprint(actual) != fmt.Sprint(wanted) {
			t.Fatal("后续消息提前执行或岗位顺序错误", actual)
		}
	}
}
