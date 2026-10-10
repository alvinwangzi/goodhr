// 本文件用实际 M1、Windows DPAPI 和受控 Worker 联调 HRPlus 重复岗位主循环，不操作真实招聘页面。
package planrunner

import (
	"context"
	"goodhr5/local-agent-go/internal/browser"
	"goodhr5/local-agent-go/internal/planmodel"
	"goodhr5/local-agent-go/internal/positionrunner"
	"sync/atomic"
	"testing"
)

// runtimeWorker 用标准 Worker 协议提供受控页面事实，统计发送由真实 M1 和检查点实现。
type runtimeWorker struct{ starts, greets int }

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
		data = map[string]any{"greeted": true}
	case "/api/v1/boss/candidates/capture-anchors":
		data = map[string]any{"valid": true, "signature": "fixture-role"}
	}
	return map[string]any{"data": data}, nil
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
