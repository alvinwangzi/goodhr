// 本文件用实际 M1、Windows DPAPI 和受控 Worker 联调 HRPlus 重复岗位主循环，不操作真实招聘页面。
package planrunner

import (
	"context"
	"errors"
	"fmt"
	"goodhr5/local-agent-go/internal/browser"
	"goodhr5/local-agent-go/internal/planclock"
	"goodhr5/local-agent-go/internal/planmodel"
	"goodhr5/local-agent-go/internal/positionrunner"
	"sync/atomic"
	"testing"
	"time"
)

// runtimeWorker 用标准 Worker 协议提供受控页面事实，统计发送由真实 M1 和检查点实现。
type runtimeWorker struct {
	starts, greets int
	afterGreet     func()
}

// TestAcquireMidnightWindow 验证结束分钟为 1440 的窗口仍保存正确延迟，不误读为零点零分钟。
func TestAcquireMidnightWindow(t *testing.T) {
	c, plan, claim, a, _, _ := acquireFixture(t, &atomic.Int32{}, func(p *planmodel.Permit) {
		p.Run.Snapshot.Schedule.Windows = []planclock.Window{{Order: 0, StartMinute: 540, EndMinute: 1440}}
	})
	c.grace = func() time.Duration { return 6 * time.Minute }
	held, err := c.Acquire(t.Context(), plan, claim, a)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Reservation.Release(true)
	if !held.lastWindow || held.finishAt.In(held.window.End.Location()).Format("2006-01-02 15:04") != "2026-10-11 00:06" {
		t.Fatal("午夜窗口截止时间错误", held.finishAt)
	}
}

// TestAcquireRejectsInvalidGrace 验证随机源失败不能领取云端占用或留下本地执行权。
func TestAcquireRejectsInvalidGrace(t *testing.T) {
	c, plan, claim, a, calls, _ := acquireFixture(t, &atomic.Int32{})
	c.grace = func() time.Duration { return 0 }
	if _, err := c.Acquire(t.Context(), plan, claim, a); err == nil || calls.Load() != 0 {
		t.Fatal("无有效随机时间仍领取占用", err)
	}
	parent, err := c.runner.ReservePlanBrowser(t.Context(), claim.RunID)
	if err != nil {
		t.Fatal("随机失败遗留本地执行权", err)
	}
	defer parent.Release(true)
}

// Start 返回隔离 Worker 状态，不启动浏览器进程。
func (w *runtimeWorker) Start(context.Context) (browser.WorkerStatus, error) {
	w.starts++
	return browser.WorkerStatus{Running: true}, nil
}

// Call 根据标准接口返回页面和账号事实，不执行页面脚本。
func (w *runtimeWorker) Call(ctx context.Context, path string, payload any) (map[string]any, error) {
	data := map[string]any{}
	switch path {
	case "/api/v1/boss/account/identity":
		data = map[string]any{"verified": true, "account_id": "fixture-account"}
	case "/api/v1/page/list":
		data = map[string]any{"pages": []any{map[string]any{"page_id": "0", "is_default": true, "url": "https://www.zhipin.com/web/chat/recommend"}}}
	case "/api/v1/page/extract-text":
		data = map[string]any{"text": "fixture", "texts": []any{"fixture"}}
	case "/api/v1/boss/candidates/extract":
		data = map[string]any{"candidates": []any{map[string]any{"id": "fixture-geek", "name": "同名", "candidate_name": "同名", "status": "scanned", "raw_text": "本科 5年", "fields": map[string]any{"name": "同名", "basic_info": "本科 5年"}}}}
	case "/api/v1/boss/candidates/greet":
		w.greets++
		if w.afterGreet != nil {
			w.afterGreet()
		}
		data = map[string]any{"greeted": true}
	case "/api/v1/boss/candidates/capture-anchors":
		data = map[string]any{"valid": true, "signature": "fixture-role"}
	}
	return map[string]any{"data": data}, nil
}

// TestM1WindowBoundary 验证名义结束后不准备下一项，已有主项可继续至持久化随机截止点。
func TestM1WindowBoundary(t *testing.T) {
	c, plan, claim, a, _, clock := acquireFixture(t, &atomic.Int32{})
	c.grace = func() time.Duration { return 4 * time.Minute }
	held, err := c.Acquire(t.Context(), plan, claim, a)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = held.Reservation.Release(true) })
	runtime := NewM1ExecutionRuntime(c, held, a, positionrunner.StartOptions{})
	if !held.finishAt.Equal(held.window.End.Add(4 * time.Minute)) {
		t.Fatal("未保存随机截止点")
	}
	clock.Store(held.window.End.UnixNano())
	run, _ := cloneLoopRun(held.Permit.Run)
	run.Items[run.CurrentItem].State = "running"
	if state, err := runtime.Boundary(t.Context(), planmodel.Permit{Run: run}); err != nil || state != "" {
		t.Fatal("延后期间已有主项不能继续", state, err)
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := runtime.Boundary(cancelled, held.Permit); !errors.Is(err, context.Canceled) {
		t.Fatal("新主项不应在延后期间进入", err)
	}
	clock.Store(held.finishAt.UnixNano())
	if state, err := runtime.Boundary(t.Context(), held.Permit); err != nil || state != "waiting_window" {
		t.Fatal("窗口截止未等待下午", state, err)
	}
	// 再次领取同一原请求仍读取第一次抽样，不采用新的随机值。
	if err := held.Reservation.Release(true); err != nil {
		t.Fatal(err)
	}
	c.grace = func() time.Duration { t.Fatal("相同窗口不应重复抽样"); return 6 * time.Minute }
	clock.Store(held.window.Start.UnixNano())
	again, err := c.Acquire(t.Context(), plan, claim, a)
	if err != nil {
		t.Fatal(err)
	}
	defer again.Reservation.Release(true)
	if !again.finishAt.Equal(held.finishAt) {
		t.Fatal("重试改变随机截止点")
	}
}

