// 本文件验证 HRPlus 原报告的归属、重复提交、摘要不可变和真实 PostgreSQL 行锁保存，不发送邮件。
package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// endedReportFixture 创建实际停止后的独立执行项摘要，报告不修改计划启用状态。
func endedReportFixture(t *testing.T, store ExecutionPlanStore, email string, machine ...string) (ExecutionPlan, ExecutionPlanReportSummary) {
	t.Helper()
	device := "A"
	if len(machine) > 0 {
		device = machine[0]
	}
	return endedReportFixtureOwned(t, store, email, device, "")
}

// endedReportFixtureOwned 从原团队的真实领取、释放结果构建原摘要，供跨模块 SDK 联调。
func endedReportFixtureOwned(t *testing.T, store ExecutionPlanStore, email, device, tenant string) (ExecutionPlan, ExecutionPlanReportSummary) {
	t.Helper()
	p := createArmedPlanFixtureOwned(t, store, email, device, tenant)
	claim := planRunClaimFixture(t, p)
	permit, err := store.ClaimRun(t.Context(), tenant, email, claim)
	if err != nil {
		t.Fatal(err)
	}
	release := planRunUpdateFixture(t, claim, permit.Run.Sequence+1, "release", "incomplete")
	permit, err = store.UpdateRun(t.Context(), tenant, email, release)
	if err != nil {
		t.Fatal(err)
	}
	run := permit.Run
	r := ExecutionPlanReportSummary{SchemaVersion: 1, RunID: run.ID, PlanID: run.PlanID, ActivationID: run.ActivationID, ConfigVersion: run.ConfigVersion, RunSequence: run.Sequence, ExecutionDate: run.ExecutionDate, PlanName: run.Snapshot.Name, Kind: "day_incomplete", RunState: run.State, EndReason: run.EndReason, SyncState: "confirmed", GeneratedAt: time.Now().UTC(), FinishedAt: run.FinishedAt, Items: []ExecutionReportItem{}, UnfinishedItemIDs: []string{}}
	for _, item := range run.Items {
		row := ExecutionReportItem{ID: item.ID, ItemID: item.ItemID, TaskRunID: item.TaskRunID, PositionID: item.Snapshot.PositionID, Order: item.Order, State: item.State, DetailsAvailable: true, Actions: map[string]ExecutionReportAction{}, Information: map[string]ExecutionReportAction{}}
		for action, progress := range item.Actions {
			row.Actions[action] = ExecutionReportAction{State: progress.State, Confirmed: progress.Count, Unknown: progress.UnknownCount}
		}
		r.Items = append(r.Items, row)
		r.UnfinishedItemIDs = append(r.UnfinishedItemIDs, item.ID)
	}
	return p, r
}

// testReportContract 验证重复提交、账号隔离和旧原摘要保护，内存与 PostgreSQL 共用契约。
func testReportContract(t *testing.T, store ExecutionPlanStore) {
	t.Helper()
	email, _ := newExecutionPlanID()
	email += "@fixture.test"
	p, summary := endedReportFixture(t, store, email)
	first, err := store.SaveReport(t.Context(), "", email, summary, "pending")
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.SaveReport(t.Context(), "", email, summary, "confirmed")
	if err != nil || second.BodyHash != first.BodyHash || !second.CreatedAt.Equal(first.CreatedAt) || second.NotificationState != "pending" {
		t.Fatal("重复上报改写原摘要或通知", err)
	}
	if late, err := store.SaveReport(t.Context(), "", email, summary, "pending"); err != nil || late.SyncState != "confirmed" {
		t.Fatal("旧同步状态覆盖已确认状态", err)
	}
	changed := summary
	changed.PlanName = "修改后的摘要"
	if _, err := store.SaveReport(t.Context(), "", email, changed, "confirmed"); !errors.Is(err, ErrExecutionPlanRequest) {
		t.Fatal("原报告被替换", err)
	}
	if _, err := store.GetReport(t.Context(), "", "other@fixture.test", summary.RunID); !errors.Is(err, ErrNotFound) {
		t.Fatal("其他账号读取报告", err)
	}
	if _, err := store.GetReport(t.Context(), "other-team", email, summary.RunID); !errors.Is(err, ErrNotFound) {
		t.Fatal("其他团队读取报告", err)
	}
	current, err := store.Get(t.Context(), "", email, p.ID)
	if err != nil || current.State != "enabled" {
		t.Fatal("报告停用周期计划", err)
	}
	read, err := store.GetReport(t.Context(), "", email, summary.RunID)
	if err != nil || read.SyncState != "confirmed" || read.Summary.PlanName != summary.PlanName {
		t.Fatal("原报告读取失败", err)
	}
}

