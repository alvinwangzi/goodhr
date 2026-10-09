// 本文件使用真实 SQLite 验证 HRPlus 计划快照重开、账号隔离、原编号和递增计数不丢失。
package localdb

import (
	"database/sql"
	"encoding/json"
	"errors"
	"goodhr5/local-agent-go/internal/planmodel"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// snapshotFixture 读取包含重复岗位的完整运行夹具，不构造真实登录凭证。
func snapshotFixture(t *testing.T) (planmodel.Plan, planmodel.Run) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "planmodel", "testdata", "permit.json"))
	if err != nil {
		t.Fatal(err)
	}
	var p planmodel.Permit
	if err = json.Unmarshal(raw, &p); err != nil {
		t.Fatal(err)
	}
	plan := planmodel.Plan{ID: p.Run.PlanID, UserEmail: "fixture@example.com", MachineID: p.Owner.MachineID, Version: 1, StateSequence: 1, ActivationID: p.Run.ActivationID, State: "enabled", Config: p.Run.Snapshot, CreatedAt: time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC)}
	plan.UpdatedAt = plan.CreatedAt
	return plan, p.Run
}

// TestPlanSnapshotsRestart 验证真实关闭重开保留原项、未知结果及大整数，不跨账号读取。
func TestPlanSnapshotsRestart(t *testing.T) {
	db, cfg := openReplyDB(t)
	plan, run := snapshotFixture(t)
	if err := db.SaveCachedPlan(t.Context(), "A", plan); err != nil {
		t.Fatal(err)
	}
	run.Sequence = (1 << 53) + 17
	run.Items[0].State = "running"
	run.Items[0].Actions["auto_reply"] = planmodel.ActionProgress{State: "active", Count: 4, UnknownCount: 1}
	if err := db.SavePlanRunSnapshot(t.Context(), "A", run); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	restored, err := reopened.PlanRunSnapshot(t.Context(), "A", run.ID)
	if err != nil || restored.Sequence != run.Sequence || restored.Items[0].ID != run.Items[0].ID || restored.Items[0].Actions["auto_reply"].Count != 4 || restored.Items[0].Actions["auto_reply"].UnknownCount != 1 {
		t.Fatal("重启丢失原进度", err)
	}
	if _, err = reopened.PlanRunSnapshot(t.Context(), "B", run.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("跨账号读取运行", err)
	}
	byDate, err := reopened.PlanRunForDay(t.Context(), "A", run.PlanID, run.ActivationID, run.ExecutionDate)
	if err != nil || byDate.ID != run.ID {
		t.Fatal("重启无法按日期找回原编号", err)
	}
	plans, err := reopened.CachedPlans(t.Context(), "A")
	if err != nil || len(plans) != 1 || plans[0].Config.Items[0].PositionID != plans[0].Config.Items[1].PositionID {
		t.Fatal("缓存合并重复岗位", err)
	}
	plans, err = reopened.CachedPlans(t.Context(), "B")
	if err != nil || len(plans) != 0 {
		t.Fatal("跨账号读取计划", err)
	}
	// 两个快照表只保存安全配置与事实，不存在凭证列。
	var count int
	if err = reopened.conn.QueryRow(`SELECT count(*) FROM (SELECT name FROM pragma_table_info('plan_run_snapshots') UNION ALL SELECT name FROM pragma_table_info('plan_cache')) WHERE name IN ('token','credential','login_token')`).Scan(&count); err != nil || count != 0 {
		t.Fatal("普通快照存在凭证列", err)
	}
}

// TestPlanSnapshotsRejectStale 验证旧状态、计数倒退与换身份都不能覆盖已保存快照。
func TestPlanSnapshotsRejectStale(t *testing.T) {
	db, _ := openReplyDB(t)
	plan, run := snapshotFixture(t)
	if err := db.SaveCachedPlan(t.Context(), "A", plan); err != nil {
		t.Fatal(err)
	}
	stopped := plan
	stopped.State = "stopped"
	stopped.StateSequence = 2
	stopped.UpdatedAt = stopped.UpdatedAt.Add(time.Second)
	if err := db.SaveCachedPlan(t.Context(), "A", stopped); err != nil {
		t.Fatal(err)
	}
	if err := db.SaveCachedPlan(t.Context(), "A", plan); !errors.Is(err, ErrPlanSnapshotStale) {
		t.Fatal("旧启用覆盖停止", err)
	}
	if err := db.SaveCachedPlan(t.Context(), "A", stopped); err != nil {
		t.Fatal("相同回执不幂等", err)
	}
	forged := stopped
	forged.UserEmail = "foreign@example.com"
	forged.StateSequence++
	if err := db.SaveCachedPlan(t.Context(), "A", forged); !errors.Is(err, ErrPlanRequestConflict) {
		t.Fatal("新序号改变所有者", err)
	}
	run.Sequence = 2
	run.Items[0].State = "running"
	run.Items[0].Actions["auto_reply"] = planmodel.ActionProgress{State: "active", Count: 4}
	if err := db.SavePlanRunSnapshot(t.Context(), "A", run); err != nil {
		t.Fatal(err)
	}
	old := run
	old.Sequence = 1
	if err := db.SavePlanRunSnapshot(t.Context(), "A", old); !errors.Is(err, ErrPlanSnapshotStale) {
		t.Fatal("旧序号覆盖运行", err)
	}
	bad := run
	raw, _ := json.Marshal(run)
	_ = json.Unmarshal(raw, &bad)
	bad.Sequence = 3
	bad.Items[0].Actions["auto_reply"] = planmodel.ActionProgress{State: "active", Count: 1}
	if err := db.SavePlanRunSnapshot(t.Context(), "A", bad); !errors.Is(err, ErrPlanSnapshotStale) {
		t.Fatal("新序号倒退计数", err)
	}
	bad = run
	bad.ID = "20000000-0000-0000-0000-000000000002"
	if err := db.SavePlanRunSnapshot(t.Context(), "A", bad); !errors.Is(err, ErrPlanRequestConflict) {
		t.Fatal("同批次同日期重建运行编号", err)
	}
	saved, err := db.PlanRunSnapshot(t.Context(), "A", run.ID)
	if err != nil || saved.Sequence != 2 || saved.Items[0].Actions["auto_reply"].Count != 4 {
		t.Fatal("拒绝后原事实改变", err)
	}
}
