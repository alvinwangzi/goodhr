// 本文件验证 HRPlus 扫描检查点仅记录安全结果，不把 DOM 引用、读取或未知发送当作完成。
package positionrunner

import (
	"context"
	"errors"
	"goodhr5/local-agent-go/internal/cloudapi"
	"goodhr5/local-agent-go/internal/localdb"
	"goodhr5/local-agent-go/internal/platformcore"
	"testing"
)

// TestScanCheckpointOutcomes 验证检查点保存实际计数和有序锚点，未知不进入完成集合。
func TestScanCheckpointOutcomes(t *testing.T) {
	db := openRunnerTestDB(t)
	runner := newTestRunner(t, db, &onceWorker{})
	checkpoint, err := db.CreateActionRun(t.Context(), localdb.ActionCheckpoint{PositionID: "position", ProfileScope: "scope", Platform: "boss"})
	if err != nil {
		t.Fatal(err)
	}
	options := StartOptions{LocalRunID: checkpoint.RunID, EnableGreet: true}
	queue := []map[string]any{{"id": "next", "recommendation_candidate_id": "next", "element_ref": "old-dom", "card_index": 88, "_pending_ai": make(chan int), "candidate_name": "同名"}}
	for _, id := range []string{"A", "B", "C", "D"} {
		if err = runner.saveScanCheckpoint(t.Context(), options, map[string]any{"id": id, "status": "greeted"}, queue, 2); err != nil {
			t.Fatal(err)
		}
	}
	if err = runner.saveScanCheckpoint(t.Context(), options, map[string]any{"id": "unknown", "status": "unknown"}, queue, 2); err != nil {
		t.Fatal(err)
	}
	saved, err := db.LoadActionCheckpoint(t.Context(), checkpoint.RunID)
	if err != nil || saved.Greeted != 2 || len(saved.Anchors) != 3 || saved.Anchors[0] != "B" || saved.Anchors[2] != "D" {
		t.Fatalf("检查点错误 %+v %v", saved, err)
	}
	for _, key := range []string{"element_ref", "card_index", "_pending_ai"} {
		if _, ok := saved.Queue[0][key]; ok {
			t.Fatalf("保存了页面引用或运行句柄 %s", key)
		}
	}
	ids, err := db.ActionCompletedIDs(t.Context(), checkpoint.RunID, "position")
	if err != nil || len(ids) != 4 {
		t.Fatalf("未知被当作已完成 %+v %v", ids, err)
	}
	if pending, err := runner.scanCandidateNeedsReview(t.Context(), checkpoint.RunID, "position", "unknown"); err != nil || !pending {
		t.Fatal("未知发送未要求核对")
	}
}

// failingGreetingRuntime 模拟发送后网络回执丢失，不允许再次点击。
type failingGreetingRuntime struct {
	platformcore.Runtime
	calls int
}

// GreetCandidate 记录发送尝试并返回回执未知错误。
func (f *failingGreetingRuntime) GreetCandidate(context.Context, platformcore.Executor, cloudapi.PlatformConfig, platformcore.Candidate) error {
	f.calls++
	return context.DeadlineExceeded
}

// TestCheckpointGreetingNoBlindRetry 验证新运行的发送回执未知时不使用旧次数配置盲目重发。
func TestCheckpointGreetingNoBlindRetry(t *testing.T) {
	runner := newTestRunner(t, openRunnerTestDB(t), &onceWorker{})
	runtime := &failingGreetingRuntime{}
	err := runner.tryGreet(t.Context(), "position", runtime, platformExecutor{runner: runner, positionID: "position"}, cloudapi.PlatformConfig{}, map[string]any{"id": "real-ID"}, StartOptions{LocalRunID: "local-run", GreetRetries: 5})
	if !errors.Is(err, context.DeadlineExceeded) || runtime.calls != 1 {
		t.Fatalf("结果未知仍重复发送：calls=%d err=%v", runtime.calls, err)
	}
}
