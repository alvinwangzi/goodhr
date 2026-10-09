// 本文件用真实 Windows 加密和本地预留验证 HRPlus 领取顺序、拒绝释放、原请求重试及名义结束边界。
package planrunner

import (
	"database/sql"
	"encoding/json"
	"errors"
	"goodhr5/local-agent-go/internal/cloudapi"
	"goodhr5/local-agent-go/internal/config"
	"goodhr5/local-agent-go/internal/localdb"
	"goodhr5/local-agent-go/internal/planmodel"
	"goodhr5/local-agent-go/internal/planoperations"
	"goodhr5/local-agent-go/internal/positionrunner"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

// acquireFixture 提供真实数据库、共享执行权和只返回虚构许可的隔离云端。
func acquireFixture(t *testing.T, mode *atomic.Int32) (*Coordinator, planmodel.Plan, cloudapi.PlanClaimRequest, planoperations.Authority, *atomic.Int32, *atomic.Int64) {
	t.Helper()
	cfg := &config.Config{DataDir: t.TempDir()}
	db, err := localdb.Open(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	raw, err := os.ReadFile(filepath.Join("..", "planmodel", "testdata", "permit.json"))
	if err != nil {
		t.Fatal(err)
	}
	var permit planmodel.Permit
	if err = json.Unmarshal(raw, &permit); err != nil {
		t.Fatal(err)
	}
	runner := positionrunner.New(db, nil, nil, cfg.DataDir, cfg.DataDir, cfg.DataDir, cfg.DataDir, "", 0)
	now := &atomic.Int64{}
	now.Store(time.Date(2026, 10, 10, 1, 0, 0, 0, time.UTC).UnixNano())
	claims := &atomic.Int32{}
	var scope string
	cloud := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/auth/me" {
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "user": map[string]any{"email": "fixture@example.com"}})
			return
		}
		if r.URL.Path == "/api/execution-plan-runs/"+permit.Run.ID+"/items/"+permit.Run.Items[0].ID+"/prepare" {
			var input cloudapi.PlanItemTaskRequest
			if e := json.NewDecoder(r.Body).Decode(&input); e != nil {
				t.Error(e)
			}
			saved, e := db.PlanOperation(t.Context(), scope, input.RequestID)
			if e != nil || saved.Kind != "prepare_item" || saved.RunID != permit.Run.ID {
				t.Error("准备任务前没有保存原请求", e)
			}
			if other, e := runner.ReservePlanBrowser(t.Context(), "90000000-0000-0000-0000-000000000001"); e == nil {
				_ = other.Release(true)
				t.Error("准备时没有保留父预留")
			}
			if mode.Load() == 2 {
				_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
				return
			}
			var result planmodel.Permit
			raw, _ := json.Marshal(permit)
			_ = json.Unmarshal(raw, &result)
			result.Run.Sequence++
			result.Run.Items[0].TaskRunID = "70000000-0000-0000-0000-000000000001"
			if mode.Load() == 3 {
				now.Store(time.Date(2026, 10, 10, 4, 0, 1, 0, time.UTC).UnixNano())
			}
			if mode.Load() == 5 {
				result.Run.ActivationID = "30000000-0000-0000-0000-000000000099"
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "permit": result})
			return
		}
		if r.URL.Path != "/api/execution-plan-runs/claim" {
			t.Error("领取访问了其他路径", r.URL.Path)
		}
		var request cloudapi.PlanClaimRequest
		if e := json.NewDecoder(r.Body).Decode(&request); e != nil {
			t.Error(e)
		}
		claims.Add(1)
		saved, e := db.PlanOperation(t.Context(), scope, request.RequestID)
		if e != nil || saved.RunID != request.RunID {
			t.Error("发送前没有保存原请求", e)
		}
		if other, e := runner.ReservePlanBrowser(t.Context(), "90000000-0000-0000-0000-000000000001"); e == nil {
			_ = other.Release(true)
			t.Error("发送时本地没有真正预留")
		}
		switch mode.Load() {
		case 1:
			w.WriteHeader(409)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": "账号忙", "error_code": "EXECUTION_BUSY"})
			return
		case 2:
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
			return
		case 3:
			now.Store(time.Date(2026, 10, 10, 4, 0, 1, 0, time.UTC).UnixNano())
		}
		result := permit
		if mode.Load() == 4 {
			result.Run.State = "draining"
			result.Owner.State = "releasing"
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "permit": result})
	}))
	t.Cleanup(cloud.Close)
	scope = cloudapi.SessionOwnerScope(cloud.URL, "fixture@example.com")
	created := time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC)
	plan := planmodel.Plan{ID: permit.Run.PlanID, UserEmail: "fixture@example.com", MachineID: permit.Owner.MachineID, Version: 1, StateSequence: 1, ActivationID: permit.Run.ActivationID, State: "enabled", Config: permit.Run.Snapshot, CreatedAt: created, UpdatedAt: created}
	input := cloudapi.PlanClaimRequest{PlanID: plan.ID, ActivationID: plan.ActivationID, ExpectedVersion: 1, ExecutionDate: permit.Run.ExecutionDate, RunID: permit.Run.ID, RequestID: "60000000-0000-0000-0000-000000000003", OwnerID: permit.Owner.OwnerID, MachineID: permit.Owner.MachineID, Credential: "fixture-acquire-secret-01234567890123456789", LocalReserved: true}
	a := planoperations.Authority{Token: "fixture-token", OwnerScope: scope, StillCurrent: func() bool { return true }}
	coordinator := New(db, runner, cloudapi.New(cloud.URL), func() time.Time { return time.Unix(0, now.Load()).UTC() })
	return coordinator, plan, input, a, claims, now
}

