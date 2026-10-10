// 本文件用真实 SQLite、Windows 加密及受控云端验证 HRPlus 调度排队、原请求重试和同日恢复，不操作真实候选人。
package planrunner

import (
	"context"
	"errors"
	"goodhr5/local-agent-go/internal/planmodel"
	"goodhr5/local-agent-go/internal/positionrunner"
	"sync/atomic"
	"testing"
	"time"
)

// schedulerPermit 使隔离云端使用实际调度运行编号，并设置受控打招呼主项。
func schedulerPermit(p *planmodel.Permit) {
	p.Run.ID = scheduledRunID(planmodel.Plan{ID: p.Run.PlanID, ActivationID: p.Run.ActivationID}, p.Run.ExecutionDate)
	for i := range p.Run.Items {
		p.Run.Snapshot.Items[i].Actions = []string{"greeting"}
		p.Run.Snapshot.Items[i].PrioritizeReply = false
		p.Run.Items[i].Snapshot = p.Run.Snapshot.Items[i]
		p.Run.Items[i].Actions = map[string]planmodel.ActionProgress{"greeting": {State: "pending"}}
	}
}

// TestSchedulerBusyRetry 验证本地预留冲突不访问云端，云端忙保留原编号和加密凭证，释放后才开始。
func TestSchedulerBusyRetry(t *testing.T) {
	mode := &atomic.Int32{}
	c, plan, _, a, calls, clock := acquireFixture(t, mode, schedulerPermit)
	scheduler := NewScheduler(c)
	plans := []planmodel.Plan{plan}
	if err := scheduler.EnqueueDue(t.Context(), plans, plan.MachineID, a); err != nil {
		t.Fatal(err)
	}
	first, err := c.db.NextPlanRequest(t.Context(), a.OwnerScope)
	if err != nil {
		t.Fatal(err)
	}
	clock.Add(int64(time.Second))
	if err := scheduler.EnqueueDue(t.Context(), plans, plan.MachineID, a); err != nil {
		t.Fatal(err)
	}
	waiting, err := c.db.WaitingPlanRequests(t.Context(), a.OwnerScope)
	if err != nil || len(waiting) != 1 || waiting[0].Sequence != first.Sequence || !waiting[0].TriggeredAt.Equal(first.TriggeredAt) {
		t.Fatal("重复触发重新入队", err)
	}
	other, err := c.runner.ReservePlanBrowser(t.Context(), "90000000-0000-0000-0000-000000000001")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := scheduler.AcquireNext(t.Context(), plans, plan.MachineID, a); !errors.Is(err, positionrunner.ErrPlanBrowserBusy) || calls.Load() != 0 {
		t.Fatal("本地忙仍访问云端", err)
	}
	if err := other.Release(true); err != nil {
		t.Fatal(err)
	}
	mode.Store(1)
	if _, err := scheduler.AcquireNext(t.Context(), plans, plan.MachineID, a); err == nil {
		t.Fatal("云端忙仍启动")
	}
	original, err := c.requests.OriginalClaim(t.Context(), a.OwnerScope, first.RequestID)
	if err != nil {
		t.Fatal(err)
	}
	mode.Store(0)
	execution, err := scheduler.AcquireNext(t.Context(), plans, plan.MachineID, a)
	if err != nil {
		t.Fatal(err)
	}
	defer execution.Held.Reservation.Release(true)
	retry, err := c.requests.OriginalClaim(t.Context(), a.OwnerScope, first.RequestID)
	if err != nil || retry != original || calls.Load() != 2 {
		t.Fatal("重试改写原请求或重复领取", err)
	}
	if err := scheduler.ConfirmFinished(t.Context(), execution); err == nil {
		t.Fatal("页面占用尚在就确认完成")
	}
	waiting, err = c.db.WaitingPlanRequests(t.Context(), a.OwnerScope)
	if err != nil || len(waiting) != 0 {
		t.Fatal("已开始项仍作为等待项", err)
	}
}

