// 本文件验证 HRPlus 完全离线且无排队记录时的原日记账，使用冻结启用回执和独占数据库，不执行招聘或邮件动作。
package httpapi

import (
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"
)

// TestOfflinePostgresClaimRace 验证日末补报与真实领取共享计划锁，只能保留一个原日事实。
func TestOfflinePostgresClaimRace(t *testing.T) {
	store := reportRecoveryPostgres(t)
	id, _ := newExecutionPlanID()
	plan := createArmedPlanFixture(t, store, id+"@example.com")
	loc, _ := time.LoadLocation(plan.Config.Schedule.Timezone)
	plan = freezePlanIntentTimeFixture(t, store, plan, time.Date(2026, 10, 6, 8, 0, 0, 0, loc))
	claim := planRunClaimFixture(t, plan)
	claim.ExecutionDate = "2026-10-06"
	start := make(chan struct{})
	var group sync.WaitGroup
	var claimErr, reportErr error
	group.Add(2)
	go func() {
		defer group.Done()
		<-start
		_, claimErr = store.ClaimRun(t.Context(), "", plan.UserEmail, claim)
	}()
	go func() {
		defer group.Done()
		<-start
		reportErr = store.SettleUnstartedWaits(t.Context(), time.Date(2026, 10, 6, 20, 6, 0, 0, loc), 20)
	}()
	close(start)
	group.Wait()
	if reportErr != nil || claimErr != nil && !errors.Is(claimErr, ErrExecutionPlanBusy) && !errors.Is(claimErr, ErrExecutionPlanRequest) {
		t.Fatal("领取或补报竞争异常", reportErr, claimErr)
	}
	runs, err := store.ListRuns(t.Context(), "", plan.UserEmail, plan.ID)
	if err != nil || len(runs) != 1 {
		t.Fatal("竞争产生两套原日事实", err, len(runs))
	}
	if claimErr == nil {
		if runs[0].ID != claim.RunID || runs[0].OwnerID != claim.OwnerID || runs[0].State != "starting" {
			t.Fatal("真实领取被改写")
		}
		if _, err := store.GetReport(t.Context(), "", plan.UserEmail, claim.RunID); !errors.Is(err, ErrNotFound) {
			t.Fatal("真实领取日创建零动作报告", err)
		}
	} else if runs[0].State != "incomplete" || runs[0].OwnerID != "" {
		t.Fatal("零动作补报创建了执行权")
	} else {
		snapshot, err := store.RuntimeSnapshot(t.Context(), "", plan.UserEmail, plan.ID)
		if err != nil || snapshot.AccountOwner != nil {
			t.Fatal("失败领取留下账号占用", err)
		}
	}
}