// TestAcquirePlanOrdering 验证领取前确有预留和原请求，只有匹配许可才保存确认并允许页面准备。
func TestAcquirePlanOrdering(t *testing.T) {
	mode := &atomic.Int32{}
	c, p, input, a, calls, clock := acquireFixture(t, mode)
	input.LocalReserved = false
	held, err := c.Acquire(t.Context(), p, input, a)
	if err != nil || !held.CanPrepare() || calls.Load() != 1 {
		t.Fatal("领取失败", err)
	}
	defer held.Reservation.Release(true)
	record, err := c.db.PlanOperation(t.Context(), a.OwnerScope, input.RequestID)
	if err != nil || record.State != "confirmed" {
		t.Fatal("匹配许可未确认原请求", err)
	}
	saved, err := c.db.PlanRunSnapshot(t.Context(), a.OwnerScope, input.RunID)
	if err != nil || saved.ID != held.Permit.Run.ID {
		t.Fatal("许可未保存原运行", err)
	}
	clock.Store(time.Date(2026, 10, 10, 4, 0, 0, 0, time.UTC).UnixNano())
	p.Config.Schedule.Windows[0].EndMinute = 780
	if held.CanPrepare() {
		t.Fatal("名义结束后仍允许新页面准备")
	}
}

// TestAcquirePlanDrainingReceipt 验证原许可回执指向收尾时只恢复结算事实，不重新执行页面。
func TestAcquirePlanDrainingReceipt(t *testing.T) {
	mode := &atomic.Int32{}
	mode.Store(4)
	c, p, input, a, _, _ := acquireFixture(t, mode)
	result, err := c.Acquire(t.Context(), p, input, a)
	if !errors.Is(err, ErrPlanNeedsSettlement) || result == nil || result.CanPrepare() || result.Permit.Run.State != "draining" {
		t.Fatal("收尾回执被当成新运行", err)
	}
	row, err := c.db.PlanOperation(t.Context(), a.OwnerScope, input.RequestID)
	if err != nil || row.State != "confirmed" {
		t.Fatal("原收尾事实丢失", err)
	}
}