// TestM1WindowExpiryAfterSafeStep 验证到期保留原项数量并释放父占用，不准备第二项或再次扫描。
func TestM1WindowExpiryAfterSafeStep(t *testing.T) {
	for _, laterWindow := range []bool{false, true} {
		t.Run(fmt.Sprint(laterWindow), func(t *testing.T) {
			worker := &runtimeWorker{}
			c, plan, claim, a, _, clock := acquireFixtureWithWorker(t, &atomic.Int32{}, worker, func(p *planmodel.Permit) {
				if !laterWindow {
					p.Run.Snapshot.Schedule.Windows = p.Run.Snapshot.Schedule.Windows[:1]
				}
				for i := range p.Run.Items {
					p.Run.Snapshot.Items[i].Actions = []string{"greeting"}
					p.Run.Snapshot.Items[i].PrioritizeReply = false
					p.Run.Items[i].Snapshot = p.Run.Snapshot.Items[i]
					p.Run.Items[i].Actions = map[string]planmodel.ActionProgress{"greeting": {State: "pending"}}
				}
			})
			c.grace = func() time.Duration { return 3 * time.Minute }
			held, err := c.Acquire(t.Context(), plan, claim, a)
			if err != nil {
				t.Fatal(err)
			}
			defer held.Reservation.Release(true)
			worker.afterGreet = func() { clock.Store(held.finishAt.UnixNano()) }
			runtime := NewM1ExecutionRuntime(c, held, a, positionrunner.StartOptions{PageReadyDelay: 1})
			result, err := NewExecutionLoop(runtime, a.OwnerScope, c.now).Run(t.Context(), held.Permit)
			if err != nil {
				for id := range runtime.items {
					_ = runtime.CloseItem(context.Background(), id)
				}
				t.Fatal(err)
			}
			want := "incomplete"
			if laterWindow {
				want = "waiting_window"
			}
			if result.Run.State != want || result.Run.CurrentItem != 0 || result.Run.Items[0].Actions["greeting"].Count != 1 || result.Run.Items[1].TaskRunID != "" || worker.greets != 1 || held.Reservation.Valid() {
				t.Fatal("窗口到期未保留原进度或执行了后续岗位", result.Run.State, result.Run.CurrentItem, worker.greets)
			}
		})
	}
}

// CallOnce 复用受控接口，不绕过 M1 单次发送边界。
func (w *runtimeWorker) CallOnce(ctx context.Context, path string, payload any) (map[string]any, error) {
	return w.Call(ctx, path, payload)
}

// TestM1ExecutionRuntimeRepeatedPosition 验证真实准备、扫描、原状态上报和最终释放连接完整主循环。
func TestM1ExecutionRuntimeRepeatedPosition(t *testing.T) {
	mode := &atomic.Int32{}
	worker := &runtimeWorker{}
	c, plan, claim, a, _, _ := acquireFixtureWithWorker(t, mode, worker, func(p *planmodel.Permit) {
		for i := range p.Run.Snapshot.Items {
			p.Run.Snapshot.Items[i].Actions = []string{"greeting"}
			p.Run.Snapshot.Items[i].PrioritizeReply = false
			p.Run.Items[i].Snapshot = p.Run.Snapshot.Items[i]
			p.Run.Items[i].Actions = map[string]planmodel.ActionProgress{"greeting": {State: "pending"}}
		}
	})
	held, err := c.Acquire(t.Context(), plan, claim, a)
	if err != nil {
		t.Fatal(err)
	}
	runtime := NewM1ExecutionRuntime(c, held, a, positionrunner.StartOptions{PageReadyDelay: 1, DetailOpenProbability: 0})
	loop := NewExecutionLoop(runtime, a.OwnerScope, c.now)
	result, err := loop.Run(t.Context(), held.Permit)
	if err != nil {
		for id := range runtime.items {
			_ = runtime.CloseItem(context.Background(), id)
		}
		_ = held.Reservation.Release(true)
		t.Fatal(err)
	}
	if result.Run.State != "completed" || result.Run.CurrentItem != 2 || result.Owner.State != "released" || result.Run.Items[0].TaskRunID == result.Run.Items[1].TaskRunID {
		t.Fatal("真实运行未独立推进并释放", result.Run.State)
	}
	if held.Reservation.Valid() {
		t.Fatal("最终结算仍持有本地父占用")
	}
	for _, item := range result.Run.Items {
		cp, err := c.db.LoadActionCheckpoint(t.Context(), item.ID)
		if err != nil || cp.CloudRunID != item.TaskRunID {
			t.Fatal("M1 检查点任务归属丢失", err)
		}
	}
}
