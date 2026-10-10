// 本文件验证 HRPlus 历史报告设备归属，配置换电脑、软删除及缺失回执不能改写原执行来源。
package httpapi

import (
	"encoding/json"
	"errors"
	"testing"
)

// stopReportPlanFixture 使用实际停止和清理确认开放编辑，不绕过计划运行保护。
func stopReportPlanFixture(t *testing.T, store ExecutionPlanStore, plan ExecutionPlan) ExecutionPlan {
	t.Helper()
	stop := planIntentFixture(t, "stop")
	stop.ExpectedVersion = plan.Version
	stop.ActivationID = plan.ActivationID
	stopped, err := store.Intent(t.Context(), plan.TenantID, plan.UserEmail, plan.ID, stop)
	if err != nil {
		t.Fatal(err)
	}
	if !stopped.StopRequested {
		return stopped
	}
	request, _ := newExecutionPlanID()
	result, err := store.ConfirmStopped(t.Context(), plan.TenantID, plan.UserEmail, plan.ID, ExecutionPlanStopConfirmation{RequestID: request, ExpectedVersion: plan.Version, ActivationID: plan.ActivationID, MachineID: plan.MachineID, CleanupConfirmed: true})
	if err != nil || result.StopRequested || stopped.ActivationID != result.ActivationID {
		t.Fatal("未确认停止", err)
	}
	return result
}

// testHistoricalReportMachine 验证真正停止、换配置与软删除后，A 仍是旧报告来源而 B 不能冒领。
func testHistoricalReportMachine(t *testing.T, store ExecutionPlanStore) {
	t.Helper()
	unique, _ := newExecutionPlanID()
	email := unique + "@fixture.invalid"
	plan, summary := endedReportFixture(t, store, email, "A")
	if err := store.VerifyReportMachine(t.Context(), "", email, summary.RunID, "A"); err != nil {
		t.Fatal("原设备不匹配", err)
	}
	stopped := stopReportPlanFixture(t, store, plan)
	stopped.MachineID = "B"
	stopped.Config.Name = "已改到 B 的新配置"
	changed, err := store.Save(t.Context(), stopped, stopped.Version)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.VerifyReportMachine(t.Context(), "", email, summary.RunID, "A"); err != nil {
		t.Fatal("改电脑后原报告被拒绝", err)
	}
	if err := store.VerifyReportMachine(t.Context(), "", email, summary.RunID, "B"); !errors.Is(err, ErrAccountExecutionProof) {
		t.Fatal("当前电脑代替原执行电脑", err)
	}
	first, err := store.SaveReport(t.Context(), "", email, summary, "confirmed")
	if err != nil || first.Summary.PlanName == changed.Config.Name {
		t.Fatal("旧报告用了新配置", err)
	}
	if err := store.Delete(t.Context(), "", email, plan.ID, changed.Version); err != nil {
		t.Fatal(err)
	}
	if err := store.VerifyReportMachine(t.Context(), "", email, summary.RunID, "A"); err != nil {
		t.Fatal("软删除后原报告来源丢失", err)
	}
	again, err := store.SaveReport(t.Context(), "", email, summary, "confirmed")
	if err != nil || again.BodyHash != first.BodyHash {
		t.Fatal("软删除后报告内容改变", err)
	}
	for _, scope := range [][2]string{{"", "other@fixture.invalid"}, {"other-team", email}} {
		if err := store.VerifyReportMachine(t.Context(), scope[0], scope[1], summary.RunID, "A"); !errors.Is(err, ErrNotFound) {
			t.Fatal("历史报告跨账号或团队", err)
		}
	}
	// 破坏仅本次夹具的原领取快照，不能改用当前计划或另一个运行补充证明。
	switch source := store.(type) {
	case *MemoryExecutionPlanStore:
		source.mu.Lock()
		for key, receipt := range source.runClaims {
			if receipt.Result.Run.ID == summary.RunID {
				receipt.Result.Run.ConfigVersion++
				source.runClaims[key] = receipt
			}
		}
		source.mu.Unlock()
	case *PostgresExecutionPlanStore:
		_, err = source.db.Exec(`UPDATE execution_plan_requests SET result=jsonb_set(result,'{run,config_version}',to_jsonb($2::bigint)) WHERE plan_id=$1 AND kind='claim' AND result->'run'->>'id'=$3`, plan.ID, summary.ConfigVersion+1, summary.RunID)
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := store.VerifyReportMachine(t.Context(), "", email, summary.RunID, "A"); !errors.Is(err, ErrAccountExecutionProof) {
		t.Fatal("错误原快照仍授权历史上传", err)
	}
}

// TestHistoricalReportMachineStores 对内存与隔离 PostgreSQL 验证相同历史来源契约。
func TestHistoricalReportMachineStores(t *testing.T) {
	t.Run("memory", func(t *testing.T) { testHistoricalReportMachine(t, NewMemoryExecutionPlanStore()) })
	t.Run("postgres", func(t *testing.T) {
		testHistoricalReportMachine(t, NewPostgresExecutionPlanStore(planPostgresFixture(t)))
	})
}

// TestReportClaimMachineNoMutation 确认核对原快照只读，JSON 字段缺失不能猜测设备来源。
func TestReportClaimMachineNoMutation(t *testing.T) {
	store := NewMemoryExecutionPlanStore()
	plan, summary := endedReportFixture(t, store, "original-proof@fixture.invalid")
	before, _ := json.Marshal(store.runClaims)
	if err := store.VerifyReportMachine(t.Context(), "", plan.UserEmail, summary.RunID, "A"); err != nil {
		t.Fatal(err)
	}
	after, _ := json.Marshal(store.runClaims)
	if string(before) != string(after) {
		t.Fatal("来源核对修改原领取事实")
	}
	store.mu.Lock()
	for key, receipt := range store.runClaims {
		if receipt.Result.Run.ID == summary.RunID {
			delete(store.runClaims, key)
		}
	}
	store.mu.Unlock()
	if err := store.VerifyReportMachine(t.Context(), "", plan.UserEmail, summary.RunID, "A"); !errors.Is(err, ErrAccountExecutionProof) {
		t.Fatal("缺失原回执仍用当前设备授权", err)
	}
}
