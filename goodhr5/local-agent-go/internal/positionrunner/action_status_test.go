// 本文件验证 HRPlus 运行结束后仍展示本次统计，状态接口不包含凭证，停止能打断休息。
package positionrunner

import (
	"context"
	"encoding/json"
	"goodhr5/local-agent-go/internal/cloudapi"
	"strings"
	"testing"
	"time"
)

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
