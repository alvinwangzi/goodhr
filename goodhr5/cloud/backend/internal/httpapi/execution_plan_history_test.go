// 本文件验证 HRPlus 页面恢复读取的原运行、独立执行项、账号隔离和读取不重启任务。
package httpapi

import (
	"errors"
	"testing"
)

// testPlanRunHistory 验证刷新只是读取原事实，不能改动快照或泄露其他账号的记录。
func testPlanRunHistory(t *testing.T, store ExecutionPlanStore) {
	t.Helper()
	email, _ := newExecutionPlanID()
	plan := createArmedPlanFixture(t, store, email+"@example.com")
	claim := planRunClaimFixture(t, plan)
	permit, err := store.ClaimRun(t.Context(), "", plan.UserEmail, claim)
	if err != nil {
		t.Fatal(err)
	}
	runs, err := store.ListRuns(t.Context(), "", plan.UserEmail, plan.ID)
	if err != nil || len(runs) != 1 || runs[0].ID != permit.Run.ID || runs[0].Sequence != permit.Run.Sequence || runs[0].State != "starting" {
		t.Fatal("页面恢复未读取原运行", runs, err)
	}
	if len(runs[0].Items) != len(plan.Config.Items) || runs[0].Items[0].ID == runs[0].Items[1].ID {
		t.Fatal("同岗位执行项被合并")
	}
	runs[0].Snapshot.Items[0].Actions[0] = "changed"
	again, err := store.ListRuns(t.Context(), "", plan.UserEmail, plan.ID)
	if err != nil || len(again) != 1 || again[0].Sequence != permit.Run.Sequence || again[0].Snapshot.Items[0].Actions[0] == "changed" {
		t.Fatal("读取修改原运行或快照", err)
	}
	for _, scope := range [][2]string{{"", "another@example.com"}, {"another-team", plan.UserEmail}} {
		if _, err := store.ListRuns(t.Context(), scope[0], scope[1], plan.ID); !errors.Is(err, ErrNotFound) {
			t.Fatal("历史读取跨账号或跨团队", scope, err)
		}
	}
}

// TestPlanRunHistoryStores 对内存和独立 PostgreSQL 执行相同恢复契约。
func TestPlanRunHistoryStores(t *testing.T) {
	t.Run("memory", func(t *testing.T) { testPlanRunHistory(t, NewMemoryExecutionPlanStore()) })
	t.Run("postgres", func(t *testing.T) { testPlanRunHistory(t, NewPostgresExecutionPlanStore(planPostgresFixture(t))) })
}
