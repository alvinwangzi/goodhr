// 本文件验证 HRPlus 计划存储的版本、权限、事务回滚和实际 PostgreSQL 编辑互斥。
package httpapi

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// TestMemoryPlanStoreIsolation 验证配置深拷贝、重复岗位、越权和停止确认保护。
func TestMemoryPlanStoreIsolation(t *testing.T) {
	s := NewMemoryExecutionPlanStore()
	p, err := s.Save(t.Context(), ExecutionPlan{TenantID: "team", UserEmail: "A", MachineID: "device", Config: validPlanConfig()}, 0)
	if err != nil {
		t.Fatal(err)
	}
	p.Config.Items[0].Actions[0] = "greeting"
	saved, err := s.Get(t.Context(), "team", "A", p.ID)
	if err != nil || saved.Config.Items[0].Actions[0] != "auto_reply" {
		t.Fatal("配置被调用者原地篡改")
	}
	if _, err = s.Get(t.Context(), "team", "B", p.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("另一用户读取了计划")
	}
	if _, err = s.Save(t.Context(), saved, 0); !errors.Is(err, ErrExecutionPlanVersion) {
		t.Fatal("旧版本覆盖了配置")
	}
	s.runs["run"] = ExecutionPlanRun{ID: "run", PlanID: p.ID, State: "waiting_window"}
	if _, err = s.Save(t.Context(), saved, 1); !errors.Is(err, ErrExecutionPlanBusy) {
		t.Fatal("自动间歇仍允许编辑")
	}
	s.runs["run"] = ExecutionPlanRun{ID: "run", PlanID: p.ID, State: "stopped"}
	copy := s.plans[p.ID]
	copy.StopRequested = true
	s.plans[p.ID] = copy
	if err = s.Delete(t.Context(), "team", "A", p.ID, 1); !errors.Is(err, ErrExecutionPlanBusy) {
		t.Fatal("停止未确认允许删除")
	}
	copy.StopRequested = false
	s.plans[p.ID] = copy
	updated, err := s.Save(t.Context(), saved, 1)
	if err != nil || updated.Version != 2 || updated.State != "stopped" {
		t.Fatal("保存自动开始或版本错误", err)
	}
	if err = s.Delete(t.Context(), "team", "A", p.ID, 2); err != nil || len(s.runs) != 1 {
		t.Fatal("删除丢失历史", err)
	}
}

// planPostgresFixture 只连接显式的 M2 独立数据库，并复用真实增量迁移。
func planPostgresFixture(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("GOODHR_EXECUTION_PLAN_TEST_PG_DSN")
	if dsn == "" {
		t.Skip("需要 M2 独立 PostgreSQL")
	}
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	t.Chdir(filepath.Join("..", ".."))
	if err = RunMigrations(db); err != nil {
		t.Fatal(err)
	}
	return db
}

// postgresPlanConfig 使用真实 UUID 构建可保存的重复岗位编排。
func postgresPlanConfig() ExecutionPlanConfig {
	c := validPlanConfig()
	for i := range c.Items {
		c.Items[i].PositionID = "20000000-0000-0000-0000-000000000001"
	}
	return c
}

// TestPlanStorePostgres 验证子项失败完整回滚，未释放的占用持续禁止编辑。
func TestPlanStorePostgres(t *testing.T) {
	db := planPostgresFixture(t)
	s := NewPostgresExecutionPlanStore(db)
	email, _ := newExecutionPlanID()
	email += "@fixture.test"
	p, err := s.Save(t.Context(), ExecutionPlan{UserEmail: email, MachineID: "A", Config: postgresPlanConfig()}, 0)
	if err != nil {
		t.Fatal(err)
	}
	bad := cloneExecutionPlan(p)
	bad.Config.Name = "不应保存"
	bad.Config.Items[1].PositionID = "not-a-uuid"
	if _, err = s.Save(t.Context(), bad, 1); err == nil {
		t.Fatal("子项失败仍提交")
	}
	old, err := s.Get(t.Context(), "", email, p.ID)
	if err != nil || old.Version != 1 || old.Config.Name != p.Config.Name {
		t.Fatal("配置或版本未回滚", err)
	}
	var count int
	if err = db.QueryRow(`SELECT count(*) FROM execution_plan_items WHERE plan_id=$1`, p.ID).Scan(&count); err != nil || count != 2 {
		t.Fatal("部分执行项被留下", err)
	}
	run, _ := newExecutionPlanID()
	batch, _ := newExecutionPlanID()
	owner, _ := newExecutionPlanID()
	_, err = db.Exec(`INSERT INTO execution_plan_runs(id,plan_id,activation_id,execution_date,config_version,snapshot,state) VALUES($1,$2,$3,'2026-10-10',1,'{}','waiting_window')`, run, p.ID, batch)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Save(t.Context(), p, 1); !errors.Is(err, ErrExecutionPlanBusy) {
		t.Fatal("间歇运行未阻止编辑", err)
	}
	_, err = db.Exec(`UPDATE execution_plan_runs SET state='completed',owner_id=$2 WHERE id=$1`, run, owner)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`INSERT INTO account_execution_owners(account_key,user_email,machine_id,owner_type,owner_id,credential_hash,state) VALUES($1,$1,'A','plan',$2,'fixture-hash','releasing')`, email, owner)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Delete(t.Context(), "", email, p.ID, 1); !errors.Is(err, ErrExecutionPlanBusy) {
		t.Fatal("收尾中仍允许删除", err)
	}
	if _, err = db.Exec(`DELETE FROM account_execution_owners WHERE owner_id=$1`, owner); err != nil {
		t.Fatal(err)
	}
	if err = s.Delete(t.Context(), "", email, p.ID, 1); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Get(t.Context(), "", email, p.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("删除后仍可读")
	}
	if err = db.QueryRow(`SELECT count(*) FROM execution_plan_runs WHERE id=$1`, run).Scan(&count); err != nil || count != 1 {
		t.Fatal("运行历史被删除", err)
	}
}

// TestPlanStoreConcurrentVersion 验证同版本的并发编辑仅有一份提交，其余明确返回版本冲突。
func TestPlanStoreConcurrentVersion(t *testing.T) {
	db := planPostgresFixture(t)
	s := NewPostgresExecutionPlanStore(db)
	email, _ := newExecutionPlanID()
	p, err := s.Save(t.Context(), ExecutionPlan{UserEmail: email, MachineID: "A", Config: postgresPlanConfig()}, 0)
	if err != nil {
		t.Fatal(err)
	}
	var group sync.WaitGroup
	results := make(chan error, 2)
	for _, name := range []string{"编辑一", "编辑二"} {
		group.Add(1)
		go func(name string) {
			defer group.Done()
			copy := cloneExecutionPlan(p)
			copy.Config.Name = name
			_, err := s.Save(context.Background(), copy, 1)
			results <- err
		}(name)
	}
	group.Wait()
	close(results)
	success, conflicts := 0, 0
	for err := range results {
		if err == nil {
			success++
		} else if errors.Is(err, ErrExecutionPlanVersion) {
			conflicts++
		} else {
			t.Fatal(err)
		}
	}
	if success != 1 || conflicts != 1 {
		t.Fatalf("并发编辑结果 %d/%d", success, conflicts)
	}
}
