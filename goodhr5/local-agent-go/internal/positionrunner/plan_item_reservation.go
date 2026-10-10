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
	if p.released || r.browserLease != p.lease || p.ctx.Err() != nil || p.lease.refs != 1 || len(r.running) != 0 {
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

// ReleaseAfterCleanup 只交还当前子项引用，未关详情或缺少明确收尾确认时继续持有；不发送云端独立结束。
func (p *PlanItemReservation) ReleaseAfterCleanup(cleanupConfirmed bool) error {
	p.parent.stepMu.Lock()
	defer p.parent.stepMu.Unlock()
	if !cleanupConfirmed {
		return ErrPlanBrowserCleanup
	}
	r := p.parent.runner
	r.mu.Lock()
	defer r.mu.Unlock()
	if p.released {
		return nil
	}
	if r.browserLease != p.parent.lease || r.running[p.positionID] != p.state || p.state.pendingDetailClose != nil {
		return ErrPlanBrowserCleanup
	}
	p.state.cancel()
	close(p.state.done)
	delete(r.running, p.positionID)
	delete(r.userStopped, p.positionID)
	r.releaseBrowserLocked(p.state.lease)
	p.released = true
	return nil
}
