// 本文件验证 HRPlus 实际子项借用、迟到 AI/平台日志和原来源拒绝，不操作招聘页面。
package positionrunner

import (
	"context"
	"errors"
	"goodhr5/local-agent-go/internal/localdb"
	"goodhr5/local-agent-go/internal/planmodel"
	"strings"
	"testing"
	"time"
)

// TestPlanActualCandidateLogs 验证真正扫描和消息步骤产生的候选人日志进入原任务，而不只测试日志辅助方法。
func TestPlanActualCandidateLogs(t *testing.T) {
	t.Run("scan", func(t *testing.T) {
		child, permit, snapshot, _ := planScanFixture(t)
		if _, err := child.ScanStep(permit, snapshot); err != nil {
			t.Fatal(err)
		}
		cp, err := child.parent.runner.db.LoadActionCheckpoint(t.Context(), child.state.options.LocalRunID)
		if err != nil {
			t.Fatal(err)
		}
		logs, err := child.parent.runner.db.ListPlanItemLogs(t.Context(), cp.OwnerScope, cp.PlanRunID, cp.ItemRunID, 0, 200)
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, entry := range logs {
			if entry.TaskRunID != cp.CloudRunID {
				t.Fatal("扫描明细写入其他任务")
			}
			if strings.Contains(entry.Message, "打招呼执行：准备执行") {
				found = true
			}
		}
		if !found {
			t.Fatal("实际候选人步骤没有原任务明细")
		}
	})
	t.Run("reply", func(t *testing.T) {
		child, permit, snapshot, _ := planMessageFixture(t, 1)
		child.messages.flow.exec = platformExecutor{runner: child.parent.runner, positionID: child.positionID, once: true, logContext: child.Context()}
		if _, err := child.MessageStep(permit, snapshot, true); err != nil {
			t.Fatal(err)
		}
		cp, err := child.parent.runner.db.LoadActionCheckpoint(t.Context(), child.state.options.LocalRunID)
		if err != nil {
			t.Fatal(err)
		}
		logs, err := child.parent.runner.db.ListPlanItemLogs(t.Context(), cp.OwnerScope, cp.PlanRunID, cp.ItemRunID, 0, 200)
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, entry := range logs {
			if entry.TaskRunID != cp.CloudRunID {
				t.Fatal("回复明细写入其他任务")
			}
			if strings.Contains(entry.Message, "自动回复处理开始") {
				found = true
			}
		}
		if !found {
			t.Fatal("实际消息步骤没有原任务明细")
		}
	})
}

// planLogFixture 使用真实检查点和父占用建立同岗位独立项，来源在实际 BorrowItem 中绑定。
func planLogFixture(t *testing.T) (*Runner, *PlanBrowserReservation, localdb.ActionCheckpoint) {
	t.Helper()
	db := openRunnerTestDB(t)
	runner := newTestRunner(t, db, &fakeWorker{})
	checkpoint := localdb.ActionCheckpoint{PlanRunID: "20000000-0000-0000-0000-000000000001", ItemRunID: "50000000-0000-0000-0000-000000000001", CloudRunID: "70000000-0000-0000-0000-000000000001", OwnerScope: "original-owner", PositionID: "same-position", Platform: "boss", ProfileScope: "fixture-profile", TaskType: "greeting"}
	checkpoint, err := db.EnsurePlanActionRun(t.Context(), checkpoint)
	if err != nil {
		t.Fatal(err)
	}
	parent, err := runner.ReservePlanBrowser(t.Context(), checkpoint.PlanRunID)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = parent.Release(true) })
	return runner, parent, checkpoint
}

