// 本文件验证 HRPlus 运行结束后仍展示本次统计，状态接口不包含凭证，停止能打断休息。
package positionrunner

import (
	"context"
	"encoding/json"
	"errors"
	"goodhr5/local-agent-go/internal/cloudapi"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// TestStopRunIdentity 验证错电脑、旧运行与缺失编号不取消当前句柄，明确强停才取消本次运行。
func TestStopRunIdentity(t *testing.T) {
	s, _, _ := cooperativeSessionFixture(t, true, 1)
	called := false
	state := s.runner.running[s.position.ID]
	state.options.CloudRunID = "run-A"
	state.cancel = func() { called = true; go s.runner.clear(s.position.ID) }
	for _, id := range []string{"", "run-B", "run-old"} {
		if _, err := s.runner.StopRun(s.position.ID, id, true); err == nil || called {
			t.Fatal("错误运行编号触发停止")
		}
	}
	if _, err := s.runner.StopRun(s.position.ID, "run-A", true); err != nil || !called {
		t.Fatalf("当前运行未被明确强停 %v", err)
	}
}

// TestStopNotificationAfterCleanup 验证云端停止只在实际收尾后发送，并保存原运行确认供重试。
func TestStopNotificationAfterCleanup(t *testing.T) {
	s, _, _ := cooperativeSessionFixture(t, true, 1)
	state := s.runner.running[s.position.ID]
	checkpoint, _ := s.runner.db.LoadActionCheckpoint(t.Context(), state.options.LocalRunID)
	checkpoint.CloudRunID = state.options.CloudRunID
	if err := s.runner.db.SaveActionCheckpoint(t.Context(), checkpoint); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	cloud := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		calls.Add(1)
		if s.runner.hasRunningLock(s.position.ID) {
			t.Error("仍在收尾就向云端释放")
		}
		var identity map[string]any
		_ = json.NewDecoder(req.Body).Decode(&identity)
		if identity["run_id"] != state.options.CloudRunID || identity["machine_id"] != "machine-stop" {
			t.Errorf("未绑定原运行和电脑: %+v", identity)
		}
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer cloud.Close()
	state.options.CloudAPIBase, state.options.Token, state.options.MachineID = cloud.URL, "fixture-token", "machine-stop"
	s.runner.notifyCloudPositionStopped(s.position.ID, state.options)
	if calls.Load() != 0 {
		t.Fatal("任务尚未退出就同步云端停止")
	}
	s.runner.markUserStopped(s.position.ID)
	s.runner.clear(s.position.ID)
	if calls.Load() != 1 {
		t.Fatal("收尾后没有同步停止")
	}
	checkpoint, _ = s.runner.db.LoadActionCheckpoint(t.Context(), checkpoint.RunID)
	if !checkpoint.StopCleanupConfirmed || !checkpoint.StopCloudConfirmed {
		t.Fatal("未持久保存停止确认")
	}
	if _, err := s.runner.StopRun(s.position.ID, state.options.CloudRunID, true); err != nil {
		t.Fatalf("云端重试不能复用原收尾确认: %v", err)
	}
	if _, err := s.runner.StopRun(s.position.ID, "new-run", true); err == nil {
		t.Fatal("错误运行借用了原停止确认")
	}
}

// TestStopCleanupFailureKeepsLease 验证详情关闭失败不释放占用，清理重试成功后才能停止。
func TestStopCleanupFailureKeepsLease(t *testing.T) {
	s, _, _ := cooperativeSessionFixture(t, true, 1)
	state := s.runner.running[s.position.ID]
	failed := true
	state.pendingDetailClose = func(context.Context) error {
		if failed {
			return errors.New("fixture close failed")
		}
		return nil
	}
	s.runner.markUserStopped(s.position.ID)
	s.runner.closePendingCandidateDetail(s.position.ID)
	s.runner.clear(s.position.ID)
	status, _ := s.runner.Status(s.position.ID)
	if status["running"] != true || !state.cleanupPending {
		t.Fatal("详情未清理就宣告停止")
	}
	if s.runner.setRunning("competing", func() {}, StartOptions{}) {
		t.Fatal("详情未清理时另一任务获得页面")
	}
	failed = false
	result, err := s.runner.StopRun(s.position.ID, state.options.CloudRunID, true)
	if err != nil || result["running"] != false {
		t.Fatalf("安全重试后未停止: %v %+v", err, result)
	}
}

// stopDrainWorker 用原请求退出通知模拟取消后仍在运行的底层点击。
type stopDrainWorker struct {
	onceWorker
	entered, idle chan struct{}
}

// WaitForIdle 只在原点击完成后确认空闲，取消超时不能冒充空闲。
func (w *stopDrainWorker) WaitForIdle(ctx context.Context) error {
	close(w.entered)
	select {
	case <-w.idle:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// TestStoppedRunWaitsForWorkerIdle 验证 Go 流程已退出但原浏览器点击未完成时仍持有占用。
func TestStoppedRunWaitsForWorkerIdle(t *testing.T) {
	s, _, _ := cooperativeSessionFixture(t, true, 1)
	w := &stopDrainWorker{entered: make(chan struct{}), idle: make(chan struct{})}
	s.runner.worker = w
	s.runner.markUserStopped(s.position.ID)
	finished := make(chan struct{})
	go func() { s.runner.finishPosition(s.position.ID); close(finished) }()
	<-w.entered
	status, _ := s.runner.Status(s.position.ID)
	if status["running"] != true || s.runner.setRunning("competing", func() {}, StartOptions{}) {
		t.Fatal("底层点击未结束就释放了执行权")
	}
	close(w.idle)
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("原请求结束后没有完成收尾")
	}
	status, _ = s.runner.Status(s.position.ID)
	if status["running"] != false {
		t.Fatal("原请求退出后仍未释放")
	}
}

// TestForceStopWaitsForCleanup 验证强制取消不会在任务实际退出及页面收尾前返回停止完成。
func TestForceStopWaitsForCleanup(t *testing.T) {
	s, _, _ := cooperativeSessionFixture(t, true, 1)
	state := s.runner.running[s.position.ID]
	state.options.CloudRunID = "cleanup-run"
	canceled := make(chan struct{})
	state.cancel = func() {
		select {
		case <-canceled:
		default:
			close(canceled)
		}
	}
	returned := make(chan map[string]any, 1)
	go func() {
		result, err := s.runner.StopRun(s.position.ID, "cleanup-run", true)
		if err != nil {
			t.Error(err)
		}
		returned <- result
	}()
	<-canceled
	select {
	case <-returned:
		t.Fatal("取消信号刚发出，页面尚未收尾就返回停止完成")
	case <-time.After(60 * time.Millisecond):
	}
	if !s.runner.hasRunningLock(s.position.ID) {
		t.Fatal("收尾前释放了本地运行占用")
	}
	s.runner.clear(s.position.ID)
	select {
	case result := <-returned:
		if result["running"] != false {
			t.Fatalf("收尾后仍在运行: %+v", result)
		}
	case <-time.After(time.Second):
		t.Fatal("收尾完成后停止请求未返回")
	}
}

// TestStopCleanupRetryIsSingleOwner 验证重复停止请求不能同时清理原页面或误清除后来运行。
func TestStopCleanupRetryIsSingleOwner(t *testing.T) {
	s, _, _ := cooperativeSessionFixture(t, true, 1)
	state := s.runner.running[s.position.ID]
	state.cleanupPending = true
	entered, release := make(chan struct{}, 2), make(chan struct{})
	state.pendingDetailClose = func(context.Context) error { entered <- struct{}{}; <-release; return nil }
	results := make(chan error, 2)
	go func() { _, err := s.runner.StopRun(s.position.ID, state.options.CloudRunID, true); results <- err }()
	<-entered
	go func() { _, err := s.runner.StopRun(s.position.ID, state.options.CloudRunID, true); results <- err }()
	select {
	case <-entered:
		t.Fatal("两个请求同时执行原页面清理")
	case <-time.After(60 * time.Millisecond):
	}
	close(release)
	for i := 0; i < 2; i++ {
		select {
		case err := <-results:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(time.Second):
			t.Fatal("停止重试未结束")
		}
	}
	if !s.runner.setRunning(s.position.ID, func() {}, StartOptions{CloudRunID: "next-run"}) {
		t.Fatal("新运行未能启动")
	}
	if _, err := s.runner.StopRun(s.position.ID, state.options.CloudRunID, true); err == nil || !s.runner.hasRunningLock(s.position.ID) {
		t.Fatal("旧停止误清除新运行")
	}
}

// TestActionStatusAfterCompletion 验证清理内存运行后仍可读取本次回复和计数，不伪装新的运行。
func TestActionStatusAfterCompletion(t *testing.T) {
	s, _, _ := cooperativeSessionFixture(t, true, 1)
	if _, err := s.service(t.Context(), true, true); err != nil {
		t.Fatal(err)
	}
	checkpoint, err := s.runner.db.LoadActionCheckpoint(t.Context(), s.options.LocalRunID)
	if err != nil {
		t.Fatal(err)
	}
	checkpoint.Scanned = 18
	checkpoint.Skipped = 9
	checkpoint.Failed = 1
	if err = s.runner.db.SaveActionCheckpoint(t.Context(), checkpoint); err != nil {
		t.Fatal(err)
	}
	if _, err := s.runner.db.UpdatePositionStatus(s.position.ID, "completed"); err != nil {
		t.Fatal(err)
	}
	s.runner.clear(s.position.ID)
	status, err := s.runner.Status(s.position.ID)
	if err != nil {
		t.Fatal(err)
	}
	position := status["position"].(map[string]any)
	if status["scanned_count"] != 18 || status["greeted_count"] != 7 || status["skipped_count"] != 9 || status["failed_count"] != 1 {
		t.Fatalf("状态接口仍用累计值冒充本次 %+v", status)
	}
	if status["running"] != false || position["current_run_greeted_count"] != 7 || position["reply_stats"].(map[string]int)["replied"] != 1 || position["task_type"] != "greeting,auto_reply" {
		t.Fatalf("结束后丢失或重置本次状态 %+v", status)
	}
	if status["action_dispatch"].(map[string]any)["waiting_for_check"] != false {
		t.Fatal("完成后仍显示等待周期检查")
	}
	raw, _ := json.Marshal(status)
	if strings.Contains(string(raw), "fixture-token") {
		t.Fatal("状态输出包含凭证")
	}
}

// TestCooperativeRestChecksAndStops 验证休息期间经同一安全边界检查消息，并立即响应停止。
func TestCooperativeRestChecksAndStops(t *testing.T) {
	s, f, _ := cooperativeSessionFixture(t, true, 1)
	calls := 0
	options := s.options
	options.scanBoundary = func(ctx context.Context) (bool, error) {
		calls++
		rescan, err := s.scanBoundary(ctx)
		s.runner.markUserStopped(s.position.ID)
		return rescan, err
	}
	started := time.Now()
	err := s.runner.waitForSimulatedRest(t.Context(), s.position, s.runtime, platformExecutor{runner: s.runner, positionID: s.position.ID}, cloudapi.PlatformConfig{}, options, 1, time.Minute, time.Now().Add(time.Minute))
	if err == nil || calls != 1 || f.sends != 1 || time.Since(started) > 2*time.Second {
		t.Fatalf("休息期间未检查或停止不及时 calls=%d sends=%d err=%v", calls, f.sends, err)
	}
}
