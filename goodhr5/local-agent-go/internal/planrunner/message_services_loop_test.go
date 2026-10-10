// 本文件验证 HRPlus 父主循环读写轮换快照的实际边界，保存失败后不执行后来步骤。
package planrunner

import (
	"context"
	"fmt"
	"goodhr5/local-agent-go/internal/planmodel"
	"testing"
)

// durableLoopFixture 保留实际主循环，只替换页面/云端与轮换存储以验证调用先后。
type durableLoopFixture struct {
	loopRuntimeFixture
	fail  bool
	saves int
}

// LoadMessageState 模拟首次没有历史快照，当前工作由原服务核对。
func (f *durableLoopFixture) LoadMessageState(context.Context, planmodel.Run) ([]byte, error) {
	return nil, nil
}

// SaveMessageState 在规范进度确认后核对快照，故障时不向调用方给出保存成功。
func (f *durableLoopFixture) SaveMessageState(_ context.Context, run planmodel.Run, raw []byte) error {
	f.saves++
	if f.fail {
		return fmt.Errorf("fixture dispatch save failed")
	}
	service := NewMessageServices("fixture-owner")
	return service.Restore(raw, run, f.Platform)
}

// TestExecutionLoopDispatchSaveFailure 确认主循环实际尝试保存，失败后没有结算成功或继续下一项。
func TestExecutionLoopDispatchSaveFailure(t *testing.T) {
	run := serviceRunFixture(t)
	fixture := &durableLoopFixture{loopRuntimeFixture: loopRuntimeFixture{counts: map[string]map[string]planmodel.ActionProgress{run.Items[0].ID: run.Items[0].Actions}}, fail: true}
	permit := planmodel.Permit{Run: run, Owner: planmodel.Owner{OwnerID: run.OwnerID, OwnerType: "plan", State: "running", MachineID: "machine-A"}}
	_, err := NewExecutionLoop(fixture, "fixture-owner", nil).Run(t.Context(), permit)
	if err == nil || fixture.saves != 1 {
		t.Fatal("父轮换保存故障被忽略", err, fixture.saves)
	}
	for _, event := range fixture.events {
		if event == "finish:completed" || event == "prepare:1" {
			t.Fatal("保存失败仍继续或完成", fixture.events)
		}
	}
}
