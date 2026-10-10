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
	p.parent.stepMu.Lock()
	defer p.parent.stepMu.Unlock()
	result := PlanMessageStep{ItemRunID: p.state.options.ItemRunID, TaskRunID: p.state.options.CloudRunID}
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
	if p.ownerID == "" || permit.Run.ID != p.state.options.PlanRunID || permit.Run.OwnerID != p.ownerID || permit.Run.OwnerID != permit.Owner.OwnerID || permit.Owner.OwnerType != "plan" || permit.Owner.MachineID != p.state.options.MachineID || permit.Owner.State != "running" || permit.Run.State != "running" {
		return result, fmt.Errorf("父计划尚未确认运行或原占用不匹配")
	}
	active := []string{}
	found := false
	for _, item := range permit.Run.Items {
		if item.ID != result.ItemRunID {
			continue
		}
		found = item.TaskRunID == result.TaskRunID && item.Snapshot.PositionID == p.positionID && item.Order <= permit.Run.CurrentItem && (item.State == "running" || item.State == "completed")
		for _, action := range []string{"auto_reply", "re_greet"} {
			if item.Actions[action].State == "active" && hasTaskType(parseTaskTypes(p.state.options.TaskType), action) {
				active = append(active, action)
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
	if snapshot.Position.ID != p.positionID || !reflect.DeepEqual(snapshot.Position.PositionSnapshot, checkpoint.PositionSnapshot) {
		return result, fmt.Errorf("消息动作岗位快照与原检查点不匹配")
	}
	options := p.state.options
	options.TaskType = strings.Join(active, ",")
	options.PrioritizeReply = options.PrioritizeReply && hasTaskType(active, "auto_reply")
	if p.messages == nil {
		p.messages, err = p.parent.runner.newActionSession(p.ctx, snapshot.Position, options, snapshot)
		if err != nil {
			return result, err
		}
	}
	if !p.messageStatsRestored {
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
	p.messages.scheduler.PrioritizeReply = options.PrioritizeReply
	if !hasTaskType(active, "auto_reply") {
		p.messages.replies = nil
	}
	if !hasTaskType(active, "re_greet") {
		p.messages.reGreets = nil
	}
	_, err = p.messages.serviceBounded(p.ctx, false, forceCheck, true)
	result.Action = string(p.messages.lastBatch)
	result.Remaining = len(p.messages.replies) > 0 || len(p.messages.reGreets) > 0 || len(p.messages.infoQueue) > 0
	result.NoWork = p.messages.lastBatch == actiondispatch.Done && !result.Remaining
	p.parent.runner.mu.Lock()
	childError := p.state.planActionError
	p.parent.runner.mu.Unlock()
	return result, errors.Join(err, childError)
}
