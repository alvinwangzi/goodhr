// 本文件把 HRPlus M1 扫描挂到候选人安全边界，保留活队列和原检查点，由父调度决定何时继续。
package positionrunner

import (
	"context"
	"errors"
	"fmt"
	"goodhr5/local-agent-go/internal/planmodel"
	"goodhr5/local-agent-go/internal/platformcore"
	"goodhr5/local-agent-go/internal/platforms"
	"reflect"
)

// PlanScanStep 返回已持久化的本项统计，Done 表示扫描退出，不代表父计划已释放或整体成功。
type PlanScanStep struct {
	ItemRunID string `json:"item_run_id"`
	TaskRunID string `json:"task_run_id"`
	Done      bool   `json:"done"`
	Scanned   int    `json:"scanned"`
	Greeted   int    `json:"greeted"`
	Skipped   int    `json:"skipped"`
	Failed    int    `json:"failed"`
	Remaining int    `json:"remaining"`
	err       error
}

// planScanSession 的扫描协程只在父锁控制下运行，安全边界等待 advance，不自行执行另一岗位。
type planScanSession struct {
	frames      chan PlanScanStep
	advance     chan bool
	finished    bool
	last        PlanScanStep
	cursor      platformcore.RecommendationCursor
	resumer     platformcore.RecommendationResumer
	runtime     platformcore.Runtime
	position    PositionRuntimeSnapshot
	paused      bool // 由安全边界发布并通过通道交接，不在暂停时执行页面动作。
	pageChanged bool
}

// ScanStep 复用 M1 扫描，每到安全边界暂停；重新调用保留内存队列，消息返回的锚点变化才重扫。
func (p *PlanItemReservation) ScanStep(permit planmodel.Permit, snapshot PositionRuntimeSnapshot) (PlanScanStep, error) {
	p.parent.stepMu.Lock()
	defer p.parent.stepMu.Unlock()
	result := PlanScanStep{ItemRunID: p.state.options.ItemRunID, TaskRunID: p.state.options.CloudRunID}
	if !p.Valid() {
		return result, ErrPlanBrowserBusy
	}
	p.parent.runner.mu.Lock()
	previousError := p.state.planActionError
	p.parent.runner.mu.Unlock()
	if previousError != nil {
		return result, previousError
	}
	if err := permit.Run.Validate(); err != nil {
		return result, err
	}
	if p.ownerID == "" || permit.Run.ID != p.state.options.PlanRunID || permit.Owner.OwnerID != p.ownerID || permit.Run.OwnerID != p.ownerID || permit.Owner.MachineID != p.state.options.MachineID || permit.Owner.OwnerType != "plan" || permit.Owner.State != "running" || permit.Run.State != "running" || permit.Run.CurrentItem >= len(permit.Run.Items) {
		return result, fmt.Errorf("父计划尚未确认当前扫描占用")
	}
	item := permit.Run.Items[permit.Run.CurrentItem]
	if item.ID != result.ItemRunID || item.TaskRunID != result.TaskRunID || item.State != "running" || item.Actions["greeting"].State != "active" || !hasTaskType(parseTaskTypes(p.state.options.TaskType), "greeting") {
		return result, fmt.Errorf("当前主项未激活找简历动作")
	}
	checkpoint, err := p.parent.runner.db.LoadActionCheckpoint(p.ctx, p.state.options.LocalRunID)
	if err != nil {
		return result, err
	}
	if checkpoint.PlanRunID != p.state.options.PlanRunID || checkpoint.ItemRunID != result.ItemRunID || checkpoint.CloudRunID != result.TaskRunID {
		return result, fmt.Errorf("扫描检查点原归属已变化")
	}
	if snapshot.Position.ID != p.positionID || !reflect.DeepEqual(snapshot.Position.PositionSnapshot, checkpoint.PositionSnapshot) {
		return result, fmt.Errorf("扫描岗位快照与原检查点不匹配")
	}
	if p.scan == nil {
		if reachedRunGreetLimit(snapshot.Position, checkpoint.Greeted) {
			return p.planScanFrame(true, nil), nil
		}
		runtime, err := platforms.RuntimeFor(snapshot.Position.PlatformID)
		if err != nil {
			return result, err
		}
		p.scan = &planScanSession{frames: make(chan PlanScanStep, 1), advance: make(chan bool), runtime: runtime, position: snapshot}
		p.scan.resumer, _ = runtime.(platformcore.RecommendationResumer)
		go p.runPlanScan()
	} else if p.scan.finished {
		return p.scan.last, p.scan.last.err
	} else {
		rescan := false
		if p.scan.pageChanged {
			if p.scan.resumer == nil {
				return result, fmt.Errorf("当前平台不支持跨岗位恢复推荐进度")
			}
			matched, err := restoreRecommendationCursor(p.ctx, p.parent.runner, snapshot.Position, snapshot.PlatformConfig, p.scan.runtime, p.scan.resumer, platformExecutor{runner: p.parent.runner, positionID: p.positionID}, p.scan.cursor)
			if err != nil {
				return result, err
			}
			rescan = !matched
			p.scan.pageChanged = false
			if p.messages != nil {
				p.messages.onRecommendation = true
			}
		} else if p.messages != nil {
			if err = p.messages.returnRecommendation(p.ctx); err != nil {
				return result, err
			}
			rescan = p.messages.needsRescan
			p.messages.needsRescan = false
		}
		select {
		case p.scan.advance <- rescan:
		case <-p.ctx.Done():
			// 不提前放开父锁，等待扫描协程完成必要收尾后返回。
		}
	}
	frame := <-p.scan.frames
	p.scan.last, p.scan.finished = frame, frame.Done
	return frame, errors.Join(frame.err, p.ctx.Err())
}