// TestOfflinePostgresReportRollback 验证报告写入失败时原父运行和子项全部回滚，修复后仍可按原日重试。
func TestOfflinePostgresReportRollback(t *testing.T) {
	store := reportRecoveryPostgres(t).(*PostgresExecutionPlanStore)
	id, _ := newExecutionPlanID()
	plan := createArmedPlanFixture(t, store, id+"@example.com")
	loc, _ := time.LoadLocation(plan.Config.Schedule.Timezone)
	plan = freezePlanIntentTimeFixture(t, store, plan, time.Date(2026, 10, 6, 8, 0, 0, 0, loc))
	if _, err := store.db.Exec(`CREATE FUNCTION offline_report_failure() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'owned offline report failure'; END $$; CREATE TRIGGER offline_report_failure BEFORE INSERT ON execution_plan_reports FOR EACH ROW EXECUTE FUNCTION offline_report_failure()`); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 6, 20, 6, 0, 0, loc)
	if err := store.SettleUnstartedWaits(t.Context(), now, 20); err == nil {
		t.Fatal("报告写入失败仍返回成功")
	}
	runID := scheduledReportRunID(plan.ID, plan.ActivationID, "2026-10-06")
	if _, err := store.GetRun(t.Context(), "", plan.UserEmail, runID); !errors.Is(err, ErrNotFound) {
		t.Fatal("失败事务留下父运行", err)
	}
	var count int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM execution_plan_item_runs WHERE run_id=$1`, runID).Scan(&count); err != nil || count != 0 {
		t.Fatal("失败事务留下独立项", err, count)
	}
	if _, err := store.db.Exec(`DROP TRIGGER offline_report_failure ON execution_plan_reports; DROP FUNCTION offline_report_failure()`); err != nil {
		t.Fatal(err)
	}
	if err := store.SettleUnstartedWaits(t.Context(), now, 20); err != nil {
		t.Fatal("回滚后不能重试", err)
	}
	if run, err := store.GetRun(t.Context(), "", plan.UserEmail, runID); err != nil || run.State != "incomplete" {
		t.Fatal("原日重试没有正常结算", err)
	}
}

// TestOfflinePostgresPreservesClaim 验证实际已领取日不被补报，真实账号占用在其他离线日结算后仍存在。
func TestOfflinePostgresPreservesClaim(t *testing.T) {
	store := reportRecoveryPostgres(t)
	id, _ := newExecutionPlanID()
	plan := createArmedPlanFixture(t, store, id+"@example.com")
	loc, _ := time.LoadLocation(plan.Config.Schedule.Timezone)
	plan = freezePlanIntentTimeFixture(t, store, plan, time.Date(2026, 10, 6, 8, 0, 0, 0, loc))
	claim := planRunClaimFixture(t, plan)
	claim.ExecutionDate = "2026-10-06"
	permit, err := store.ClaimRun(t.Context(), "", plan.UserEmail, claim)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SettleUnstartedWaits(t.Context(), time.Date(2026, 10, 7, 20, 6, 0, 0, loc), 1); err != nil {
		t.Fatal(err)
	}
	snapshot, err := store.RuntimeSnapshot(t.Context(), "", plan.UserEmail, plan.ID)
	if err != nil || snapshot.AccountOwner == nil || snapshot.AccountOwner.OwnerID != claim.OwnerID {
		t.Fatal("日报释放真实占用", err)
	}
	original, err := store.GetRun(t.Context(), "", plan.UserEmail, permit.Run.ID)
	if err != nil || original.State != permit.Run.State || original.Sequence != permit.Run.Sequence {
		t.Fatal("真实领取被零动作报告覆盖", err)
	}
	if _, err := store.GetReport(t.Context(), "", plan.UserEmail, original.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("已领取日生成假未开始报告", err)
	}
	runs, err := store.ListRuns(t.Context(), "", plan.UserEmail, plan.ID)
	if err != nil || len(runs) != 2 {
		t.Fatal("已有运行阻止其他离线日记账", err, len(runs))
	}
}

// TestOfflineLegacyWaitStores 验证缺少原启用回执的旧数据仍可使用已冻结等待配置结算，不猜测无证据的日期。
func TestOfflineLegacyWaitStores(t *testing.T) {
	for _, kind := range []string{"memory", "postgres"} {
		t.Run(kind, func(t *testing.T) {
			var store ExecutionPlanStore = NewMemoryExecutionPlanStore()
			if kind == "postgres" {
				store = reportRecoveryPostgres(t)
			}
			id, _ := newExecutionPlanID()
			plan := createArmedPlanFixture(t, store, id+"@example.com")
			loc, _ := time.LoadLocation(plan.Config.Schedule.Timezone)
			request, _ := newExecutionPlanID()
			wait := ExecutionPlanWait{PlanID: plan.ID, ActivationID: plan.ActivationID, ConfigVersion: plan.Version, MachineID: plan.MachineID, RequestID: request, TriggeredAt: time.Date(2026, 10, 6, 9, 0, 0, 0, loc)}
			if _, err := store.RecordWait(t.Context(), "", plan.UserEmail, wait); err != nil {
				t.Fatal(err)
			}
			switch s := store.(type) {
			case *MemoryExecutionPlanStore:
				for key, receipt := range s.intents {
					if receipt.Result.ID == plan.ID && receipt.Result.State == "enabled" {
						delete(s.intents, key)
					}
				}
			case *PostgresExecutionPlanStore:
				if _, err := s.db.Exec(`DELETE FROM execution_plan_requests WHERE plan_id=$1 AND kind='arm'`, plan.ID); err != nil {
					t.Fatal(err)
				}
			}
			if err := store.SettleUnstartedWaits(t.Context(), time.Date(2026, 10, 8, 21, 0, 0, 0, loc), 20); err != nil {
				t.Fatal(err)
			}
			runs, err := store.ListRuns(t.Context(), "", plan.UserEmail, plan.ID)
			if err != nil || len(runs) != 1 || runs[0].ExecutionDate != "2026-10-06" {
				t.Fatal("旧冻结等待不再结算或凭空补造其他日", err, runs)
			}
		})
	}
}

// freezePlanIntentTimeFixture 固定隔离夹具的服务器启用或停止时间，避免日历验收依赖当前机器时钟。
func freezePlanIntentTimeFixture(t *testing.T, store ExecutionPlanStore, plan ExecutionPlan, at time.Time) ExecutionPlan {
	t.Helper()
	kind := "arm"
	if plan.State == "enabled" {
		plan.CreatedAt = at.Add(-time.Minute)
	} else {
		kind = "stop"
	}
	plan.UpdatedAt = at
	switch s := store.(type) {
	case *MemoryExecutionPlanStore:
		s.mu.Lock()
		defer s.mu.Unlock()
		s.plans[plan.ID] = cloneExecutionPlan(plan)
		for key, receipt := range s.intents {
			if receipt.Result.ID == plan.ID && receipt.Result.ActivationID == plan.ActivationID && receipt.Result.State == plan.State {
				receipt.Result = cloneExecutionPlan(plan)
				s.intents[key] = receipt
			}
		}
	case *PostgresExecutionPlanStore:
		raw, _ := json.Marshal(plan)
		if _, err := s.db.Exec(`UPDATE execution_plans SET created_at=$2,updated_at=$3 WHERE id=$1`, plan.ID, plan.CreatedAt, at); err != nil {
			t.Fatal(err)
		}
		if _, err := s.db.Exec(`UPDATE execution_plan_requests SET result=$2 WHERE plan_id=$1 AND kind=$3`, plan.ID, string(raw), kind); err != nil {
			t.Fatal(err)
		}
	}
	return plan
}

// TestOfflinePlanWithoutWaitStores 验证无需任何本地上报即可结算，原日重复检查幂等且次日仍可新领取。
func TestOfflinePlanWithoutWaitStores(t *testing.T) {
	for _, kind := range []string{"memory", "postgres"} {
		t.Run(kind, func(t *testing.T) {
			var store ExecutionPlanStore = NewMemoryExecutionPlanStore()
			if kind == "postgres" {
				store = reportRecoveryPostgres(t)
			}
			id, _ := newExecutionPlanID()
			plan := createArmedPlanFixture(t, store, id+"@example.com")
			loc, _ := time.LoadLocation(plan.Config.Schedule.Timezone)
			plan = freezePlanIntentTimeFixture(t, store, plan, time.Date(2026, 10, 6, 8, 0, 0, 0, loc))
			closed := time.Date(2026, 10, 6, 20, 6, 0, 0, loc)
			if err := store.SettleUnstartedWaits(t.Context(), closed.Add(-time.Second), 20); err != nil {
				t.Fatal(err)
			}
			if runs, err := store.ListRuns(t.Context(), "", plan.UserEmail, plan.ID); err != nil || len(runs) != 0 {
				t.Fatal("最大收尾前提前结算", err)
			}
			if err := store.SettleUnstartedWaits(t.Context(), closed, 20); err != nil {
				t.Fatal(err)
			}
			runs, err := store.ListRuns(t.Context(), "", plan.UserEmail, plan.ID)
			if err != nil || len(runs) != 1 {
				t.Fatal("完全离线的原日没有报告", err, len(runs))
			}
			run := runs[0]
			if run.State != "incomplete" || run.StartedAt != nil || run.OwnerID != "" || run.ExecutionDate != "2026-10-06" {
				t.Fatal("离线报告伪造启动", run)
			}
			for _, item := range run.Items {
				if item.TaskRunID != "" {
					t.Fatal("离线报告创建 TaskRun")
				}
				for _, action := range item.Actions {
					if action.Count != 0 || action.State != "pending" {
						t.Fatal("离线报告伪造动作")
					}
				}
			}
			report, err := store.GetReport(t.Context(), "", plan.UserEmail, run.ID)
			if err != nil || report.Summary.NextNominalAt == nil {
				t.Fatal("离线报告没有保留周期安排", err)
			}
			if err := store.SettleUnstartedWaits(t.Context(), closed.Add(time.Hour), 20); err != nil {
				t.Fatal(err)
			}
			again, err := store.GetReport(t.Context(), "", plan.UserEmail, run.ID)
			if err != nil || again.BodyHash != report.BodyHash || !again.Summary.GeneratedAt.Equal(report.Summary.GeneratedAt) {
				t.Fatal("离线报告被重新生成", err)
			}
			current, err := store.Get(t.Context(), "", plan.UserEmail, plan.ID)
			if err != nil || current.State != "enabled" {
				t.Fatal("离线日关闭计划", err)
			}
			claim := planRunClaimFixture(t, plan)
			claim.ExecutionDate = "2026-10-07"
			if permit, err := store.ClaimRun(t.Context(), "", plan.UserEmail, claim); err != nil || permit.Run.CurrentItem != 0 {
				t.Fatal("次日不能从头执行", err)
			}
		})
	}
}

// TestOfflineHistoricalStopEditStores 验证没有本地队列时，停止、改名及软删除后的过去日报仍使用原启用配置。
func TestOfflineHistoricalStopEditStores(t *testing.T) {
	for _, kind := range []string{"memory", "postgres"} {
		t.Run(kind, func(t *testing.T) {
			var store ExecutionPlanStore = NewMemoryExecutionPlanStore()
			if kind == "postgres" {
				store = reportRecoveryPostgres(t)
			}
			id, _ := newExecutionPlanID()
			plan := createArmedPlanFixture(t, store, id+"@example.com")
			loc, _ := time.LoadLocation(plan.Config.Schedule.Timezone)
			plan = freezePlanIntentTimeFixture(t, store, plan, time.Date(2026, 10, 6, 8, 0, 0, 0, loc))
			originalName := plan.Config.Name
			stop := planIntentFixture(t, "stop")
			stop.ActivationID = plan.ActivationID
			plan, err := store.Intent(t.Context(), "", plan.UserEmail, plan.ID, stop)
			if err != nil {
				t.Fatal(err)
			}
			plan = freezePlanIntentTimeFixture(t, store, plan, time.Date(2026, 10, 7, 8, 0, 0, 0, loc))
			plan.Config.Name = "后来编辑的名称"
			plan, err = store.Save(t.Context(), plan, plan.Version)
			if err != nil {
				t.Fatal(err)
			}
			if err = store.Delete(t.Context(), "", plan.UserEmail, plan.ID, plan.Version); err != nil {
				t.Fatal(err)
			}
			if err = store.SettleUnstartedWaits(t.Context(), time.Date(2026, 10, 10, 21, 0, 0, 0, loc), 20); err != nil {
				t.Fatal(err)
			}
			runs, err := store.ListRuns(t.Context(), "", plan.UserEmail, plan.ID)
			if err != nil || len(runs) != 1 || runs[0].ExecutionDate != "2026-10-06" || runs[0].ConfigVersion != 1 || runs[0].Snapshot.Name != originalName {
				t.Fatal("历史启用被新配置替代或停止后仍生成未来日", err, runs)
			}
			report, err := store.GetReport(t.Context(), "", plan.UserEmail, runs[0].ID)
			if err != nil || report.Summary.NextNominalAt != nil {
				t.Fatal("已经停止的历史报告虚构后续安排", err)
			}
		})
	}
}

// TestOfflineReportLimitAndExistingRun 验证每批只新增规定数量，已有任何运行都不被零动作摘要覆盖或释放。
func TestOfflineReportLimitAndExistingRun(t *testing.T) {
	store := NewMemoryExecutionPlanStore()
	plan := createArmedPlanFixture(t, store, "offline-limit@example.com")
	loc, _ := time.LoadLocation(plan.Config.Schedule.Timezone)
	plan = freezePlanIntentTimeFixture(t, store, plan, time.Date(2026, 10, 6, 8, 0, 0, 0, loc))
	claim := planRunClaimFixture(t, plan)
	claim.ExecutionDate = "2026-10-07"
	permit, err := store.ClaimRun(t.Context(), "", plan.UserEmail, claim)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 8, 20, 6, 0, 0, loc)
	for expected := 1; expected <= 2; expected++ {
		if err := store.SettleUnstartedWaits(t.Context(), now, 1); err != nil {
			t.Fatal(err)
		}
		if len(store.reports) != expected {
			t.Fatal("每批数量不正确或已有运行消耗报告位置", len(store.reports))
		}
	}
	if err := store.SettleUnstartedWaits(t.Context(), now, 1); err != nil {
		t.Fatal(err)
	}
	if len(store.reports) != 2 {
		t.Fatal("已经报告的日重复生成")
	}
	original, err := store.GetRun(t.Context(), "", plan.UserEmail, permit.Run.ID)
	if err != nil || original.State != permit.Run.State || original.OwnerID != claim.OwnerID || original.Sequence != permit.Run.Sequence {
		t.Fatal("原运行被改写", err)
	}
	if _, held := store.owners[claim.OwnerID]; !held {
		t.Fatal("日报释放了真实账号占用")
	}
}

// TestOfflineCalendarEligibility 验证迟到启用不补造今天机会、一次/每周/有效日期以及夏令时日的名义时间。
func TestOfflineCalendarEligibility(t *testing.T) {
	loc, _ := time.LoadLocation("Asia/Shanghai")
	for _, kind := range []string{"late_arm", "once", "weekly", "range", "dst"} {
		t.Run(kind, func(t *testing.T) {
			p := ExecutionPlan{ID: "fixture", ActivationID: "activation", Config: validPlanConfig(), UpdatedAt: time.Date(2026, 10, 6, 8, 0, 0, 0, loc)}
			now := time.Date(2026, 10, 8, 20, 6, 0, 0, loc)
			want := 1
			switch kind {
			case "late_arm":
				p.UpdatedAt = time.Date(2026, 10, 8, 20, 1, 0, 0, loc)
				want = 0
			case "once":
				p.Config.Schedule.Cycle = "once"
				p.Config.Schedule.OnceDate = "2026-10-06"
			case "weekly":
				p.Config.Schedule.Cycle = "weekly"
				p.Config.Schedule.Weekdays = []int{3}
			case "range":
				p.Config.Schedule.StartDate = "2026-10-07"
				p.Config.Schedule.EndDate = "2026-10-07"
			case "dst":
				dst, _ := time.LoadLocation("America/New_York")
				p.Config.Schedule.Timezone = dst.String()
				p.UpdatedAt = time.Date(2026, 3, 8, 0, 0, 0, 0, dst)
				now = time.Date(2026, 3, 8, 20, 6, 0, 0, dst)
			}
			period := offlinePlanPeriod{plan: p}
			if kind == "dst" {
				early, err := offlineReportTriggers(t.Context(), period, now.Add(-time.Second), map[string]bool{}, 20)
				if err != nil || len(early) != 0 {
					t.Fatal("夏令时日提前结算", err)
				}
			}
			triggers, err := offlineReportTriggers(t.Context(), period, now, map[string]bool{}, 20)
			if err != nil || len(triggers) != want {
				t.Fatal("离线报告日期资格不匹配", err, triggers)
			}
			if kind == "dst" && triggers[0].Hour() != 9 {
				t.Fatal("夏令时把名义九点改成其他时刻", triggers)
			}
		})
	}
}
