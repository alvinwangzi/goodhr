// 本文件验证 HRPlus 原日志批次事务、内容幂等、分页和账号隔离，不运行招聘页面。
package httpapi

import (
	"errors"
	"testing"
	"time"
)

// testItemLogStore 创建真实独立 TaskRun，日志归属不能用任意构造任务编号代替。
func testItemLogStore(t *testing.T, store ExecutionPlanStore) {
	t.Helper()
	unique, _ := newExecutionPlanID()
	email := unique + "@example.com"
	tenant := ""
	var position Position
	var err error
	switch source := store.(type) {
	case *MemoryExecutionPlanStore:
		source.positions = NewMemoryPositionStore()
		source.taskRuns = NewMemoryTaskRunStore()
		position, err = source.positions.SavePosition(Position{UserEmail: email, Name: "夹具岗位", PlatformID: "boss"})
	case *PostgresExecutionPlanStore:
		value, tenantErr := NewPostgresTenantStore(source.db).GetOrCreateTenant(email)
		if tenantErr != nil {
			t.Fatal(tenantErr)
		}
		tenant = value.ID
		position, err = NewPostgresPositionStore(source.db).SavePosition(Position{UserEmail: email, Name: "夹具岗位", PlatformID: "boss"})
	}
	if err != nil {
		t.Fatal(err)
	}
	config := postgresPlanConfig()
	for index := range config.Items {
		config.Items[index].PositionID = position.ID
	}
	plan, err := store.Save(t.Context(), ExecutionPlan{TenantID: tenant, UserEmail: email, MachineID: "A", Config: config}, 0)
	if err != nil {
		t.Fatal(err)
	}
	plan, err = store.Intent(t.Context(), tenant, email, plan.ID, planIntentFixture(t, "arm"))
	if err != nil {
		t.Fatal(err)
	}
	claim := planRunClaimFixture(t, plan)
	permit, err := store.ClaimRun(t.Context(), tenant, email, claim)
	if err != nil {
		t.Fatal(err)
	}
	permit, err = store.PrepareItemTask(t.Context(), tenant, email, itemTaskRequestFixture(t, claim, permit.Run.Items[0].ID))
	if err != nil {
		t.Fatal(err)
	}
	item := permit.Run.Items[0]
	first := ExecutionPlanItemLog{ID: 1, PlanRunID: permit.Run.ID, ItemRunID: item.ID, TaskRunID: item.TaskRunID, LocalRunID: item.ID, PositionID: position.ID, Level: "info", Message: "原候选人处理日志", CreatedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	second := first
	second.ID = 2
	second.Message = "下一条原日志"
	if err := store.AppendItemLogs(t.Context(), tenant, email, permit.Run.ID, item.ID, "A", []ExecutionPlanItemLog{first, second}); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendItemLogs(t.Context(), tenant, email, permit.Run.ID, item.ID, "A", []ExecutionPlanItemLog{first, second}); err != nil {
		t.Fatal("原内容不能重试", err)
	}
	changed := second
	changed.Message = "修改旧编号的内容"
	third := first
	third.ID = 3
	if err := store.AppendItemLogs(t.Context(), tenant, email, permit.Run.ID, item.ID, "A", []ExecutionPlanItemLog{third, changed}); !errors.Is(err, ErrExecutionPlanRequest) {
		t.Fatal("修改原日志仍被接受", err)
	}
	logs, err := store.ListItemLogs(t.Context(), tenant, email, permit.Run.ID, item.ID, 0, 200)
	if err != nil || len(logs) != 2 || logs[0].Message != second.Message || logs[0].CreatedAt != second.CreatedAt {
		t.Fatal("重试重复、事务未回滚或原时间变化", logs, err)
	}
	page, err := store.ListItemLogs(t.Context(), tenant, email, permit.Run.ID, item.ID, logs[0].ID, 200)
	if err != nil || len(page) != 1 {
		t.Fatal("分页重复或漏读", page, err)
	}
	wrong := first
	wrong.TaskRunID = permit.Run.ID
	if err := store.AppendItemLogs(t.Context(), tenant, email, permit.Run.ID, item.ID, "A", []ExecutionPlanItemLog{wrong}); !errors.Is(err, ErrExecutionPlanRequest) {
		t.Fatal("另一任务混入原项", err)
	}
	if err := store.AppendItemLogs(t.Context(), tenant, email, permit.Run.ID, item.ID, "B", []ExecutionPlanItemLog{first}); !errors.Is(err, ErrAccountExecutionProof) {
		t.Fatal("B 电脑冒领原日志", err)
	}
	for _, scope := range [][2]string{{"", "other@example.com"}, {"other-team", email}} {
		if _, err := store.ListItemLogs(t.Context(), scope[0], scope[1], permit.Run.ID, item.ID, 0, 100); !errors.Is(err, ErrNotFound) {
			t.Fatal("原日志跨账号或团队读取", err)
		}
	}
}

// TestPlanItemLogStores 对内存和真实 PostgreSQL 执行相同原内容事务与读取契约。
func TestPlanItemLogStores(t *testing.T) {
	t.Run("memory", func(t *testing.T) { testItemLogStore(t, NewMemoryExecutionPlanStore()) })
	t.Run("postgres", func(t *testing.T) { testItemLogStore(t, NewPostgresExecutionPlanStore(planPostgresFixture(t))) })
}
