// 本文件让 HRPlus 单岗位动作继承父计划页面占用；子项交还引用不结束父占用，不调用独立岗位云端启动。
package positionrunner

import (
	"context"
	"errors"
	"goodhr5/local-agent-go/internal/planmodel"
	"strings"
	"time"
)

// PlanItemReservation 绑定当前执行项状态句柄，旧句柄不能清除后来同岗位的新执行项。
type PlanItemReservation struct {
	parent               *PlanBrowserReservation
	positionID           string
	state                *runState
	ctx                  context.Context
	released             bool
	messages             *actionSession // 同一项跨批次保留队列、计数和公平性，不复制 M1 消息动作。
	ownerID              string         // 由原云端准备许可绑定，裸借用引用不能授权页面动作。
	messageStatsRestored bool
	scan                 *planScanSession
	suspended            bool
}

// BorrowItem 只允许已持久保存的原执行项继承父占用；尚未取得 TaskRun 或正在收尾时不能附加。
func (p *PlanBrowserReservation) BorrowItem(ownerScope, itemRunID string, options StartOptions) (*PlanItemReservation, error) {
	if !planmodel.ValidID(itemRunID) || ownerScope == "" {
		return nil, errors.New("执行项或所有者缺失")
	}
	checkpoint, err := p.runner.db.LoadActionCheckpoint(p.ctx, itemRunID)
	if err != nil {
		return nil, err
	}
	if checkpoint.PlanRunID != p.lease.planRunID || checkpoint.ItemRunID != itemRunID || checkpoint.RunID != itemRunID || checkpoint.OwnerScope != ownerScope || !planmodel.ValidID(checkpoint.CloudRunID) || options.CloudRunID != checkpoint.CloudRunID || checkpoint.TaskType != strings.Join(parseTaskTypes(options.TaskType), ",") || checkpoint.PrioritizeReply != options.PrioritizeReply {
		return nil, errors.New("执行项检查点与父运行或原任务不匹配")
	}
	r := p.runner
	r.mu.Lock()
	defer r.mu.Unlock()
	canBorrow := p.lease.refs == 1 || (p.suspended != nil && p.lease.refs == 2)
	if p.released || r.browserLease != p.lease || p.ctx.Err() != nil || !canBorrow || len(r.running) != 0 {
		return nil, ErrPlanBrowserBusy
	}
	options.PlanRunID, options.ItemRunID, options.LocalRunID = checkpoint.PlanRunID, checkpoint.ItemRunID, checkpoint.RunID
	ctx, cancel := context.WithCancel(p.ctx)
	state := &runState{lease: p.lease, done: make(chan struct{}), cancel: cancel, options: options, runGreeted: checkpoint.Greeted, progress: Progress{Stage: "starting", Message: "计划执行项准备", TotalRounds: scanRounds(options), UpdatedAt: time.Now().UTC().Format(time.RFC3339Nano)}}
	p.lease.refs++
	r.running[checkpoint.PositionID] = state
	delete(r.userStopped, checkpoint.PositionID)
	return &PlanItemReservation{parent: p, positionID: checkpoint.PositionID, state: state, ctx: ctx}, nil
}

// Context 继承全局停止和登录切换的取消，取消后仍须保留引用到页面收尾完成。
func (p *PlanItemReservation) Context() context.Context { return p.ctx }

// Valid 核对当前子项身份和父预留，不能把历史句柄复用于后来同岗位。
func (p *PlanItemReservation) Valid() bool {
	r := p.parent.runner
	r.mu.Lock()
	defer r.mu.Unlock()
	return !p.released && r.browserLease == p.parent.lease && r.running[p.positionID] == p.state && p.ctx.Err() == nil
}

// CleanupReleased 只证明原子项已交还引用，取消或安全挂起不能冒充清理完成。
func (p *PlanItemReservation) CleanupReleased() bool {
	p.parent.runner.mu.Lock()
	defer p.parent.runner.mu.Unlock()
	return p.released
}

// ReleaseAfterCleanup 只交还当前子项引用，未关详情或缺少明确收尾确认时继续持有；不发送云端独立结束。
func (p *PlanItemReservation) ReleaseAfterCleanup(cleanupConfirmed bool) error {
	p.parent.stepMu.Lock()
	defer p.parent.stepMu.Unlock()
	if !cleanupConfirmed {
		return ErrPlanBrowserCleanup
	}
	r := p.parent.runner
	if p.scan != nil && !p.scan.finished {
		p.state.cancel()
		for !p.scan.finished {
			frame := <-p.scan.frames
			p.scan.finished = frame.Done
		}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if p.released {
		return nil
	}
	owned := r.running[p.positionID] == p.state || (p.suspended && p.parent.suspended == p)
	if r.browserLease != p.parent.lease || !owned || p.state.pendingDetailClose != nil {
		return ErrPlanBrowserCleanup
	}
	p.state.cancel()
	close(p.state.done)
	if p.suspended {
		p.parent.suspended = nil
		p.suspended = false
	} else {
		delete(r.running, p.positionID)
		delete(r.userStopped, p.positionID)
	}
	r.releaseBrowserLocked(p.state.lease)
	p.released = true
	return nil
}

// SuspendAtBoundary 只在已关闭详情和保存结果的暂停点让出当前角色，不取消扫描协程或释放父占用。
func (p *PlanItemReservation) SuspendAtBoundary() error {
	p.parent.stepMu.Lock()
	defer p.parent.stepMu.Unlock()
	r := p.parent.runner
	r.mu.Lock()
	defer r.mu.Unlock()
	if p.released || p.ctx.Err() != nil || r.running[p.positionID] != p.state || p.parent.suspended != nil || p.scan == nil || p.scan.finished || !p.scan.paused || p.state.pendingDetailClose != nil {
		return ErrPlanBrowserCleanup
	}
	delete(r.running, p.positionID)
	p.suspended = true
	p.parent.suspended = p
	p.scan.pageChanged = true
	return nil
}

// ResumeAtBoundary 等其他消息角色完成收尾后恢复原状态句柄，原数量、账号核对和候选人队列继续保留。
func (p *PlanItemReservation) ResumeAtBoundary() error {
	p.parent.stepMu.Lock()
	defer p.parent.stepMu.Unlock()
	r := p.parent.runner
	r.mu.Lock()
	defer r.mu.Unlock()
	if p.released || p.ctx.Err() != nil || !p.suspended || p.parent.suspended != p || r.browserLease != p.parent.lease || p.parent.lease.refs != 2 || len(r.running) != 0 {
		return ErrPlanBrowserCleanup
	}
	r.running[p.positionID] = p.state
	p.parent.suspended = nil
	p.suspended = false
	return nil
}