// TestSchedulerSameDayResume 验证上午原步骤保存后下午沿用任务及数量，次日使用新运行身份。
func TestSchedulerSameDayResume(t *testing.T) {
	worker := &runtimeWorker{}
	c, plan, _, a, _, clock := acquireFixtureWithWorker(t, &atomic.Int32{}, worker, schedulerPermit)
	scheduler := NewScheduler(c)
	plans := []planmodel.Plan{plan}
	if err := scheduler.EnqueueDue(t.Context(), plans, plan.MachineID, a); err != nil {
		t.Fatal(err)
	}
	first, err := scheduler.AcquireNext(t.Context(), plans, plan.MachineID, a)
	if err != nil {
		t.Fatal(err)
	}
	worker.afterGreet = func() { clock.Store(first.Held.finishAt.UnixNano()) }
	runtime := NewM1ExecutionRuntime(c, first.Held, a, positionrunner.StartOptions{PageReadyDelay: 1})
	defer func() {
		for id := range runtime.items {
			_ = runtime.CloseItem(context.Background(), id)
		}
		_ = first.Held.Reservation.Release(true)
	}()
	morning, err := NewExecutionLoop(runtime, a.OwnerScope, c.now).Run(t.Context(), first.Held.Permit)
	if err != nil {
		t.Fatal(err)
	}
	if morning.Run.State != "waiting_window" || morning.Run.Items[0].Actions["greeting"].Count != 1 {
		t.Fatal("上午进度丢失")
	}
	if err := scheduler.ConfirmFinished(t.Context(), first); err != nil {
		t.Fatal(err)
	}
	worker.afterGreet = nil
	clock.Store(time.Date(2026, 10, 10, 5, 30, 0, 0, time.UTC).UnixNano())
	if err := scheduler.EnqueueDue(t.Context(), plans, plan.MachineID, a); err != nil {
		t.Fatal(err)
	}
	second, err := scheduler.AcquireNext(t.Context(), plans, plan.MachineID, a)
	if err != nil {
		t.Fatal(err)
	}
	if second.Held.Permit.Run.ID != morning.Run.ID || second.Held.Permit.Run.Items[0].TaskRunID != morning.Run.Items[0].TaskRunID || second.Held.Permit.Run.Items[0].Actions["greeting"].Count != 1 || second.Held.Permit.Owner.OwnerID == first.Held.Permit.Owner.OwnerID {
		t.Fatal("下午未恢复原任务数量或复用已释放占用")
	}
	resumed := NewM1ExecutionRuntime(c, second.Held, a, positionrunner.StartOptions{PageReadyDelay: 1})
	defer func() {
		for id := range resumed.items {
			_ = resumed.CloseItem(context.Background(), id)
		}
		_ = second.Held.Reservation.Release(true)
	}()
	end, err := NewExecutionLoop(resumed, a.OwnerScope, c.now).Run(t.Context(), second.Held.Permit)
	if err != nil || end.Run.State != "completed" {
		t.Fatal("下午续跑未结束", err)
	}
	if err := scheduler.ConfirmFinished(t.Context(), second); err != nil {
		t.Fatal(err)
	}
	if err := scheduler.EnqueueDue(t.Context(), plans, plan.MachineID, a); err != nil {
		t.Fatal(err)
	}
	if pending, _ := c.db.WaitingPlanRequests(t.Context(), a.OwnerScope); len(pending) != 0 {
		t.Fatal("当天完成后重复触发")
	}
	clock.Store(time.Date(2026, 10, 11, 1, 0, 0, 0, time.UTC).UnixNano())
	if err := scheduler.EnqueueDue(t.Context(), plans, plan.MachineID, a); err != nil {
		t.Fatal(err)
	}
	if next, err := c.db.NextPlanRequest(t.Context(), a.OwnerScope); err != nil || next.RequestID == first.Request.RequestID || scheduledRunID(plan, "2026-10-11") == morning.Run.ID {
		t.Fatal("次日沿用旧运行", err)
	}
}

// TestSchedulerNominalWindowAndAuthority 验证窗口外不登记或领取，旧授权不能消费等待项。
func TestSchedulerNominalWindowAndAuthority(t *testing.T) {
	c, plan, _, a, calls, clock := acquireFixture(t, &atomic.Int32{}, schedulerPermit)
	scheduler := NewScheduler(c)
	plans := []planmodel.Plan{plan}
	clock.Store(time.Date(2026, 10, 10, 0, 59, 59, 0, time.UTC).UnixNano())
	if err := scheduler.EnqueueDue(t.Context(), plans, plan.MachineID, a); err != nil {
		t.Fatal(err)
	}
	if waiting, _ := c.db.WaitingPlanRequests(t.Context(), a.OwnerScope); len(waiting) != 0 {
		t.Fatal("开始时间之前登记任务")
	}
	clock.Store(time.Date(2026, 10, 10, 1, 0, 0, 0, time.UTC).UnixNano())
	if err := scheduler.EnqueueDue(t.Context(), plans, plan.MachineID, a); err != nil {
		t.Fatal(err)
	}
	clock.Store(time.Date(2026, 10, 10, 4, 0, 0, 0, time.UTC).UnixNano())
	if execution, err := scheduler.AcquireNext(t.Context(), plans, plan.MachineID, a); err != nil || execution != nil || calls.Load() != 0 {
		t.Fatal("名义结束后开始新任务", err)
	}
	a.StillCurrent = func() bool { return false }
	if _, err := scheduler.AcquireNext(t.Context(), nil, plan.MachineID, a); !errors.Is(err, ErrPlanAuthority) {
		t.Fatal("旧登录消费队列", err)
	}
	if waiting, _ := c.db.WaitingPlanRequests(t.Context(), a.OwnerScope); len(waiting) != 1 {
		t.Fatal("旧登录取消原等待项")
	}
}