// TestPlanLogLateProducer 使用旧执行器和 AI 回调，在新同岗位角色开始后仍写原 A 项，不读当前角色。
func TestPlanLogLateProducer(t *testing.T) {
	runner, parent, checkpoint := planLogFixture(t)
	first, err := parent.BorrowItem(checkpoint.OwnerScope, checkpoint.ItemRunID, StartOptions{CloudRunID: checkpoint.CloudRunID, TaskType: "greeting"})
	if err != nil {
		t.Fatal(err)
	}
	oldContext := first.Context()
	oldExecutor := platformExecutor{runner: runner, positionID: checkpoint.PositionID, logContext: oldContext}
	if err := first.ReleaseAfterCleanup(true); err != nil {
		t.Fatal(err)
	}
	next := checkpoint
	next.RunID = ""
	next.ItemRunID = "50000000-0000-0000-0000-000000000002"
	next.CloudRunID = "70000000-0000-0000-0000-000000000002"
	next, err = runner.db.EnsurePlanActionRun(t.Context(), next)
	if err != nil {
		t.Fatal(err)
	}
	second, err := parent.BorrowItem(next.OwnerScope, next.ItemRunID, StartOptions{CloudRunID: next.CloudRunID, TaskType: "greeting"})
	if err != nil {
		t.Fatal(err)
	}
	defer second.ReleaseAfterCleanup(true)
	done := make(chan error, 1)
	go func() {
		done <- runner.withOperationTimeout(oldContext, checkpoint.PositionID, "夹具候选人", "迟到 AI 回调", time.Second, func(context.Context) error { return nil })
	}()
	if err := <-done; !errors.Is(err, context.Canceled) && err != nil {
		t.Fatal(err)
	}
	oldExecutor.Log("info", "A 项原平台日志")
	runner.positionContextLog(second.Context(), next.PositionID, "info", "B 项当前候选人日志")
	firstLogs, err := runner.db.ListPlanItemLogs(t.Context(), checkpoint.OwnerScope, checkpoint.PlanRunID, checkpoint.ItemRunID, 0, 100)
	if err != nil || len(firstLogs) != 3 {
		t.Fatal("原 A 的迟到日志丢失", len(firstLogs), err)
	}
	for _, entry := range firstLogs {
		if entry.TaskRunID != checkpoint.CloudRunID || entry.LocalRunID != checkpoint.RunID {
			t.Fatal("A 的旧回调混入 B", entry)
		}
	}
	secondLogs, err := runner.db.ListPlanItemLogs(t.Context(), next.OwnerScope, next.PlanRunID, next.ItemRunID, 0, 100)
	if err != nil || len(secondLogs) != 1 || secondLogs[0].TaskRunID != next.CloudRunID {
		t.Fatal("同岗位项日志未分开", secondLogs, err)
	}
	if planLogFailure(oldContext) != nil || planLogFailure(second.Context()) != nil {
		t.Fatal("正常原来源日志被误判失败")
	}
}

// TestPlanLogMissingOriginAndFailure 缺少上下文只保留旧岗位日志，错误来源在下一安全步骤前停止。
func TestPlanLogMissingOriginAndFailure(t *testing.T) {
	runner, parent, checkpoint := planLogFixture(t)
	child, err := parent.BorrowItem(checkpoint.OwnerScope, checkpoint.ItemRunID, StartOptions{CloudRunID: checkpoint.CloudRunID, TaskType: "greeting"})
	if err != nil {
		t.Fatal(err)
	}
	defer child.ReleaseAfterCleanup(true)
	runner.positionContextLog(t.Context(), checkpoint.PositionID, "info", "没有原来源的旧日志")
	logs, err := runner.db.ListPlanItemLogs(t.Context(), checkpoint.OwnerScope, checkpoint.PlanRunID, checkpoint.ItemRunID, 0, 100)
	if err != nil || len(logs) != 0 {
		t.Fatal("旧日志被猜测归到当前执行项", logs, err)
	}
	producer := child.Context().Value(planLogContextKey{}).(*planLogProducer)
	producer.original.TaskRunID = "70000000-0000-0000-0000-000000000099"
	runner.positionContextLog(child.Context(), checkpoint.PositionID, "info", "错误原任务不能保存")
	if !errors.Is(planLogFailure(child.Context()), localdb.ErrPlanRequestConflict) {
		t.Fatal("第一次日志未核对原绑定来源")
	}
	if _, err := child.ScanStep(planmodel.Permit{}, PositionRuntimeSnapshot{}); !errors.Is(err, localdb.ErrPlanRequestConflict) {
		t.Fatal("保存错误后仍推进扫描", err)
	}
	if _, err := child.MessageStep(planmodel.Permit{}, PositionRuntimeSnapshot{}, true); !errors.Is(err, localdb.ErrPlanRequestConflict) {
		t.Fatal("保存错误后仍推进消息", err)
	}
}
