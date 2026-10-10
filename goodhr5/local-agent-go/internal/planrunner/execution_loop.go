// 本文件编排 HRPlus 父计划的主项推进和活跃消息，步骤由 M1 运行适配器执行，确认进度后才继续。
package planrunner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"goodhr5/local-agent-go/internal/planmodel"
	"goodhr5/local-agent-go/internal/positionrunner"
	"time"
)

// SafeItemFailure 表示本步骤失败但平台已经确认页面安全，只有实际适配器可以给出该结论。
type SafeItemFailure struct{ Cause error }

// Error 返回步骤失败原因，不能把安全收尾当作步骤成功。
func (e SafeItemFailure) Error() string {
	if e.Cause == nil {
		return "执行项失败，页面已安全收尾"
	}
	return e.Cause.Error()
}

// Unwrap 保留原错误供父结算区分，不改变成功数量。
func (e SafeItemFailure) Unwrap() error { return e.Cause }

// ExecutionRuntime 是父计划实际运行入口，页面与持久上报分别复用既有 M1 和原请求实现。
type ExecutionRuntime interface {
	Boundary(context.Context, planmodel.Permit) (string, error)
	Prepare(context.Context, planmodel.Permit) (planmodel.Permit, error)
	Scan(context.Context, planmodel.Permit) (positionrunner.PlanScanStep, error)
	Message(context.Context, planmodel.Permit, MessageChoice) (positionrunner.PlanMessageStep, error)
	Save(context.Context, planmodel.Run) (planmodel.Permit, error)
	CloseItem(context.Context, string) error
	Platform(string) (string, error)
	Progress(context.Context, string) (map[string]planmodel.ActionProgress, error)
	Finish(context.Context, planmodel.Run, string) (planmodel.Permit, error)
}

// ExecutionLoop 按原岗位顺序推进，不创建独立账号占用或等待未来消息。
type ExecutionLoop struct {
	runtime            ExecutionRuntime
	services           *MessageServices
	now                func() time.Time
	messageStateLoaded bool
}

// NewExecutionLoop 连接真实步骤适配器和当前账号活跃服务，时钟可替换用于验收。
func NewExecutionLoop(runtime ExecutionRuntime, ownerScope string, now func() time.Time) *ExecutionLoop {
	if now == nil {
		now = time.Now
	}
	return &ExecutionLoop{runtime: runtime, services: NewMessageServices(ownerScope), now: now}
}

// cloneLoopRun 在独立快照上修改事实，不在云端确认前改写调用方或原回执。
func cloneLoopRun(run planmodel.Run) (planmodel.Run, error) {
	raw, err := json.Marshal(run)
	if err != nil {
		return planmodel.Run{}, err
	}
	var copy planmodel.Run
	err = json.Unmarshal(raw, &copy)
	return copy, err
}

// save 调用原状态上报并要求完整原运行、序号和游标确认，失败立即交回收尾流程。
func (l *ExecutionLoop) save(ctx context.Context, current planmodel.Permit, next planmodel.Run) (planmodel.Permit, error) {
	next.Sequence = current.Run.Sequence + 1
	next.State = "running"
	var result planmodel.Permit
	var err error
	atomic, canAtomic := l.runtime.(AtomicMessageStateRuntime)
	if canAtomic && l.messageStateLoaded {
		if err = l.services.Sync(next, l.runtime.Platform); err != nil {
			return current, err
		}
		raw, encodeErr := l.services.Snapshot()
		if encodeErr != nil {
			return current, encodeErr
		}
		result, err = atomic.SaveMessageProgress(ctx, next, raw)
	} else {
		result, err = l.runtime.Save(ctx, next)
	}
	if err != nil {
		return current, err
	}
	if result.Run.ID != next.ID || result.Run.OwnerID != next.OwnerID || result.Run.Sequence != next.Sequence || result.Run.CurrentItem != next.CurrentItem || result.Run.State != "running" {
		return current, fmt.Errorf("计划步骤没有得到原运行进度确认")
	}
	if canAtomic && l.messageStateLoaded {
		if err := l.services.ConfirmProgress(result.Run, l.runtime.Platform); err != nil {
			return result, err
		}
	}
	if durable, ok := l.runtime.(MessageStateRuntime); ok && l.messageStateLoaded && !canAtomic {
		if err := l.services.Sync(result.Run, l.runtime.Platform); err != nil {
			return result, err
		}
		raw, err := l.services.Snapshot()
		if err != nil {
			return result, err
		}
		if err = durable.SaveMessageState(ctx, result.Run, raw); err != nil {
			return result, err
		}
	}
	return result, nil
}

