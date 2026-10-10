// 本文件使用真实 Windows DPAPI、SQLite 重开与隔离 HTTP 验证 HRPlus 原停止确认，禁止真实页面和邮件操作。
package planoperations

import (
	"encoding/json"
	"goodhr5/local-agent-go/internal/cloudapi"
	"goodhr5/local-agent-go/internal/localdb"
	"goodhr5/local-agent-go/internal/planmodel"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync/atomic"
	"testing"
	"time"
)

// TestProtectedStopRestart 验证不明确结果保留原请求，数据库重开后相同编号重试并原子确认。
func TestProtectedStopRestart(t *testing.T) {
	db, cfg, permit, _ := operationFixture(t)
	created := time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC)
	p := planmodel.Plan{ID: permit.Run.PlanID, TenantID: "fixture-team", UserEmail: "fixture@example.com", MachineID: permit.Owner.MachineID, Version: 1, StateSequence: 2, ActivationID: permit.Run.ActivationID, State: "stopped", StopRequested: true, Config: permit.Run.Snapshot, CreatedAt: created, UpdatedAt: created}
	mode, calls := &atomic.Int32{}, &atomic.Int32{}
	current := &atomic.Bool{}
	current.Store(true)
	mode.Store(1)
	var scope string
	var first cloudapi.PlanStopRequest
	cloud := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/auth/me" {
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "user": map[string]any{"email": p.UserEmail, "tenant_id": p.TenantID}})
			return
		}
		if r.URL.Path != "/api/execution-plans/"+p.ID+"/confirm-stop" {
			t.Error("访问了其他接口", r.URL.Path)
		}
		var input cloudapi.PlanStopRequest
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			t.Error(err)
		}
		if o, err := db.NextPlanStop(t.Context(), scope); err != nil || o.RequestID != input.RequestID {
			t.Error("发送前没有原密文", err)
		}
		if calls.Add(1) == 1 {
			first = input
		} else if input != first {
			t.Error("重试改变原停止请求")
		}
		if mode.Load() == 1 {
			w.WriteHeader(503)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": "fixture unavailable"})
			return
		}
		result := p
		result.StopRequested = false
		result.StateSequence++
		if mode.Load() == 2 {
			result.MachineID = "other-machine"
		}
		if mode.Load() == 3 {
			current.Store(false)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "plan": result})
	}))
	defer cloud.Close()
	scope = cloudapi.SessionOwnerScope(cloud.URL, p.UserEmail)
	store := New(db)
	o, err := store.StageStop(t.Context(), cloud.URL, scope, p)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.EnqueuePlanRequest(t.Context(), localdb.PlanWaitingRequest{OwnerScope: scope, RequestID: "original-wait", PlanID: p.ID, ActivationID: p.ActivationID, TriggeredAt: created}); err != nil {
		t.Fatal(err)
	}
	a := Authority{Token: "fixture-stop-token", OwnerScope: scope, StillCurrent: current.Load}
	if sent, err := store.UploadNextStop(t.Context(), cloudapi.New(cloud.URL), a); err == nil || sent {
		t.Fatal("不明确回执仍确认停止")
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = localdb.Open(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store = New(db)
	if retry, err := store.StageStop(t.Context(), cloud.URL, scope, p); err != nil || retry.RequestID != o.RequestID || retry.BodyHash != o.BodyHash || !reflect.DeepEqual(retry.Cipher, o.Cipher) {
		t.Fatal("重开重新登记确认", err)
	}
	mode.Store(2)
	if sent, err := store.UploadNextStop(t.Context(), cloudapi.New(cloud.URL), a); err == nil || sent {
		t.Fatal("其他设备回执被确认")
	}
	current.Store(false)
	if sent, err := store.UploadNextStop(t.Context(), cloudapi.New(cloud.URL), a); err == nil || sent || calls.Load() != 2 {
		t.Fatal("旧授权发送停止确认")
	}
	current.Store(true)
	mode.Store(3)
	if sent, err := store.UploadNextStop(t.Context(), cloudapi.New(cloud.URL), a); err == nil || sent {
		t.Fatal("授权变化仍确认迟到回执")
	}
	if record, err := db.PlanStopOperation(t.Context(), scope, o.RequestID); err != nil || record.State != "pending" {
		t.Fatal("迟到回执覆盖原请求", err)
	}
	current.Store(true)
	mode.Store(0)
	if sent, err := store.UploadNextStop(t.Context(), cloudapi.New(cloud.URL), a); err != nil || !sent {
		t.Fatal("原停止未确认", err)
	}
	if record, err := db.PlanStopOperation(t.Context(), scope, o.RequestID); err != nil || record.State != "confirmed" {
		t.Fatal("确认标记丢失", err)
	}
	if cached, err := db.CachedPlans(t.Context(), scope); err != nil || len(cached) != 1 || cached[0].StopRequested {
		t.Fatal("计划快照未确认停止", err)
	}
	if waiting, err := db.WaitingPlanRequests(t.Context(), scope); err != nil || len(waiting) != 0 {
		t.Fatal("原批次等待项没有取消", err)
	}
}

// TestStopBlockedByOriginalRun 验证本地原运行未结算时不能声明已经完成停止清理。
func TestStopBlockedByOriginalRun(t *testing.T) {
	db, _, permit, _ := operationFixture(t)
	defer db.Close()
	base := "http://fixture.invalid"
	scope := cloudapi.SessionOwnerScope(base, "fixture@example.com")
	if err := db.SavePlanRunSnapshot(t.Context(), scope, permit.Run); err != nil {
		t.Fatal(err)
	}
	date := time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC)
	p := planmodel.Plan{ID: permit.Run.PlanID, UserEmail: "fixture@example.com", MachineID: permit.Owner.MachineID, Version: 1, StateSequence: 2, ActivationID: permit.Run.ActivationID, State: "stopped", StopRequested: true, Config: permit.Run.Snapshot, CreatedAt: date, UpdatedAt: date}
	if _, err := New(db).StageStop(t.Context(), base, scope, p); err == nil {
		t.Fatal("原 starting 运行被当作已释放")
	}
}
