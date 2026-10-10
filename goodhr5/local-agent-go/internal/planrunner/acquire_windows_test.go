// 本文件用真实 Windows 加密和本地预留验证 HRPlus 领取顺序、拒绝释放、原请求重试及名义结束边界。
package planrunner

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
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
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// acquireFixture 提供真实数据库、共享执行权和只返回虚构许可的隔离云端。
func acquireFixture(t *testing.T, mode *atomic.Int32, configure ...func(*planmodel.Permit)) (*Coordinator, planmodel.Plan, cloudapi.PlanClaimRequest, planoperations.Authority, *atomic.Int32, *atomic.Int64) {
	return acquireFixtureWithWorker(t, mode, nil, configure...)
}

// acquireFixtureWithWorker 注入受控页面 Worker，真实 M1 适配联调仍使用同一数据库和预留对象。
func acquireFixtureWithWorker(t *testing.T, mode *atomic.Int32, worker positionrunner.BrowserWorker, configure ...func(*planmodel.Permit)) (*Coordinator, planmodel.Plan, cloudapi.PlanClaimRequest, planoperations.Authority, *atomic.Int32, *atomic.Int64) {
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
	for _, apply := range configure {
		apply(&permit)
	}
	runner := positionrunner.New(db, worker, nil, cfg.DataDir, cfg.DataDir, cfg.DataDir, cfg.DataDir, "", 0)
	now := &atomic.Int64{}
	now.Store(time.Date(2026, 10, 10, 1, 0, 0, 0, time.UTC).UnixNano())
	claims := &atomic.Int32{}
	var scope string
	cloud := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/auth/me" {
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "user": map[string]any{"email": "fixture@example.com"}})
			return
		}
		var configuration any
		switch r.URL.Path {
		case "/api/positions/same-job":
			configuration = map[string]any{"position": map[string]any{"id": "same-job", "name": "fixture", "platform_id": "boss", "match_limit": 1, "keywords": []string{"本科"}, "common_config": map[string]any{"mode_default": "keyword", "detail_mode": "keyword"}}}
		case "/api/subscription/status":
			configuration = map[string]any{"subscription": map[string]any{"active": true}}
		case "/api/config/user-preferences":
			configuration = map[string]any{"config": map[string]any{}}
		case "/api/platforms/config/":
			configuration = map[string]any{"configs": []map[string]any{{"config_key": "platform.boss", "config_value": `{"id":"boss","auth":{"pages":[{"url":"https://www.zhipin.com/web/chat/recommend","entry":true}]}}`}}}
		}
		if configuration != nil {
			if r.URL.Path == "/api/platforms/config/" {
				body, _ := json.Marshal(map[string]any{"id": "boss", "auth": map[string]any{"pages": []any{map[string]any{"url": "https://www.zhipin.com/web/chat/recommend", "entry": true}}}, "position": map[string]any{"current": map[string]any{"selector": ".fixture-job"}}})
				configuration = map[string]any{"configs": []map[string]any{{"config_key": "platform.boss", "config_value": string(body)}}}
			}
			_ = json.NewEncoder(w).Encode(configuration)
			return
		}
		if r.URL.Path == "/api/execution-plan-runs/"+permit.Run.ID+"/status" {
			var input cloudapi.PlanRunUpdateRequest
			if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
				t.Error(err)
			}
			operation, err := db.PlanOperation(t.Context(), scope, input.RequestID)
			if err != nil || operation.Kind != "status" || operation.RunSequence != input.Sequence {
				t.Error("状态发送前没有原请求", err)
			}
			if mode.Load() == 2 {
				_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
				return
			}
			current, err := db.PlanRunSnapshot(t.Context(), scope, permit.Run.ID)
			if err != nil {
				t.Error(err)
				w.WriteHeader(500)
				return
			}
			current.Sequence, current.State, current.CurrentItem = input.Sequence, input.State, input.CurrentItem
			if current.StartedAt == nil && input.State == "running" {
				started := time.Date(2026, 10, 10, 1, 0, 7, 0, time.UTC)
				current.StartedAt = &started
			}
			for i, item := range input.Items {
				current.Items[i].State, current.Items[i].Actions = item.State, item.Actions
			}
			owner := permit.Owner
			owner.State = "running"
			if input.State == "draining" {
				owner.State = "releasing"
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "permit": planmodel.Permit{Run: current, Owner: owner}})
			return
		}
		if r.URL.Path == "/api/execution-plan-runs/"+permit.Run.ID+"/release" {
			var input cloudapi.PlanRunUpdateRequest
			if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
				t.Error(err)
			}
			current, err := db.PlanRunSnapshot(t.Context(), scope, permit.Run.ID)
			if err != nil {
				t.Error(err)
				w.WriteHeader(500)
				return
			}
			current.Sequence, current.State, current.CurrentItem = input.Sequence, input.State, input.CurrentItem
			for i, item := range input.Items {
				current.Items[i].State, current.Items[i].Actions = item.State, item.Actions
			}
			finished := time.Date(2026, 10, 10, 1, 1, 0, 0, time.UTC)
			if input.State != "waiting_window" && input.State != "completed" {
				for i := range current.Items {
					if current.Items[i].State == "pending" || current.Items[i].State == "running" {
						current.Items[i].State = "stopped"
					}
					for action, progress := range current.Items[i].Actions {
						if progress.State == "active" {
							progress.State = "stopped"
							current.Items[i].Actions[action] = progress
						}
					}
				}
			}
			current.FinishedAt = &finished
			if input.State == "waiting_window" {
				current.FinishedAt = nil
			}
			owner := permit.Owner
			owner.State = "released"
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "permit": planmodel.Permit{Run: current, Owner: owner}})
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/positions/") && !strings.HasSuffix(r.URL.Path, "/status") {
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
			return
		}
		prepareIndex := -1
		for i, item := range permit.Run.Items {
			if r.URL.Path == "/api/execution-plan-runs/"+permit.Run.ID+"/items/"+item.ID+"/prepare" {
				prepareIndex = i
			}
		}
		if prepareIndex >= 0 {
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
			current, e := db.PlanRunSnapshot(t.Context(), scope, permit.Run.ID)
			if e != nil {
				t.Error(e)
				w.WriteHeader(500)
				return
			}
			result := planmodel.Permit{Run: current, Owner: permit.Owner}
			if current.State == "running" {
				result.Owner.State = "running"
			}
			if result.Run.Items[prepareIndex].TaskRunID == "" {
				result.Run.Sequence++
				result.Run.Items[prepareIndex].TaskRunID = fmt.Sprintf("70000000-0000-0000-0000-%012d", prepareIndex+1)
			}
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
		// 新调度请求使用不同占用编号，受控许可必须返回该原占用身份。
		if current, e := db.PlanRunSnapshot(t.Context(), scope, request.RunID); e == nil && current.State == "waiting_window" && current.OwnerID != request.OwnerID {
			permit.Run = current
			permit.Run.Sequence++
			permit.Run.State = "starting"
		}
		permit.Run.OwnerID, permit.Owner.OwnerID = request.OwnerID, request.OwnerID
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
