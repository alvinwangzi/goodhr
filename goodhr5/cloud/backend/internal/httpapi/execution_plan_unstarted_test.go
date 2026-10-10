// 本文件验证 HRPlus 排队未开始日末记账，冻结配置、零动作、原运行竞争和次日启用状态均保持真实。
package httpapi

import (
	"testing"
	"time"
)

// testUnstartedWaitReport 验证未开始日报告只生成一次，不创建执行权或 TaskRun，不覆盖原运行。
func testUnstartedWaitReport(t *testing.T, store ExecutionPlanStore) {
	t.Helper()
	id, _ := newExecutionPlanID()
	email := id + "@example.com"
	plan := createArmedPlanFixture(t, store, email)
	loc, _ := time.LoadLocation(plan.Config.Schedule.Timezone)
	window := plan.Config.Schedule.Windows[0]
	start := time.Date(2026, 10, 10, window.StartMinute/60, window.StartMinute%60, 0, 0, loc)
	request, _ := newExecutionPlanID()
	wait := ExecutionPlanWait{PlanID: plan.ID, RequestID: request, ActivationID: plan.ActivationID, ConfigVersion: plan.Version, MachineID: plan.MachineID, TriggeredAt: start}
	if _, err := store.RecordWait(t.Context(), "", email, wait); err != nil {
		t.Fatal(err)
	}
	if err := store.SettleUnstartedWaits(t.Context(), start, 20); err != nil {
		t.Fatal(err)
	}
	runs, err := store.ListRuns(t.Context(), "", email, plan.ID)
	if err != nil || len(runs) != 0 {
		t.Fatal("到点排队被误认为实际开始", err)
	}
	closed := start.Add(48 * time.Hour)
	if err := store.SettleUnstartedWaits(t.Context(), closed, 20); err != nil {
		t.Fatal(err)
	}
	runs, err = store.ListRuns(t.Context(), "", email, plan.ID)
	if err != nil || len(runs) != 1 {
		t.Fatal("未开始日漏报或重复记录", err, len(runs))
	}
	run := runs[0]
	if run.OwnerID != "" || run.StartedAt != nil || run.State != "incomplete" || run.EndReason != "plan_never_started" || run.CurrentItem != 0 {
		t.Fatal("未开始记账补造执行事实", run)
	}
	for _, item := range run.Items {
		if item.TaskRunID != "" || item.State != "pending" {
			t.Fatal("未开始记账创建了任务", item)
		}
		for _, action := range item.Actions {
			if action.Count != 0 || action.UnknownCount != 0 || action.State != "pending" {
				t.Fatal("未开始动作虚构数量", action)
			}
		}
	}
	report, err := store.GetReport(t.Context(), "", email, run.ID)
	if err != nil || len(report.Summary.UnfinishedItemIDs) != len(plan.Config.Items) {
		t.Fatal("未开始项未列入报告", err)
	}
	if report.Summary.NextNominalAt == nil {
		t.Fatal("启用的周期计划报告丢失后续名义安排")
	}
	if err := store.SettleUnstartedWaits(t.Context(), closed.Add(time.Hour), 20); err != nil {
		t.Fatal(err)
	}
	again, err := store.GetReport(t.Context(), "", email, run.ID)
	if err != nil || again.BodyHash != report.BodyHash || !again.Summary.GeneratedAt.Equal(report.Summary.GeneratedAt) {
		t.Fatal("重复检查改写原报告", err)
	}
	current, err := store.Get(t.Context(), "", email, plan.ID)
	if err != nil || current.State != "enabled" || current.ActivationID != plan.ActivationID {
		t.Fatal("未开始日末停用周期计划", err)
	}
	claim := planRunClaimFixture(t, plan)
	claim.ExecutionDate = "2026-10-11"
	permit, err := store.ClaimRun(t.Context(), "", email, claim)
	if err != nil || permit.Run.CurrentItem != 0 || permit.Run.ID == run.ID {
		t.Fatal("次日不能从第一项新开始", err)
	}
	if err := store.SettleUnstartedWaits(t.Context(), closed, 20); err != nil {
		t.Fatal(err)
	}
	active, err := store.GetRun(t.Context(), "", email, permit.Run.ID)
	if err != nil || active.State != "starting" || active.OwnerID != claim.OwnerID {
		t.Fatal("原检查覆盖实际已领取运行", err)
	}
}