// TestAcquirePlanRejectsBusyAndOutside 验证本地忙和时间段外不发送领取，云端忙保留原请求并释放本地。
func TestAcquirePlanRejectsBusyAndOutside(t *testing.T) {
	mode := &atomic.Int32{}
	c, p, input, a, calls, clock := acquireFixture(t, mode)
	prior, err := c.runner.ReservePlanBrowser(t.Context(), "90000000-0000-0000-0000-000000000001")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = c.Acquire(t.Context(), p, input, a); !errors.Is(err, positionrunner.ErrPlanBrowserBusy) || calls.Load() != 0 {
		t.Fatal("本地忙先申请了云端", err)
	}
	_ = prior.Release(true)
	clock.Store(time.Date(2026, 10, 10, 4, 0, 0, 0, time.UTC).UnixNano())
	if _, err = c.Acquire(t.Context(), p, input, a); !errors.Is(err, ErrOutsidePlanWindow) || calls.Load() != 0 {
		t.Fatal("名义结束仍申请云端", err)
	}
	if _, err = c.db.PlanOperation(t.Context(), a.OwnerScope, input.RequestID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("未预留就登记了可领取请求", err)
	}
	clock.Store(time.Date(2026, 10, 10, 1, 0, 0, 0, time.UTC).UnixNano())
	mode.Store(1)
	if _, err = c.Acquire(t.Context(), p, input, a); err == nil {
		t.Fatal("云端忙被当作许可")
	}
	next, err := c.runner.ReservePlanBrowser(t.Context(), "90000000-0000-0000-0000-000000000001")
	if err != nil {
		t.Fatal("被拒绝领取没有释放本地", err)
	}
	_ = next.Release(true)
	record, err := c.db.PlanOperation(t.Context(), a.OwnerScope, input.RequestID)
	if err != nil || record.State != "pending" {
		t.Fatal("被拒绝领取删除原请求", err)
	}
}

// TestAcquirePlanAmbiguousRetry 验证不明确结果不确认也不操作页面，重试使用同一原编号及密文。
func TestAcquirePlanAmbiguousRetry(t *testing.T) {
	mode := &atomic.Int32{}
	mode.Store(2)
	c, p, input, a, calls, _ := acquireFixture(t, mode)
	if _, err := c.Acquire(t.Context(), p, input, a); err == nil {
		t.Fatal("泛成功成为页面许可")
	}
	before, err := c.db.PlanOperation(t.Context(), a.OwnerScope, input.RequestID)
	if err != nil || before.State != "pending" {
		t.Fatal(err)
	}
	mode.Store(0)
	held, err := c.Acquire(t.Context(), p, input, a)
	if err != nil || !held.CanPrepare() || calls.Load() != 2 {
		t.Fatal("原请求不能核对", err)
	}
	defer held.Reservation.Release(true)
	after, err := c.db.PlanOperation(t.Context(), a.OwnerScope, input.RequestID)
	if err != nil || after.Sequence != before.Sequence || after.BodyHash != before.BodyHash {
		t.Fatal("重试改变原请求", err)
	}
}

// TestAcquirePlanLatePermit 验证许可到达时窗口已结束，仍记账原许可，但不给页面准备资格。
func TestAcquirePlanLatePermit(t *testing.T) {
	mode := &atomic.Int32{}
	mode.Store(3)
	c, p, input, a, _, _ := acquireFixture(t, mode)
	outcome, err := c.Acquire(t.Context(), p, input, a)
	if !errors.Is(err, ErrOutsidePlanWindow) || outcome == nil || outcome.Permit.Run.ID != input.RunID || outcome.CanPrepare() {
		t.Fatal("迟到许可开启新页面或丢失占用事实", err)
	}
	record, err := c.db.PlanOperation(t.Context(), a.OwnerScope, input.RequestID)
	if err != nil || record.State != "confirmed" {
		t.Fatal("已领取事实未保存", err)
	}
	next, err := c.runner.ReservePlanBrowser(t.Context(), "90000000-0000-0000-0000-000000000001")
	if err != nil {
		t.Fatal("未打开页面的迟到领取保留本地占用", err)
	}
	_ = next.Release(true)
}
