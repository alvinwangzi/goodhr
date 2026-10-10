// 本文件通过父计划执行权复用 HRPlus M1 消息批次，已激活动作每次最多处理一批，不等待未来消息。
package positionrunner

import (
	"errors"
	"fmt"
	"goodhr5/local-agent-go/internal/actiondispatch"
	"goodhr5/local-agent-go/internal/planmodel"
	"reflect"
	"strings"
)

// PlanMessageStep 保存原归属与当前批次结果，无工作与还有名单分开，数量继续从原检查点读取。
type PlanMessageStep struct {
	ItemRunID string `json:"item_run_id"`
	TaskRunID string `json:"task_run_id"`
	Action    string `json:"action"`
	Remaining bool   `json:"remaining"`
	NoWork    bool   `json:"no_work"`
}

// MessageStep 在当前父许可下处理一批已激活消息；未开始项、未激活动作和旧任务归属均拒绝。
func (p *PlanItemReservation) MessageStep(permit planmodel.Permit, snapshot PositionRuntimeSnapshot, forceCheck bool) (PlanMessageStep, error) {
	return p.messageStep(permit, snapshot, forceCheck, "")
}

// MessageActionStep 由父消息编排指定一个已激活的动作，其他已发现队列保留且本次不执行。
func (p *PlanItemReservation) MessageActionStep(permit planmodel.Permit, snapshot PositionRuntimeSnapshot, forceCheck bool, action string) (PlanMessageStep, error) {
	if action != "auto_reply" && action != "re_greet" {
		return PlanMessageStep{}, fmt.Errorf("计划消息动作不支持")
	}
	return p.messageStep(permit, snapshot, forceCheck, action)
}