// TestUnstartedWaitStores 在独占 PostgreSQL 和内存核对原日结算，不污染其他测试的待报告记录。
func TestUnstartedWaitStores(t *testing.T) {
	t.Run("memory", func(t *testing.T) { testUnstartedWaitReport(t, NewMemoryExecutionPlanStore()) })
	t.Run("postgres", func(t *testing.T) { testUnstartedWaitReport(t, reportRecoveryPostgres(t)) })
}

// TestUnstartedSnapshotAfterEdit 验证停止、重新编辑和软删除不让旧排队报告使用后来岗位配置。
func TestUnstartedSnapshotAfterEdit(t *testing.T) {
	store := NewMemoryExecutionPlanStore()
	plan := createArmedPlanFixture(t, store, "unstarted-edit@example.com")
	loc, _ := time.LoadLocation(plan.Config.Schedule.Timezone)
	first := plan.Config.Schedule.Windows[0]
	at := time.Date(2026, 10, 10, first.StartMinute/60, first.StartMinute%60, 0, 0, loc)
	request, _ := newExecutionPlanID()
	if _, err := store.RecordWait(t.Context(), "", plan.UserEmail, ExecutionPlanWait{PlanID: plan.ID, RequestID: request, ActivationID: plan.ActivationID, ConfigVersion: plan.Version, MachineID: plan.MachineID, TriggeredAt: at}); err != nil {
		t.Fatal(err)
	}
	stopped := stopReportPlanFixture(t, store, plan)
	stopped.Config.Name = "后来的新名字"
	stopped.MachineID = "B"
	updated, err := store.Save(t.Context(), stopped, stopped.Version)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Delete(t.Context(), "", plan.UserEmail, plan.ID, updated.Version); err != nil {
		t.Fatal(err)
	}
	if err := store.SettleUnstartedWaits(t.Context(), at.Add(48*time.Hour), 20); err != nil {
		t.Fatal(err)
	}
	runs, err := store.ListRuns(t.Context(), "", plan.UserEmail, plan.ID)
	if err != nil || len(runs) != 1 {
		t.Fatal(err)
	}
	report, err := store.GetReport(t.Context(), "", plan.UserEmail, runs[0].ID)
	if err != nil || report.Summary.PlanName != plan.Config.Name || report.Summary.ConfigVersion != plan.Version {
		t.Fatal("旧排队报告用了后来配置", err)
	}
}

// TestUnstartedDoesNotReplaceClaim 验证同日实际领取在前时，原排队日末检查不能改写它或生成假未开始报告。
func TestUnstartedDoesNotReplaceClaim(t *testing.T) {
	store := NewMemoryExecutionPlanStore()
	plan := createArmedPlanFixture(t, store, "unstarted-claim@example.com")
	loc, _ := time.LoadLocation(plan.Config.Schedule.Timezone)
	first := plan.Config.Schedule.Windows[0]
	at := time.Date(2026, 10, 10, first.StartMinute/60, first.StartMinute%60, 0, 0, loc)
	request, _ := newExecutionPlanID()
	if _, err := store.RecordWait(t.Context(), "", plan.UserEmail, ExecutionPlanWait{PlanID: plan.ID, RequestID: request, ActivationID: plan.ActivationID, ConfigVersion: plan.Version, MachineID: plan.MachineID, TriggeredAt: at}); err != nil {
		t.Fatal(err)
	}
	claim := planRunClaimFixture(t, plan)
	original, err := store.ClaimRun(t.Context(), "", plan.UserEmail, claim)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SettleUnstartedWaits(t.Context(), at.Add(48*time.Hour), 20); err != nil {
		t.Fatal(err)
	}
	run, err := store.GetRun(t.Context(), "", plan.UserEmail, original.Run.ID)
	if err != nil || run.State != original.Run.State || run.Sequence != original.Run.Sequence || run.OwnerID != original.Run.OwnerID {
		t.Fatal("日末假未开始覆盖真实领取", err)
	}
	if len(store.reports) != 0 {
		t.Fatal("已领取运行生成了假未开始报告")
	}
}
