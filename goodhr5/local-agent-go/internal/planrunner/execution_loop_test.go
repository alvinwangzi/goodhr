// 本文件验证 HRPlus 主循环的岗位顺序、未来动作保护、原归属及上报失败后不推进，不使用招聘页面。
package planrunner

import (
	"context"
	"errors"
	"fmt"
	"goodhr5/local-agent-go/internal/planmodel"
	"goodhr5/local-agent-go/internal/positionrunner"
	"testing"
)

// loopRuntimeFixture 记录真实循环调用边界，替换页面和云端实现以核对动作顺序。
type loopRuntimeFixture struct {
	events          []string
	failSave        bool
	counts          map[string]map[string]planmodel.ActionProgress
	safeScanFailure bool
}

// Boundary 为不涉及时间窗口的流程夹具保留执行资格。
func (f *loopRuntimeFixture) Boundary(context.Context, planmodel.Permit) (string, error) {
	return "", nil
}

// Prepare 为原项产生固定任务记录，已有记录保持原编号。
func (f *loopRuntimeFixture) Prepare(_ context.Context, p planmodel.Permit) (planmodel.Permit, error) {
	if p.Run.Items[p.Run.CurrentItem].TaskRunID != "" {
		return p, nil
	}
	p.Run, _ = cloneLoopRun(p.Run)
	p.Run.Sequence++
	p.Run.Items[p.Run.CurrentItem].TaskRunID = fmt.Sprintf("70000000-0000-0000-0000-%012d", p.Run.CurrentItem+1)
	f.events = append(f.events, fmt.Sprintf("prepare:%d", p.Run.CurrentItem))
	return p, nil
}

// Scan 记录一个安全扫描单元，并验证后续消息仍未激活。
func (f *loopRuntimeFixture) Scan(_ context.Context, p planmodel.Permit) (positionrunner.PlanScanStep, error) {
	item := p.Run.Items[p.Run.CurrentItem]
	f.events = append(f.events, fmt.Sprintf("scan:%d", p.Run.CurrentItem))
	if f.safeScanFailure {
		return positionrunner.PlanScanStep{}, SafeItemFailure{Cause: errors.New("fixture scan failed")}
	}
	for index, other := range p.Run.Items {
		if index > p.Run.CurrentItem {
			for _, a := range other.Actions {
				if a.State != "pending" {
					return positionrunner.PlanScanStep{}, fmt.Errorf("未来项提前激活")
				}
			}
		}
	}
	return positionrunner.PlanScanStep{ItemRunID: item.ID, TaskRunID: item.TaskRunID, Done: true, Greeted: 1}, nil
}

// Message 返回当前无消息，主循环须继续岗位而非等待未来消息。
func (f *loopRuntimeFixture) Message(_ context.Context, p planmodel.Permit, c MessageChoice) (positionrunner.PlanMessageStep, error) {
	f.events = append(f.events, "message:"+c.ItemRunID)
	return positionrunner.PlanMessageStep{ItemRunID: c.ItemRunID, TaskRunID: c.TaskRunID, Action: "done", NoWork: true}, nil
}

// Save 只确认当前原序号，故障场景拒绝步骤确认。
func (f *loopRuntimeFixture) Save(_ context.Context, r planmodel.Run) (planmodel.Permit, error) {
	if f.failSave {
		return planmodel.Permit{}, fmt.Errorf("fixture save failed")
	}
	f.events = append(f.events, fmt.Sprintf("save:%d", r.CurrentItem))
	return planmodel.Permit{Run: r, Owner: planmodel.Owner{OwnerID: r.OwnerID, OwnerType: "plan", MachineID: "machine-A", State: "running"}}, nil
}

// CloseItem 标记实际子项收尾边界。
func (f *loopRuntimeFixture) CloseItem(_ context.Context, id string) error {
	f.events = append(f.events, "close:"+id)
	return nil
}

// Platform 提供已准备项的平台。
func (f *loopRuntimeFixture) Platform(string) (string, error) { return "boss", nil }

// Progress 返回无发送的真实零数量，仍保持各动作原状态。
func (f *loopRuntimeFixture) Progress(_ context.Context, id string) (map[string]planmodel.ActionProgress, error) {
	return f.counts[id], nil
}

