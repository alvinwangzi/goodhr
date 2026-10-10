// 本文件用 Windows DPAPI、SQLite 重开和隔离云端验证 HRPlus 原清理事实恢复，不重新领取页面执行权。
package planoperations

import (
	"encoding/json"
	"goodhr5/local-agent-go/internal/cloudapi"
	"goodhr5/local-agent-go/internal/localdb"
	"goodhr5/local-agent-go/internal/planmodel"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

// TestCleanupIntentRestart 验证未知任务回执后真实数据库重开、原关联核对及原释放顺序。
func TestCleanupIntentRestart(t *testing.T) {
	db, cfg, p, claim := operationFixture(t)
	wrong := &atomic.Bool{}
	posts := &atomic.Int32{}
	actual := p.Run
	actual.Sequence++
	actual.Items[0].TaskRunID = "70000000-0000-0000-0000-000000000001"
	cloud := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/auth/me":
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "user": map[string]any{"email": "fixture@example.com"}})
		case "/api/execution-plan-runs/" + p.Run.ID:
			if r.Method != http.MethodGet {
				t.Error("核对必须只读")
			}
			copy := actual
			if wrong.Load() {
				copy.OwnerID = "40000000-0000-0000-0000-000000000099"
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "run": copy})
		case "/api/execution-plan-runs/" + p.Run.ID + "/release":
			posts.Add(1)
			var input cloudapi.PlanRunUpdateRequest
			if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
				t.Error(err)
			}
			if input.Credential != claim.Credential || input.OwnerID != claim.OwnerID || input.Sequence != actual.Sequence+1 {
				t.Error("释放改写原占用或猜测序号")
			}
			result := actual
			result.Sequence = input.Sequence
			result.State = input.State
			for i, item := range input.Items {
				result.Items[i].State = item.State
				result.Items[i].Actions = item.Actions
				if item.State == "pending" || item.State == "running" {
					result.Items[i].State = "stopped"
				}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "permit": planmodel.Permit{Run: result, Owner: planmodel.Owner{OwnerID: claim.OwnerID, OwnerType: "plan", MachineID: claim.MachineID, State: "released"}}})
		default:
			t.Error("清理恢复访问了领取或其他接口", r.URL.Path)
			w.WriteHeader(500)
		}
	}))
	defer cloud.Close()
	scope := cloudapi.SessionOwnerScope(cloud.URL, "fixture@example.com")
	store := New(db)
	if _, err := store.StageClaim(t.Context(), scope, claim); err != nil {
		t.Fatal(err)
	}
	if err := db.SavePlanRunSnapshot(t.Context(), scope, p.Run); err != nil {
		t.Fatal(err)
	}
	intent := CleanupIntent{Run: p.Run, ClaimRequestID: claim.RequestID, State: "blocked", Reason: "fixture lost preparation", CleanupConfirmed: true}
	o, err := store.StageCleanup(t.Context(), scope, intent)
	if err != nil {
		t.Fatal(err)
	}
	changed := intent
	changed.Reason = "changed"
	if _, err := store.StageCleanup(t.Context(), scope, changed); err == nil {
		t.Fatal("同一意图可改写事实")
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
	original, err := store.OriginalCleanup(t.Context(), o)
	if err != nil || original.Run.ID != p.Run.ID {
		t.Fatal("重开丢失原清理", err)
	}
	a := Authority{Token: "fixture-restored", OwnerScope: scope, StillCurrent: func() bool { return true }}
	wrong.Store(true)
	if changed, err := store.ResolveNextCleanup(t.Context(), cloudapi.New(cloud.URL), a); err == nil || changed || posts.Load() != 0 {
		t.Fatal("其他占用获得释放", err)
	}
	wrong.Store(false)
	if changed, err := store.ResolveNextCleanup(t.Context(), cloudapi.New(cloud.URL), a); err != nil || !changed {
		t.Fatal("原关联无法恢复", err)
	}
	release, err := db.NextPlanUpdate(t.Context(), scope)
	if err != nil || release.RequestID != original.ReleaseRequestID {
		t.Fatal("未登记原释放编号", err)
	}
	if changed, err := store.ResolveNextCleanup(t.Context(), cloudapi.New(cloud.URL), a); err != nil || changed {
		t.Fatal("释放未确认就完成意图", err)
	}
	if sent, err := store.UploadNext(t.Context(), cloudapi.New(cloud.URL), a); err != nil || !sent {
		t.Fatal("原释放无法确认", err)
	}
	if changed, err := store.ResolveNextCleanup(t.Context(), cloudapi.New(cloud.URL), a); err != nil || !changed {
		t.Fatal("已确认释放未完成意图", err)
	}
	if record, err := db.PlanCleanupOperation(t.Context(), scope, o.RequestID); err != nil || record.State != "confirmed" || posts.Load() != 1 {
		t.Fatal("意图重复释放或丢失确认", err)
	}
}