// messageStep 复用消息安全批次；动作选择和最新策略不改写原 TaskRun、检查点或队列归属。
func (p *PlanItemReservation) messageStep(permit planmodel.Permit, snapshot PositionRuntimeSnapshot, forceCheck bool, selected string) (PlanMessageStep, error) {
	p.parent.stepMu.Lock()
	defer p.parent.stepMu.Unlock()
	result := PlanMessageStep{ItemRunID: p.state.options.ItemRunID, TaskRunID: p.state.options.CloudRunID}
	if !p.Valid() {
		return result, ErrPlanBrowserBusy
	}
	if failure := planLogFailure(p.ctx); failure != nil {
		return result, failure
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
	if p.ownerID == "" || permit.Run.ID != p.state.options.PlanRunID || permit.Run.OwnerID != p.ownerID || permit.Run.OwnerID != permit.Owner.OwnerID || permit.Owner.OwnerType != "plan" || permit.Owner.MachineID != p.state.options.MachineID || permit.Owner.State != "running" || permit.Run.State != "running" {
		return result, fmt.Errorf("父计划尚未确认运行或原占用不匹配")
	}
	active := []string{}
	allActive := []string{}
	found := false
	for _, item := range permit.Run.Items {
		if item.ID != result.ItemRunID {
			continue
		}
		found = item.TaskRunID == result.TaskRunID && item.Snapshot.PositionID == p.positionID && item.Order <= permit.Run.CurrentItem && (item.State == "running" || item.State == "completed")
		for _, action := range []string{"auto_reply", "re_greet"} {
			if item.Actions[action].State == "active" && hasTaskType(parseTaskTypes(p.state.options.TaskType), action) {
				allActive = append(allActive, action)
				if selected == "" || selected == action {
					active = append(active, action)
				}
			}
		}
	}
	if !found || len(active) == 0 {
		return result, fmt.Errorf("当前执行项没有已激活的消息动作")
	}
	checkpoint, err := p.parent.runner.db.LoadActionCheckpoint(p.ctx, p.state.options.LocalRunID)
	if err != nil {
		return result, err
	}
	if checkpoint.PlanRunID != p.state.options.PlanRunID || checkpoint.ItemRunID != result.ItemRunID || checkpoint.CloudRunID != result.TaskRunID {
		return result, fmt.Errorf("消息检查点原归属已变化")
	}
	if snapshot.Position.ID != p.positionID || !reflect.DeepEqual(snapshot.Position.PositionSnapshot, checkpoint.PositionSnapshot) {
		return result, fmt.Errorf("消息动作岗位快照与原检查点不匹配")
	}
	options := p.state.options
	options.TaskType = strings.Join(active, ",")
	options.PrioritizeReply = options.PrioritizeReply && hasTaskType(active, "auto_reply")
	if hasTaskType(active, "auto_reply") {
		for _, candidate := range permit.Run.Items {
			if candidate.Order <= permit.Run.CurrentItem && candidate.Snapshot.PositionID == p.positionID && candidate.Actions["auto_reply"].State == "active" && (candidate.State == "running" || candidate.State == "completed") {
				policy := checkpoint
				if candidate.ID != checkpoint.ItemRunID {
					policy, err = p.parent.runner.db.LoadActionCheckpoint(p.ctx, candidate.ID)
					if err != nil {
						return result, err
					}
				}
				if policy.PlanRunID != checkpoint.PlanRunID || policy.OwnerScope != checkpoint.OwnerScope || policy.CloudRunID != candidate.TaskRunID {
					return result, fmt.Errorf("最新消息设置不属于原计划或任务")
				}
				if policy.Platform == checkpoint.Platform {
					options.PrioritizeReply = candidate.Snapshot.PrioritizeReply
				}
			}
		}
	}
	if p.messages == nil {
		if p.scan != nil && !p.scan.finished && p.scan.resumer == nil {
			return result, fmt.Errorf("当前平台不支持扫描间隙的推荐进度恢复")
		}
		p.messages, err = p.parent.runner.newActionSession(p.ctx, snapshot.Position, options, snapshot)
		if err != nil {
			return result, err
		}
		if p.scan != nil && !p.scan.finished {
			p.messages.cursor = p.scan.cursor
			p.messages.resumer = p.scan.resumer
		}
	}
	if !p.messageStatsRestored {
		if err = p.messages.restoreMessageState(checkpoint); err != nil {
			return result, err
		}
		// 复用原计数；重新附加或跨窗口恢复不能把上一批累计值写回零。
		p.messages.replyStats.Checked = checkpoint.ReplyStats["checked"]
		p.messages.replyStats.Replied = checkpoint.Replied
		p.messages.replyStats.AcceptedResume = checkpoint.ReplyStats["accepted_resume"]
		p.messages.replyStats.Skipped = checkpoint.ReplyStats["skipped"]
		p.messages.replyStats.Failed = checkpoint.ReplyStats["failed"]
		p.messages.replyStats.Unknown = checkpoint.ReplyStats["unknown"]
		p.messages.reGreetStats.total = checkpoint.ReGreetStats["total"]
		p.messages.reGreetStats.sent = checkpoint.ReGreeted
		p.messages.reGreetStats.skipped = checkpoint.ReGreetStats["skipped"]
		p.messages.reGreetStats.failed = checkpoint.ReGreetStats["failed"]
		p.messages.reGreetStats.unknown = checkpoint.ReGreetStats["unknown"]
		p.messageStatsRestored = true
	}
	p.messages.options = options
	if p.messages.flow != nil {
		if original, ok := p.messages.flow.exec.(platformExecutor); ok {
			original.logContext = p.ctx
			p.messages.flow.exec = original
		}
	}
	p.messages.scheduler.PrioritizeReply = options.PrioritizeReply
	if !hasTaskType(allActive, "auto_reply") {
		p.messages.replies = nil
	}
	if !hasTaskType(allActive, "re_greet") {
		p.messages.reGreets = nil
	}
	_, err = p.messages.serviceBounded(p.ctx, false, forceCheck, true)
	result.Action = string(p.messages.lastBatch)
	result.Remaining = (hasTaskType(active, "auto_reply") && len(p.messages.replies) > 0) || (hasTaskType(active, "re_greet") && len(p.messages.reGreets) > 0) || len(p.messages.infoQueue) > 0
	result.NoWork = p.messages.lastBatch == actiondispatch.Done && !result.Remaining
	p.parent.runner.mu.Lock()
	childError := p.state.planActionError
	p.parent.runner.mu.Unlock()
	return result, errors.Join(err, childError, planLogFailure(p.ctx))
}