// TestMemoryOriginalReport 验证内存报告和标准契约，错误执行项或负数量不得写入。
func TestMemoryOriginalReport(t *testing.T) {
	store := NewMemoryExecutionPlanStore()
	testReportContract(t, store)
	p, summary := endedReportFixture(t, store, "bad@fixture.test")
	wrong := cloneExecutionReport(ExecutionPlanReport{Summary: summary}).Summary
	wrong.Items[0].PositionID = wrong.Items[0].PositionID + "wrong"
	if _, err := store.SaveReport(t.Context(), "", p.UserEmail, wrong, "confirmed"); err == nil {
		t.Fatal("错岗位报告接受")
	}
	wrong = cloneExecutionReport(ExecutionPlanReport{Summary: summary}).Summary
	for action, value := range wrong.Items[0].Actions {
		value.Confirmed = -1
		wrong.Items[0].Actions[action] = value
		break
	}
	if _, err := store.SaveReport(t.Context(), "", p.UserEmail, wrong, "confirmed"); err == nil {
		t.Fatal("负数量报告接受")
	}
}

// TestPostgresOriginalReport 验证实际迁移与报告契约。
func TestPostgresOriginalReport(t *testing.T) {
	testReportContract(t, NewPostgresExecutionPlanStore(planPostgresFixture(t)))
}

// TestPostgresReportConcurrent 验证相同运行并发上传仍保存一份原报告。
func TestPostgresReportConcurrent(t *testing.T) {
	db := planPostgresFixture(t)
	store := NewPostgresExecutionPlanStore(db)
	email, _ := newExecutionPlanID()
	email += "@fixture.test"
	_, summary := endedReportFixture(t, store, email)
	var group sync.WaitGroup
	errs := make(chan error, 4)
	for i := 0; i < 4; i++ {
		group.Add(1)
		go func() {
			defer group.Done()
			_, err := store.SaveReport(context.Background(), "", email, summary, "confirmed")
			errs <- err
		}()
	}
	group.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM execution_plan_reports WHERE run_id=$1`, summary.RunID).Scan(&count); err != nil || count != 1 {
		t.Fatal("并发报告重复", err, count)
	}
}

// TestOriginalReportHTTP 验证实际报告路由的指定设备上传和其他账号读取边界。
func TestOriginalReportHTTP(t *testing.T) {
	server := mustNewServer(t)
	routes := server.Routes()
	email := "report-api@example.com"
	token := loginForTest(t, routes, email)
	bindPositionDeviceForTest(t, routes, token)
	store := server.executionPlans.store.(*MemoryExecutionPlanStore)
	p, summary := endedReportFixture(t, store, email, positionTestMachineID)
	if server.positions.auth.tenantStore != nil {
		tenant, err := server.positions.auth.tenantStore.GetOrCreateTenant(email)
		if err != nil {
			t.Fatal(err)
		}
		p.TenantID = tenant.ID
	}
	p.MachineID = positionTestMachineID
	store.mu.Lock()
	store.plans[p.ID] = p
	store.mu.Unlock()
	post := func(machine string) *httptest.ResponseRecorder {
		body, _ := json.Marshal(map[string]any{"summary": summary, "sync_state": "confirmed", "machine_id": machine})
		request := httptest.NewRequest(http.MethodPost, "/api/execution-plan-runs/"+summary.RunID+"/report", bytes.NewReader(body))
		request.Header.Set("Authorization", "Bearer "+token)
		result := httptest.NewRecorder()
		routes.ServeHTTP(result, request)
		return result
	}
	if result := post("other-machine"); result.Code != 403 {
		t.Fatal("其他电脑上传报告", result.Code)
	}
	if result := post(positionTestMachineID); result.Code != 200 {
		t.Fatal("原报告上传失败", result.Code, result.Body.String())
	}
	stopped := stopReportPlanFixture(t, store, p)
	otherMachine := "goodhr-device-v1-report-history-B"
	if _, err := server.executionPlans.agents.SaveBinding(AgentBinding{UserEmail: email, MachineID: otherMachine, BindStatus: "active"}); err != nil {
		t.Fatal(err)
	}
	stopped.MachineID = otherMachine
	changed, err := store.Save(t.Context(), stopped, stopped.Version)
	if err != nil {
		t.Fatal(err)
	}
	if result := post(otherMachine); result.Code != 403 {
		t.Fatal("绑定的当前 B 电脑冒领 A 的报告", result.Code, result.Body.String())
	}
	if result := post(positionTestMachineID); result.Code != 200 {
		t.Fatal("改到 B 后 A 不能补传历史报告", result.Code, result.Body.String())
	}
	if err := store.Delete(t.Context(), p.TenantID, email, p.ID, changed.Version); err != nil {
		t.Fatal(err)
	}
	if result := post(positionTestMachineID); result.Code != 200 {
		t.Fatal("软删除后 A 不能补传历史报告", result.Code, result.Body.String())
	}
	other := loginForTest(t, routes, "other-report-api@example.com")
	request := httptest.NewRequest(http.MethodGet, "/api/execution-plan-runs/"+summary.RunID+"/report", nil)
	request.Header.Set("Authorization", "Bearer "+other)
	result := httptest.NewRecorder()
	routes.ServeHTTP(result, request)
	if result.Code != 404 {
		t.Fatal("其他账号读取原报告", result.Code)
	}
}
