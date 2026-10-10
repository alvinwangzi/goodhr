// 本文件连接 HRPlus 计划主循环与实际 M1 扫描、消息、检查点、父占用和原请求上报，不复制平台动作。
package planrunner

import (
	"context"
	"fmt"
	"github.com/google/uuid"
	"goodhr5/local-agent-go/internal/planmodel"
	"goodhr5/local-agent-go/internal/planoperations"
	"goodhr5/local-agent-go/internal/positionrunner"
	"time"
)

// M1ExecutionRuntime 保存本次父运行的已准备快照，令牌仅存在内存且不序列化。
type M1ExecutionRuntime struct {
	coordinator *Coordinator
	held        *Acquired
	authority   planoperations.Authority
	options     positionrunner.StartOptions
	items       map[string]PreparedItem
	scanning    map[string]bool
	active      string
	settling    bool
}

// NewM1ExecutionRuntime 创建真实计划适配器，后台触发仍由计划服务管理。
func NewM1ExecutionRuntime(c *Coordinator, held *Acquired, a planoperations.Authority, options positionrunner.StartOptions) *M1ExecutionRuntime {
	return &M1ExecutionRuntime{coordinator: c, held: held, authority: a, options: options, items: map[string]PreparedItem{}, scanning: map[string]bool{}}
}

// String 隐藏快照中的当前登录与 AI 配置。
func (r *M1ExecutionRuntime) String() string {
	return "HRPlus M1 计划运行适配器（运行配置隐藏）"
}

// GoString 隐藏调试输出中的运行配置。
func (r *M1ExecutionRuntime) GoString() string { return r.String() }

// check 在领取任何页面步骤之前核对原父预留和当前授权。
func (r *M1ExecutionRuntime) check() error {
	if r.settling {
		return ErrPlanNeedsSettlement
	}
	if r.coordinator == nil || r.held == nil || r.held.Reservation == nil || !r.held.Reservation.Valid() || r.authority.StillCurrent == nil || !r.authority.StillCurrent() || r.authority.OwnerScope != r.held.scope {
		return ErrPlanAuthority
	}
	return nil
}

// Boundary 仅在安全步骤之间检查原窗口；延后期间不准备下一项，到期收尾保留当日游标。
func (r *M1ExecutionRuntime) Boundary(ctx context.Context, p planmodel.Permit) (string, error) {
	if err := r.check(); err != nil {
		return "", err
	}
	if r.held.finishAt.IsZero() || r.held.window.End.IsZero() {
		return "", fmt.Errorf("原计划窗口收尾时间缺失")
	}
	for {
		now := r.coordinator.now()
		if !now.Before(r.held.finishAt) {
			if r.held.lastWindow {
				return "incomplete", nil
			}
			return "waiting_window", nil
		}
		pending := p.Run.CurrentItem < len(p.Run.Items) && p.Run.Items[p.Run.CurrentItem].State == "pending"
		if now.Before(r.held.window.End) || !pending {
			return "", nil
		}
		// 原主项已经完成但下一项尚未开始，不借随机延后开启新岗位，也不忙循环。
		delay := r.held.finishAt.Sub(now)
		if delay > 5*time.Second {
			delay = 5 * time.Second
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return "", ctx.Err()
		case <-timer.C:
		}
		if err := r.check(); err != nil {
			return "", err
		}
	}
}

// Prepare 调用原任务准备和 M1 配置链，同项在本次运行中准备过则复用。
func (r *M1ExecutionRuntime) Prepare(ctx context.Context, p planmodel.Permit) (planmodel.Permit, error) {
	if err := r.check(); err != nil {
		return p, err
	}
	if p.Run.CurrentItem >= len(p.Run.Items) {
		return p, fmt.Errorf("当前主项不存在")
	}
	id := p.Run.Items[p.Run.CurrentItem].ID
	if _, exists := r.items[id]; exists {
		return p, nil
	}
	if r.active != "" {
		return p, fmt.Errorf("上一执行项尚未收尾")
	}
	prepared, err := r.coordinator.BeginItem(ctx, r.held, p.Run, uuid.NewString(), r.authority, r.options)
	if prepared.Reservation != nil {
		r.items[id] = prepared
		r.active = id
	}
	if err != nil {
		return p, err
	}
	return prepared.Permit, nil
}