// planScanFrame 只读取已保存的计数和剩余队列，不把未来候选人或未知发送计入成功。
func (p *PlanItemReservation) planScanFrame(done bool, failure error) PlanScanStep {
	result := PlanScanStep{ItemRunID: p.state.options.ItemRunID, TaskRunID: p.state.options.CloudRunID, Done: done, err: failure}
	checkpoint, err := p.parent.runner.db.LoadActionCheckpoint(context.WithoutCancel(p.ctx), p.state.options.LocalRunID)
	if err != nil {
		result.err = errors.Join(failure, err)
		return result
	}
	result.Scanned, result.Greeted, result.Skipped, result.Failed, result.Remaining = checkpoint.Scanned, checkpoint.Greeted, checkpoint.Skipped, checkpoint.Failed, len(checkpoint.Queue)
	return result
}

// runPlanScan 运行原 M1 扫描并在清理、结果保存后的边界交接，取消不会把活协程遗留给下一岗位。
func (p *PlanItemReservation) runPlanScan() {
	s := p.scan
	var failure error
	defer func() {
		if recover() != nil {
			failure = fmt.Errorf("计划扫描意外中断，需要核对原检查点")
		}
		if !s.paused {
			closeErr := p.parent.runner.closeCandidateDetailNow(context.WithoutCancel(p.ctx), p.positionID, "当前候选人", "计划扫描收尾关闭详情", nil)
			failure = errors.Join(failure, closeErr)
		}
		s.frames <- p.planScanFrame(true, failure)
	}()
	options := p.state.options
	pendingRescan := false
	options.requestScanRescan = func() { pendingRescan = true }
	options.acknowledgeRescan = func() { pendingRescan = false }
	options.scanBoundary = func(ctx context.Context) (bool, error) {
		frame := p.planScanFrame(false, nil)
		if frame.err != nil {
			return false, frame.err
		}
		if s.resumer != nil {
			checkpoint, err := p.parent.runner.db.LoadActionCheckpoint(ctx, options.LocalRunID)
			if err != nil {
				return false, err
			}
			cursor, err := s.resumer.CaptureRecommendationCursor(ctx, platformExecutor{runner: p.parent.runner, positionID: p.positionID}, checkpoint.Anchors)
			if err != nil {
				cursor = platformcore.RecommendationCursor{Reason: "anchor_capture_failed"}
			}
			s.cursor = cursor
		}
		s.paused = true
		s.frames <- frame
		select {
		case rescan := <-s.advance:
			s.paused = false
			return rescan || pendingRescan, nil
		case <-ctx.Done():
			return false, ctx.Err()
		}
	}
	_, failure = p.parent.runner.scanOnce(p.ctx, s.position.Position, s.position.PlatformConfig, options)
}