// Finish 记录父结算，保留全部原项的独立任务关联。
func (f *loopRuntimeFixture) Finish(_ context.Context, r planmodel.Run, state string) (planmodel.Permit, error) {
	f.events = append(f.events, "finish:"+state)
	r.State = state
	return planmodel.Permit{Run: r, Owner: planmodel.Owner{OwnerID: r.OwnerID, State: "released", OwnerType: "plan", MachineID: "machine-A"}}, nil
}

// TestExecutionLoopGreetingBeforeReply 验证先找简历后回复的重复岗位编排，最终无消息即结束。
func TestExecutionLoopGreetingBeforeReply(t *testing.T) {
	r := serviceRunFixture(t)
	r.Items[0].State = "pending"
	r.Items[0].TaskRunID = ""
	r.Snapshot.Items[0].Actions = []string{"greeting"}
	r.Snapshot.Items[0].PrioritizeReply = false
	r.Items[0].Snapshot = r.Snapshot.Items[0]
	r.Items[0].Actions = map[string]planmodel.ActionProgress{"greeting": {State: "pending"}}
	f := &loopRuntimeFixture{counts: map[string]map[string]planmodel.ActionProgress{r.Items[1].ID: {"auto_reply": {State: "active"}}}}
	permit := planmodel.Permit{Run: r, Owner: planmodel.Owner{OwnerID: r.OwnerID, OwnerType: "plan", MachineID: "machine-A", State: "starting"}}
	result, err := NewExecutionLoop(f, "fixture-owner", nil).Run(t.Context(), permit)
	if err != nil || result.Run.State != "completed" || result.Run.CurrentItem != 2 || result.Run.Items[0].TaskRunID == result.Run.Items[1].TaskRunID {
		t.Fatal("编排未正确完成", result.Run.State, err)
	}
	scan, reply := -1, -1
	for i, event := range f.events {
		if event == "scan:0" {
			scan = i
		}
		if event == "message:"+r.Items[1].ID && reply < 0 {
			reply = i
		}
	}
	if scan < 0 || reply < scan {
		t.Fatal("未来回复在找简历前执行", f.events)
	}
}

// TestExecutionLoopSaveFailureDoesNotAdvance 验证开始步骤未确认时不进入扫描、后续岗位或结束结算。
func TestExecutionLoopSaveFailureDoesNotAdvance(t *testing.T) {
	r := serviceRunFixture(t)
	r.Items[0].State = "pending"
	r.Items[0].TaskRunID = ""
	for a := range r.Items[0].Actions {
		r.Items[0].Actions[a] = planmodel.ActionProgress{State: "pending"}
	}
	f := &loopRuntimeFixture{failSave: true}
	_, err := NewExecutionLoop(f, "fixture-owner", nil).Run(t.Context(), planmodel.Permit{Run: r})
	if err == nil || len(f.events) != 1 || f.events[0] != "prepare:0" {
		t.Fatal("未确认仍执行或推进", f.events, err)
	}
}

// TestExecutionLoopSafeFailureContinuesAsIncomplete 验证已可靠清理的单项失败继续后续项，整体不能显示全部成功。
func TestExecutionLoopSafeFailureContinuesAsIncomplete(t *testing.T) {
	r := serviceRunFixture(t)
	r.Items[0].State = "pending"
	r.Items[0].TaskRunID = ""
	r.Snapshot.Items[0].Actions = []string{"greeting"}
	r.Snapshot.Items[0].PrioritizeReply = false
	r.Items[0].Snapshot = r.Snapshot.Items[0]
	r.Items[0].Actions = map[string]planmodel.ActionProgress{"greeting": {State: "pending"}}
	f := &loopRuntimeFixture{safeScanFailure: true, counts: map[string]map[string]planmodel.ActionProgress{r.Items[0].ID: {"greeting": {State: "active", Count: 1}}, r.Items[1].ID: {"auto_reply": {State: "active"}}}}
	result, err := NewExecutionLoop(f, "fixture-owner", nil).Run(t.Context(), planmodel.Permit{Run: r})
	if err != nil || result.Run.State != "incomplete" || result.Run.CurrentItem != 2 || result.Run.Items[0].State != "failed" || result.Run.Items[0].Actions["greeting"].Count != 1 || result.Run.Items[1].State != "completed" {
		t.Fatal("安全单项失败未继续或被计为成功", result.Run.State, err)
	}
}
