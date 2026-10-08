// 本文件验证 HRPlus 扫描检查点仅记录安全结果，不把 DOM 引用、读取或未知发送当作完成。
package positionrunner

import (
	"context"
	"errors"
	"fmt"
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
	checkpoint, err := runner.db.CreateActionRun(t.Context(), localdb.ActionCheckpoint{PositionID: "position", ProfileScope: "scope", Platform: "boss"})
	if err != nil {
		t.Fatal(err)
	}
	if err = runner.db.SaveActionCandidate(t.Context(), checkpoint, "real-ID", "processing", "candidate_processing"); err != nil {
		t.Fatal(err)
	}
	err = runner.tryGreet(t.Context(), "position", runtime, platformExecutor{runner: runner, positionID: "position"}, cloudapi.PlatformConfig{}, map[string]any{"id": "real-ID"}, StartOptions{LocalRunID: checkpoint.RunID, GreetRetries: 5})
	if !errors.Is(err, context.DeadlineExceeded) || runtime.calls != 1 {
		t.Fatalf("结果未知仍重复发送：calls=%d err=%v", runtime.calls, err)
	}
}

// TestUnknownScanProtectionAcrossRuns 验证重新开始不清空发送保护，纯读取中断可以重新处理，账号作用域互相隔离。
func TestUnknownScanProtectionAcrossRuns(t *testing.T) {
	db := openRunnerTestDB(t)
	r := newTestRunner(t, db, &onceWorker{})
	old, err := db.CreateActionRun(t.Context(), localdb.ActionCheckpoint{PositionID: "old-position", ProfileScope: "scope", Platform: "boss"})
	if err != nil {
		t.Fatal(err)
	}
	if err = db.SaveActionCandidate(t.Context(), old, "uncertain", "processing", "greet_sending"); err != nil {
		t.Fatal(err)
	}
	if err = db.SaveActionCandidate(t.Context(), old, "read-only", "processing", "candidate_processing"); err != nil {
		t.Fatal(err)
	}
	current, err := db.CreateActionRun(t.Context(), localdb.ActionCheckpoint{PositionID: "new-position", ProfileScope: "scope", Platform: "boss"})
	if err != nil {
		t.Fatal(err)
	}
	input := []map[string]any{{"id": "uncertain"}, {"id": "read-only"}}
	eligible, err := r.recheckUnknownScanCandidates(t.Context(), current.PositionID, StartOptions{LocalRunID: current.RunID}, &observedCandidateRuntime{}, platformExecutor{runner: r, positionID: current.PositionID}, cloudapi.PlatformConfig{}, input)
	if err != nil || len(eligible) != 1 || stringFromMap(eligible[0], "id") != "read-only" {
		t.Fatalf("新运行丢保护或把读取当发送 %+v %v", eligible, err)
	}
	other, err := db.CreateActionRun(t.Context(), localdb.ActionCheckpoint{PositionID: "other-position", ProfileScope: "other-scope", Platform: "boss"})
	if err != nil {
		t.Fatal(err)
	}
	eligible, err = r.recheckUnknownScanCandidates(t.Context(), other.PositionID, StartOptions{LocalRunID: other.RunID}, &observedCandidateRuntime{}, platformExecutor{runner: r, positionID: other.PositionID}, cloudapi.PlatformConfig{}, input)
	if err != nil || len(eligible) != 2 {
		t.Fatal("跨账号错误套用了未知记录")
	}
}

// TestUnknownScanRecheck 验证未知发送必须读取页面新事实，正向事实不当作本次确认成功，也不重新评分发送。
func TestUnknownScanRecheck(t *testing.T) {
	for _, observed := range []bool{false, true} {
		t.Run(fmt.Sprint(observed), func(t *testing.T) {
			db := openRunnerTestDB(t)
			r := newTestRunner(t, db, &onceWorker{})
			checkpoint, err := db.CreateActionRun(t.Context(), localdb.ActionCheckpoint{PositionID: "p", ProfileScope: "scope", Platform: "boss", Greeted: 2})
			if err != nil {
				t.Fatal(err)
			}
			if err = db.SaveActionCandidate(t.Context(), checkpoint, "real-ID", "unknown", "send_unconfirmed"); err != nil {
				t.Fatal(err)
			}
			runtime := &observedCandidateRuntime{state: platformcore.CandidatePageState{ContactObserved: observed}}
			eligible, err := r.recheckUnknownScanCandidates(t.Context(), "p", StartOptions{LocalRunID: checkpoint.RunID, EnableGreet: true}, runtime, platformExecutor{runner: r, positionID: "p"}, cloudapi.PlatformConfig{}, []map[string]any{{"id": "real-ID", "status": "scanned"}})
			if err != nil || len(eligible) != 0 {
				t.Fatalf("未知目标重进了评分或发送队列 %v %+v", err, eligible)
			}
			status, err := db.ActionCandidateStatus(t.Context(), checkpoint.RunID, "p", "real-ID")
			if err != nil {
				t.Fatal(err)
			}
			want := "unknown"
			if observed {
				want = "skipped"
			}
			if status != want {
				t.Fatalf("%s != %s", status, want)
			}
			saved, err := db.LoadActionCheckpoint(t.Context(), checkpoint.RunID)
			if err != nil || saved.Greeted != 2 {
				t.Fatal("观察事实增加了成功数")
			}
		})
	}
}