// bind 在消息收尾后恢复或附加原角色，原快照和原队列不改写为当前主扫描项。
func (r *M1ExecutionRuntime) bind(p planmodel.Permit, id string) error {
	if err := r.check(); err != nil {
		return err
	}
	item, exists := r.items[id]
	if !exists {
		options := r.options
		options.Token = r.authority.Token
		options.CloudAPIBase = r.coordinator.client.BaseURL
		options.MachineID = p.Owner.MachineID
		snapshot, err := r.coordinator.runner.RestorePlanItemSnapshot(r.held.Reservation.Context(), r.held.scope, p.Run.ID, id, options)
		if err != nil {
			return err
		}
		item = PreparedItem{Permit: p, Snapshot: snapshot}
		r.items[id] = item
	}
	if item.Reservation != nil && item.Reservation.Valid() {
		r.active = id
		return nil
	}
	if item.Reservation != nil {
		if err := item.Reservation.ResumeAtBoundary(); err == nil {
			r.active = id
			return nil
		}
	}
	child, err := r.coordinator.runner.ReattachPlanItem(r.held.Reservation, r.held.scope, p, id, item.Snapshot, item.Reservation)
	if err != nil {
		return err
	}
	item.Reservation = child
	r.items[id] = item
	r.active = id
	return nil
}

// Scan 在原角色中执行一个实际 M1 候选人安全步骤。
func (r *M1ExecutionRuntime) Scan(ctx context.Context, p planmodel.Permit) (positionrunner.PlanScanStep, error) {
	id := p.Run.Items[p.Run.CurrentItem].ID
	if err := r.bind(p, id); err != nil {
		return positionrunner.PlanScanStep{}, err
	}
	item := r.items[id]
	step, err := item.Reservation.ScanStep(p, item.Snapshot)
	if err == nil && !step.Done {
		r.scanning[id] = true
	}
	if step.Done {
		r.scanning[id] = false
	}
	message := fmt.Sprintf("找简历步骤返回：扫描=%d，打招呼=%d，跳过=%d，失败=%d，剩余=%d，工作结束=%t", step.Scanned, step.Greeted, step.Skipped, step.Failed, step.Remaining, step.Done)
	level := "info"
	if err != nil {
		level = "warning"
		message = "找简历步骤未确认，保留原进度待核对"
	}
	if logErr := r.recordItemStepLog(ctx, id, level, message); logErr != nil && err == nil {
		return step, logErr
	}
	return step, err
}

// Message 只在主扫描安全挂起后借用消息角色，批次后清理并恢复原主角色。
func (r *M1ExecutionRuntime) Message(ctx context.Context, p planmodel.Permit, c MessageChoice) (positionrunner.PlanMessageStep, error) {
	if err := r.check(); err != nil {
		return positionrunner.PlanMessageStep{}, err
	}
	old := r.active
	if old != "" && old != c.ItemRunID {
		current := r.items[old]
		var err error
		if r.scanning[old] {
			err = current.Reservation.SuspendAtBoundary()
		} else {
			err = current.Reservation.ReleaseAfterCleanup(true)
		}
		if err != nil {
			return positionrunner.PlanMessageStep{}, err
		}
		r.active = ""
	}
	if err := r.bind(p, c.ItemRunID); err != nil {
		return positionrunner.PlanMessageStep{}, err
	}
	item := r.items[c.ItemRunID]
	step, err := item.Reservation.MessageActionStep(p, item.Snapshot, c.ForceCheck, c.Action)
	if err != nil {
		_ = r.recordItemStepLog(ctx, c.ItemRunID, "warning", "消息步骤未确认，保留原进度待核对")
		return step, err
	}
	if err = r.recordItemStepLog(ctx, c.ItemRunID, "info", fmt.Sprintf("消息批次返回：当前无待处理=%t，尚有待处理=%t。统计与未知结果按原检查点核对", step.NoWork, step.Remaining)); err != nil {
		return step, err
	}
	if old != "" && old != c.ItemRunID {
		if err = item.Reservation.ReleaseAfterCleanup(true); err != nil {
			return step, err
		}
		r.active = ""
		if err = r.bind(p, old); err != nil {
			return step, err
		}
	}
	return step, nil
}

