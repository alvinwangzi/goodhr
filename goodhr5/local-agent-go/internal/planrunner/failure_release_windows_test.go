// 本文件用真实 M1、Windows 加密和临时 SQLite 验证 HRPlus 异常收尾保留原数量与未确认请求，不操作真实候选人。
package planrunner

import (
	"context"
	"errors"
	"goodhr5/local-agent-go/internal/positionrunner"
	"sync/atomic"
	"testing"
)

// TestFailureReleasePreservesUnconfirmedProgress 验证已发生步骤的上报不明确时先补原状态再补原释放。
func TestFailureReleasePreservesUnconfirmedProgress(t *testing.T) {
	mode := &atomic.Int32{}
	worker := &runtimeWorker{}
	c, plan, claim, a, _, _ := acquireFixtureWithWorker(t, mode, worker, schedulerPermit)
	held, err := c.Acquire(t.Context(), plan, claim, a)
	if err != nil {
		t.Fatal(err)
	}
	runtime := NewM1ExecutionRuntime(c, held, a, positionrunner.StartOptions{PageReadyDelay: 1})
	defer func() {
		for id := range runtime.items {
			_ = runtime.CloseItem(context.Background(), id)
		}
		_ = held.Reservation.Release(true)
	}()
	worker.afterGreet = func() { mode.Store(2) }
	if _, err := NewExecutionLoop(runtime, a.OwnerScope, c.now).Run(t.Context(), held.Permit); err == nil {
		t.Fatal("上报没有原回执仍推进")
	}
	pending, err := c.db.NextPlanUpdate(t.Context(), a.OwnerScope)
	if err != nil || pending.Kind != "status" {
		t.Fatal("丢失未确认原状态", err)
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	op, err := runtime.StageFailureRelease(cancelled, "blocked", "plan_execution_failed")
	if err != nil {
		t.Fatal(err)
	}
	original, err := c.requests.OriginalUpdate(t.Context(), a.OwnerScope, op.RequestID)
	if err != nil || original.Sequence != pending.RunSequence+1 || original.Items[0].Actions["greeting"].Count != 1 || held.Reservation.Valid() {
		t.Fatal("原数量或释放顺序丢失", err)
	}
	retry, err := runtime.StageFailureRelease(t.Context(), "blocked", "plan_execution_failed")
	if err != nil || retry.RequestID != op.RequestID || retry.BodyHash != op.BodyHash {
		t.Fatal("重复收尾改写原释放", err)
	}
	mode.Store(0)
	for i := 0; i < 2; i++ {
		if sent, err := c.requests.UploadNext(t.Context(), c.client, a); err != nil || !sent {
			t.Fatal("不能按原顺序补传", err)
		}
	}
	run, err := c.db.PlanRunSnapshot(t.Context(), a.OwnerScope, held.Permit.Run.ID)
	if err != nil || run.State != "blocked" || run.Items[0].Actions["greeting"].Count != 1 || run.Items[1].TaskRunID != "" {
		t.Fatal("结算未保留实际事实", err)
	}
}

// TestFailureReleaseAfterAuthorityLoss 验证授权变化后不再访问云端，仍可保存实际数量与原释放供同账号恢复补传。
func TestFailureReleaseAfterAuthorityLoss(t *testing.T) {
	worker := &runtimeWorker{}
	c, plan, claim, a, _, _ := acquireFixtureWithWorker(t, &atomic.Int32{}, worker, schedulerPermit)
	current := &atomic.Bool{}
	current.Store(true)
	a.StillCurrent = current.Load
	held, err := c.Acquire(t.Context(), plan, claim, a)
	if err != nil {
		t.Fatal(err)
	}
	runtime := NewM1ExecutionRuntime(c, held, a, positionrunner.StartOptions{PageReadyDelay: 1})
	defer func() {
		for id := range runtime.items {
			_ = runtime.CloseItem(context.Background(), id)
		}
		_ = held.Reservation.Release(true)
	}()
	worker.afterGreet = func() { current.Store(false) }
	if _, err := NewExecutionLoop(runtime, a.OwnerScope, c.now).Run(t.Context(), held.Permit); !errors.Is(err, ErrPlanAuthority) {
		t.Fatal("旧授权继续执行", err)
	}
	op, err := runtime.StageFailureRelease(t.Context(), "stopped", "plan_authority_changed")
	if err != nil {
		t.Fatal(err)
	}
	original, err := c.requests.OriginalUpdate(t.Context(), a.OwnerScope, op.RequestID)
	if err != nil || original.Items[0].Actions["greeting"].Count != 1 || held.Reservation.Valid() {
		t.Fatal("登录变化丢失已完成数量", err)
	}
	if sent, err := c.requests.UploadNext(t.Context(), c.client, a); err == nil || sent {
		t.Fatal("旧授权发送释放")
	}
	current.Store(true)
	if sent, err := c.requests.UploadNext(t.Context(), c.client, a); err != nil || !sent {
		t.Fatal("同账号恢复未补传原释放", err)
	}
}

// TestFailureReleaseReadsUnknownPreparation 验证创建任务回执丢失时先核对真实关联，授权无效不能猜测序号并释放。
func TestFailureReleaseReadsUnknownPreparation(t *testing.T) {
	mode := &atomic.Int32{}
	c, plan, claim, a, _, _ := acquireFixture(t, mode, schedulerPermit)
	current := &atomic.Bool{}
	current.Store(true)
	a.StillCurrent = current.Load
	held, err := c.Acquire(t.Context(), plan, claim, a)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Reservation.Release(true)
	mode.Store(7)
	if _, err := c.PrepareItem(t.Context(), held, held.Permit.Run, "60000000-0000-0000-0000-000000000099", a); err == nil {
		t.Fatal("丢失准备许可仍成功")
	}
	runtime := NewM1ExecutionRuntime(c, held, a, positionrunner.StartOptions{})
	current.Store(false)
	if _, err := runtime.StageFailureRelease(t.Context(), "blocked", "plan_prepare_failed"); !errors.Is(err, ErrPlanAuthority) || !held.Reservation.Valid() {
		t.Fatal("未核对关联就释放", err)
	}
	current.Store(true)
	if err := runtime.UseSettlementAuthority(t.Context(), a); err != nil {
		t.Fatal("同账号收尾授权不能核对", err)
	}
	if _, err := runtime.Boundary(t.Context(), held.Permit); !errors.Is(err, ErrPlanNeedsSettlement) {
		t.Fatal("收尾授权重新开放页面步骤", err)
	}
	op, err := runtime.StageFailureRelease(t.Context(), "blocked", "plan_prepare_failed")
	if err != nil {
		t.Fatal(err)
	}
	original, err := c.requests.OriginalUpdate(t.Context(), a.OwnerScope, op.RequestID)
	run, readErr := c.db.PlanRunSnapshot(t.Context(), a.OwnerScope, held.Permit.Run.ID)
	if err != nil || readErr != nil || original.Sequence != 3 || run.Items[0].TaskRunID == "" || held.Reservation.Valid() {
		t.Fatal("未使用已核对原任务与序号", err, readErr)
	}
}
