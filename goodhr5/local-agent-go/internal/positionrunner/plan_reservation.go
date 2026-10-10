// 本文件为 HRPlus 父计划预留同一浏览器执行权，取消不代替页面收尾确认，也不操作招聘页面。
package positionrunner

import (
	"context"
	"errors"
	"goodhr5/local-agent-go/internal/planmodel"
	"sync"
)

var ErrPlanBrowserBusy = errors.New("本地有任务正在执行或收尾，请等待")
var ErrPlanBrowserCleanup = errors.New("计划尚未完成页面收尾，不能释放执行权")

// PlanBrowserReservation 是父计划的进程内预留证明，不能序列化为重启后的启动许可。
type PlanBrowserReservation struct {
	stepMu   sync.Mutex // 父计划动作和收尾串行，不能在消息批次仍执行时交还子引用。
	runner   *Runner
	lease    *browserLease
	ctx      context.Context
	released bool
}

// ReservePlanBrowser 与手动启动及已有收尾任务共用同一锁，预留成功前不能申请云端开始。
func (r *Runner) ReservePlanBrowser(parent context.Context, runID string) (*PlanBrowserReservation, error) {
	if !planmodel.ValidID(runID) {
		return nil, errors.New("计划运行编号不正确")
	}
	if err := parent.Err(); err != nil {
		return nil, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.running) > 0 || r.browserLease != nil {
		return nil, ErrPlanBrowserBusy
	}
	ctx, cancel := context.WithCancel(parent)
	if err := ctx.Err(); err != nil {
		cancel()
		return nil, err
	}
	lease := &browserLease{refs: 1, planRunID: runID, planCancel: cancel}
	r.browserLease = lease
	return &PlanBrowserReservation{runner: r, lease: lease, ctx: ctx}, nil
}

// Context 将退出、登录切换及父计划取消传递给流程，但取消时仍须保留执行权到收尾结束。
func (p *PlanBrowserReservation) Context() context.Context { return p.ctx }

// Valid 只确认本进程的当前预留，不代替云端许可、时间检查或招聘账号证明。
func (p *PlanBrowserReservation) Valid() bool {
	p.runner.mu.Lock()
	defer p.runner.mu.Unlock()
	return !p.released && p.runner.browserLease == p.lease && p.ctx.Err() == nil
}

// Release 只有明确完成页面清理才能释放；迟到旧句柄或重复释放不会清除新的预留。
func (p *PlanBrowserReservation) Release(cleanupConfirmed bool) error {
	p.stepMu.Lock()
	defer p.stepMu.Unlock()
	if !cleanupConfirmed {
		return ErrPlanBrowserCleanup
	}
	p.runner.mu.Lock()
	if p.released {
		p.runner.mu.Unlock()
		return nil
	}
	if p.runner.browserLease != p.lease || p.lease.refs != 1 {
		p.runner.mu.Unlock()
		return ErrPlanBrowserCleanup
	}
	p.lease.planCancel()
	p.runner.releaseBrowserLocked(p.lease)
	p.released = true
	p.runner.mu.Unlock()
	p.runner.releasePowerProtectionIfIdle()
	return nil
}
