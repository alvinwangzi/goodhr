// 本文件用实际 M1 与受控 Worker 验证 HRPlus 新调度对象恢复原领取及检查点，不操作真实招聘页面。
package planrunner

import (
	"context"
	"goodhr5/local-agent-go/internal/planmodel"
	"goodhr5/local-agent-go/internal/positionrunner"
	"sync/atomic"
	"testing"
	"time"
)

// TestSchedulerRestoresOriginalRunning 验证丢弃内存运行对象后仍使用原任务、占用、数量和已保存原领取请求。
func TestSchedulerRestoresOriginalRunning(t *testing.T) {
	worker := &runtimeWorker{}
	c, plan, _, a, calls, _ := acquireFixtureWithWorker(t, &atomic.Int32{}, worker, schedulerPermit)
	firstScheduler := NewScheduler(c)
	plans := []planmodel.Plan{plan}
	if err := firstScheduler.EnqueueDue(t.Context(), plans, plan.MachineID, a); err != nil {
		t.Fatal(err)
	}
	first, err := firstScheduler.AcquireNext(t.Context(), plans, plan.MachineID, a)
	if err != nil {
		t.Fatal(err)
	}
	options := positionrunner.StartOptions{PageReadyDelay: 1}
	runtime := NewM1ExecutionRuntime(c, first.Held, a, options)
	prepared, err := runtime.Prepare(t.Context(), first.Held.Permit)
	if err != nil {
		t.Fatal(err)
	}
	start, _ := cloneLoopRun(prepared.Run)
	start.Sequence++
	start.State = "running"
	start.Items[0].State = "running"
	for action, progress := range start.Items[0].Actions {
		progress.State = "active"
		start.Items[0].Actions[action] = progress
	}
	permit, err := runtime.Save(t.Context(), start)
	if err != nil {
		t.Fatal(err)
	}
	step, err := runtime.Scan(t.Context(), permit)
	if err != nil || step.Greeted != 1 {
		t.Fatal("实际 M1 没有保存第一步", err)
	}
	progress, _ := cloneLoopRun(permit.Run)
	progress.Sequence++
	value := progress.Items[0].Actions["greeting"]
	value.Count = 1
	progress.Items[0].Actions["greeting"] = value
	permit, err = runtime.Save(t.Context(), progress)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.CloseItem(t.Context(), permit.Run.Items[0].ID); err != nil {
		t.Fatal(err)
	}
	if err := first.Held.Reservation.Release(true); err != nil {
		t.Fatal(err)
	}
	// 仅销毁本地运行引用，保留原云端许可，模拟调度对象丢失；不冒称原生进程重启。
	restarted := New(c.db, c.runner, c.client, c.now)
	next, err := NewScheduler(restarted).AcquireNext(t.Context(), plans, plan.MachineID, a)
	if err != nil {
		t.Fatal(err)
	}
	defer next.Held.Reservation.Release(true)
	if next.Request.RequestID != first.Request.RequestID || next.Held.Permit.Run.ID != permit.Run.ID || next.Held.Permit.Owner.OwnerID != permit.Owner.OwnerID || next.Held.Permit.Run.Items[0].TaskRunID != permit.Run.Items[0].TaskRunID || next.Held.Permit.Run.Items[0].Actions["greeting"].Count != 1 || calls.Load() != 2 {
		t.Fatal("恢复重置原身份、数量或重新创建占用")
	}
	resumed := NewM1ExecutionRuntime(restarted, next.Held, a, options)
	defer func() {
		for id := range resumed.items {
			_ = resumed.CloseItem(context.Background(), id)
		}
	}()
	end, err := NewExecutionLoop(resumed, a.OwnerScope, restarted.now).Run(t.Context(), next.Held.Permit)
	if err != nil || end.Run.State != "completed" || end.Run.Items[0].TaskRunID != permit.Run.Items[0].TaskRunID || end.Run.Items[0].Actions["greeting"].Count != 1 {
		t.Fatal("恢复后未沿原任务完成", err)
	}
}

// TestSchedulerDoesNotRestoreOldDate 验证已领取记录不提供次日或名义时段外的页面恢复资格。
func TestSchedulerDoesNotRestoreOldDate(t *testing.T) {
	c, plan, _, a, calls, clock := acquireFixture(t, &atomic.Int32{}, schedulerPermit)
	scheduler := NewScheduler(c)
	plans := []planmodel.Plan{plan}
	if err := scheduler.EnqueueDue(t.Context(), plans, plan.MachineID, a); err != nil {
		t.Fatal(err)
	}
	first, err := scheduler.AcquireNext(t.Context(), plans, plan.MachineID, a)
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Held.Reservation.Release(true); err != nil {
		t.Fatal(err)
	}
	for _, now := range []time.Time{time.Date(2026, 10, 10, 4, 0, 0, 0, time.UTC), time.Date(2026, 10, 11, 1, 0, 0, 0, time.UTC)} {
		clock.Store(now.UnixNano())
		if restored, err := scheduler.AcquireNext(t.Context(), plans, plan.MachineID, a); err != nil || restored != nil || calls.Load() != 1 {
			t.Fatal("时段外或次日恢复旧运行", err)
		}
	}
}

// TestRecoveredPreparationUsesOriginalRequest 验证准备回执丢失后重试不会登记第二个准备编号。
func TestRecoveredPreparationUsesOriginalRequest(t *testing.T) {
	mode := &atomic.Int32{}
	c, plan, claim, a, _, _ := acquireFixture(t, mode, schedulerPermit)
	held, err := c.Acquire(t.Context(), plan, claim, a)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Reservation.Release(true)
	firstID := "60000000-0000-0000-0000-000000000099"
	mode.Store(7)
	if _, err := c.PrepareItem(t.Context(), held, held.Permit.Run, firstID, a); err == nil {
		t.Fatal("回执丢失被当作成功")
	}
	mode.Store(0)
	if _, err := c.PrepareItem(t.Context(), held, held.Permit.Run, "60000000-0000-0000-0000-000000000098", a); err != nil {
		t.Fatal(err)
	}
	operations, err := c.db.PlanRunOperations(t.Context(), a.OwnerScope, held.Permit.Run.ID)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, o := range operations {
		if o.Kind == "prepare_item" {
			count++
			if o.RequestID != firstID || o.State != "confirmed" {
				t.Fatal("未沿用原准备编号")
			}
		}
	}
	if count != 1 {
		t.Fatal("同一次准备登记了多个请求", count)
	}
}