// Run 每个安全步骤后先确认进度，消息主项无当前工作后推进，最终再核对已激活消息。
func (l *ExecutionLoop) Run(ctx context.Context, permit planmodel.Permit) (planmodel.Permit, error) {
	if l.runtime == nil {
		return permit, fmt.Errorf("计划运行适配器未连接")
	}
	if err := permit.Run.Validate(); err != nil {
		return permit, err
	}
	for {
		if err := ctx.Err(); err != nil {
			return permit, err
		}
		state, err := l.runtime.Boundary(ctx, permit)
		if err != nil {
			return permit, err
		}
		if state != "" {
			if state != "waiting_window" && state != "incomplete" {
				return permit, fmt.Errorf("计划窗口返回未知收尾状态")
			}
			return l.runtime.Finish(ctx, permit.Run, state)
		}
		if permit.Run.CurrentItem < len(permit.Run.Items) {
			item := permit.Run.Items[permit.Run.CurrentItem]
			if item.State == "running" {
				prepared, err := l.runtime.Prepare(ctx, permit)
				if err != nil {
					return permit, err
				}
				permit = prepared
				if permit.Run.State == "starting" {
					next, err := cloneLoopRun(permit.Run)
					if err != nil {
						return permit, err
					}
					permit, err = l.save(ctx, permit, next)
					if err != nil {
						return permit, err
					}
				}
			}
			if item.State == "pending" {
				prepared, err := l.runtime.Prepare(ctx, permit)
				if err != nil {
					return permit, err
				}
				if prepared.Run.ID != permit.Run.ID || prepared.Run.OwnerID != permit.Run.OwnerID || prepared.Run.CurrentItem != permit.Run.CurrentItem || prepared.Run.Items[prepared.Run.CurrentItem].TaskRunID == "" {
					return permit, fmt.Errorf("执行项准备身份不匹配")
				}
				permit = prepared
				next, err := cloneLoopRun(permit.Run)
				if err != nil {
					return permit, err
				}
				next.Items[next.CurrentItem].State = "running"
				for action, progress := range next.Items[next.CurrentItem].Actions {
					progress.State = "active"
					next.Items[next.CurrentItem].Actions[action] = progress
				}
				permit, err = l.save(ctx, permit, next)
				if err != nil {
					return permit, err
				}
			}
		}
		if err := l.services.Sync(permit.Run, l.runtime.Platform); err != nil {
			return permit, err
		}
		if !l.messageStateLoaded {
			if durable, ok := l.runtime.(MessageStateRuntime); ok {
				raw, err := durable.LoadMessageState(ctx, permit.Run)
				if err != nil {
					return permit, err
				}
				if len(raw) > 0 {
					if err = l.services.Restore(raw, permit.Run, l.runtime.Platform); err != nil {
						return permit, err
					}
				}
			}
			l.messageStateLoaded = true
		}
		finalPass := permit.Run.CurrentItem == len(permit.Run.Items)
		greeting, priority := false, false
		if !finalPass {
			item := permit.Run.Items[permit.Run.CurrentItem]
			greeting = item.Actions["greeting"].State == "active"
			priority = item.Snapshot.PrioritizeReply
		}
		choice := l.services.Next(l.now(), greeting, priority, finalPass)
		next, err := cloneLoopRun(permit.Run)
		if err != nil {
			return permit, err
		}
		mainDone := false
		switch choice.Kind {
		case "scan":
			step, err := l.runtime.Scan(ctx, permit)
			if err != nil {
				var safe SafeItemFailure
				if !errors.As(err, &safe) {
					return permit, err
				}
				if err = l.runtime.CloseItem(ctx, next.Items[next.CurrentItem].ID); err != nil {
					return permit, err
				}
				progress, err := l.runtime.Progress(ctx, next.Items[next.CurrentItem].ID)
				if err != nil {
					return permit, err
				}
				for action, actual := range progress {
					old, exists := next.Items[next.CurrentItem].Actions[action]
					if !exists || actual.Count < old.Count || actual.UnknownCount < old.UnknownCount {
						return permit, fmt.Errorf("失败步骤原数量不匹配")
					}
					actual.State = old.State
					next.Items[next.CurrentItem].Actions[action] = actual
				}
				next.Items[next.CurrentItem].State = "failed"
				for action, progress := range next.Items[next.CurrentItem].Actions {
					if progress.State == "active" {
						progress.State = "stopped"
						next.Items[next.CurrentItem].Actions[action] = progress
					}
				}
				next.CurrentItem++
				permit, err = l.save(ctx, permit, next)
				if err != nil {
					return permit, err
				}
				continue
			}
			item := next.Items[next.CurrentItem]
			if step.ItemRunID != item.ID || step.TaskRunID != item.TaskRunID {
				return permit, fmt.Errorf("扫描结果归属不匹配")
			}
			progress := item.Actions["greeting"]
			if int64(step.Greeted) < progress.Count {
				return permit, fmt.Errorf("扫描成功数量倒退")
			}
			progress.Count = int64(step.Greeted)
			if step.Done {
				progress.State = "completed"
				mainDone = true
			}
			next.Items[next.CurrentItem].Actions["greeting"] = progress
			l.services.Scanned()
		case "message":
			step, err := l.runtime.Message(ctx, permit, choice)
			if err != nil {
				return permit, err
			}
			if step.ItemRunID != choice.ItemRunID || step.TaskRunID != choice.TaskRunID {
				return permit, fmt.Errorf("消息结果原归属不匹配")
			}
			progress, err := l.runtime.Progress(ctx, choice.ItemRunID)
			if err != nil {
				return permit, err
			}
			for index, item := range next.Items {
				if item.ID != choice.ItemRunID {
					continue
				}
				for action, actual := range progress {
					old, exists := item.Actions[action]
					if !exists || actual.Count < old.Count || actual.UnknownCount < old.UnknownCount {
						return permit, fmt.Errorf("消息数量或动作归属不匹配")
					}
					actual.State = old.State
					next.Items[index].Actions[action] = actual
				}
			}
			if err := l.services.Observed(choice, step.Remaining, l.now(), finalPass); err != nil {
				return permit, err
			}
			if !finalPass && !greeting && choice.ItemRunID == next.Items[next.CurrentItem].ID && !step.Remaining {
				mainDone = l.messageMainChecked(next.Items[next.CurrentItem])
			}
		case "done":
			if finalPass {
				state := "completed"
				for index, item := range next.Items {
					if item.State == "failed" || item.State == "stopped" {
						state = "incomplete"
					}
					for action, progress := range item.Actions {
						if progress.UnknownCount > 0 || progress.State == "stopped" {
							state = "incomplete"
						}
						if progress.State == "active" {
							progress.State = "completed"
							next.Items[index].Actions[action] = progress
						}
					}
				}
				return l.runtime.Finish(ctx, next, state)
			}
			mainDone = !greeting
		default:
			return permit, fmt.Errorf("计划动作选择不支持")
		}
		if mainDone {
			itemID := next.Items[next.CurrentItem].ID
			if err := l.runtime.CloseItem(ctx, itemID); err != nil {
				return permit, err
			}
			next.Items[next.CurrentItem].State = "completed"
			next.CurrentItem++
		}
		permit, err = l.save(ctx, permit, next)
		if err != nil {
			return permit, err
		}
	}
}

// messageMainChecked 要求本主项的每个已选消息动作都至少检查过当前工作，再结束主项而保留服务。
func (l *ExecutionLoop) messageMainChecked(item planmodel.ItemRun) bool {
	for _, entry := range l.services.services {
		source, exists := entry.sources[item.ID]
		if exists && (source.lastServed.IsZero() || source.pending) {
			return false
		}
	}
	return true
}