// Save 使用已加密原状态接口确认完整进度，不以本地意图代替云端结果。
func (r *M1ExecutionRuntime) Save(ctx context.Context, run planmodel.Run) (planmodel.Permit, error) {
	return r.coordinator.PersistProgress(ctx, r.held, run, uuid.NewString(), r.authority)
}

// CloseItem 等实际扫描协程退出后交还子引用，父占用继续保留。
func (r *M1ExecutionRuntime) CloseItem(ctx context.Context, id string) error {
	item, exists := r.items[id]
	if !exists {
		return fmt.Errorf("执行项收尾缺少原快照")
	}
	if err := item.Reservation.ReleaseAfterCleanup(true); err != nil {
		return err
	}
	r.scanning[id] = false
	if r.active == id {
		r.active = ""
	}
	return nil
}

// Platform 从原已准备检查点读取平台，不按当前网页或岗位名猜测。
func (r *M1ExecutionRuntime) Platform(id string) (string, error) {
	cp, err := r.coordinator.db.LoadActionCheckpoint(context.Background(), id)
	if err != nil {
		return "", err
	}
	if cp.PlanRunID != r.held.Permit.Run.ID || cp.OwnerScope != r.held.scope {
		return "", ErrPlanAuthority
	}
	return cp.Platform, nil
}

// Progress 从实际 M1 检查点获取各动作数量，未知发送独立保留。
func (r *M1ExecutionRuntime) Progress(ctx context.Context, id string) (map[string]planmodel.ActionProgress, error) {
	cp, err := r.coordinator.db.LoadActionCheckpoint(ctx, id)
	if err != nil {
		return nil, err
	}
	if cp.PlanRunID != r.held.Permit.Run.ID || cp.OwnerScope != r.held.scope {
		return nil, ErrPlanAuthority
	}
	result := map[string]planmodel.ActionProgress{}
	for _, item := range r.held.Permit.Run.Items {
		if item.ID != id {
			continue
		}
		for _, action := range item.Snapshot.Actions {
			value := planmodel.ActionProgress{}
			switch action {
			case "greeting":
				value.Count = int64(cp.Greeted)
				states, err := r.coordinator.db.ActionCandidateStates(ctx, id, cp.PositionID)
				if err != nil {
					return nil, err
				}
				for _, state := range states {
					if state == "unknown" {
						value.UnknownCount++
					}
				}
			case "auto_reply":
				value.Count = int64(cp.Replied)
				value.UnknownCount = int64(cp.ReplyStats["unknown"])
			case "re_greet":
				value.Count = int64(cp.ReGreeted)
				value.UnknownCount = int64(cp.ReGreetStats["unknown"])
			}
			result[action] = value
		}
	}
	return result, nil
}

// Finish 收尾所有子引用，先保存原释放再交还本地父占用，核对原云端释放后返回终态。
func (r *M1ExecutionRuntime) Finish(ctx context.Context, run planmodel.Run, state string) (planmodel.Permit, error) {
	for id, item := range r.items {
		if item.Reservation != nil {
			if err := r.CloseItem(ctx, id); err != nil {
				return planmodel.Permit{}, err
			}
		}
	}
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	reason := "plan_work_finished"
	if state == "waiting_window" || state == "incomplete" && !r.coordinator.now().Before(r.held.finishAt) {
		reason = "plan_window_ended"
	}
	op, err := r.coordinator.ReleaseAfterCleanup(cleanup, r.held, run, uuid.NewString(), state, reason, true)
	if err != nil {
		return planmodel.Permit{}, err
	}
	for {
		record, err := r.coordinator.db.PlanOperation(cleanup, r.held.scope, op.RequestID)
		if err != nil {
			return planmodel.Permit{}, err
		}
		if record.State == "confirmed" {
			break
		}
		sent, err := r.coordinator.requests.UploadNext(cleanup, r.coordinator.client, r.authority)
		if err != nil {
			return planmodel.Permit{}, err
		}
		if !sent {
			return planmodel.Permit{}, fmt.Errorf("原计划释放等待核对")
		}
	}
	canonical, err := r.coordinator.db.PlanRunSnapshot(cleanup, r.held.scope, run.ID)
	owner := r.held.Permit.Owner
	owner.State = "released"
	return planmodel.Permit{Run: canonical, Owner: owner}, err
}
