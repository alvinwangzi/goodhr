// 本文件用隔离 HTTP 和真实 SQLite 重开验证 HRPlus 原报告补传与授权变更，不发送邮件。
package planoperations

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"goodhr5/local-agent-go/internal/cloudapi"
	"goodhr5/local-agent-go/internal/localdb"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// TestOriginalReportUploadRestart 验证报告先保存、相同内容重试、错误回执和登录变化不能确认。
func TestOriginalReportUploadRestart(t *testing.T) {
	db, cfg, permit, _ := operationFixture(t)
	run := permit.Run
	run.State = "incomplete"
	run.Items[0].State = "stopped"
	run.Items[1].State = "stopped"
	mode, calls := &atomic.Int32{}, &atomic.Int32{}
	mode.Store(1)
	current := &atomic.Bool{}
	current.Store(true)
	var scope string
	var original []byte
	cloud := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/auth/me" {
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "user": map[string]any{"email": "fixture@example.com"}})
			return
		}
		if r.URL.Path != "/api/execution-plan-runs/"+run.ID+"/report" {
			t.Error("报告访问其他接口", r.URL.Path)
		}
		var input struct {
			Summary   json.RawMessage `json:"summary"`
			SyncState string          `json:"sync_state"`
			MachineID string          `json:"machine_id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			t.Error(err)
		}
		var summary cloudapi.PlanReportReceipt
		if err := json.Unmarshal(input.Summary, &summary.Summary); err != nil {
			t.Error(err)
		}
		raw, _ := json.Marshal(summary.Summary)
		hash := sha256.Sum256(raw)
		if calls.Add(1) == 1 {
			original = raw
		} else if string(original) != string(raw) {
			t.Error("重试改写原摘要")
		}
		if record, err := db.PlanReportSnapshot(t.Context(), scope, run.ID); err != nil || record.BodyHash != hex.EncodeToString(hash[:]) {
			t.Error("发送前没有保存原报告", err)
		}
		if mode.Load() == 1 {
			w.WriteHeader(503)
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": false})
			return
		}
		now := time.Now().UTC()
		summary.RunID = run.ID
		summary.BodyHash = hex.EncodeToString(hash[:])
		summary.SyncState = input.SyncState
		summary.NotificationState = "pending"
		summary.CreatedAt = now
		summary.UpdatedAt = now
		if mode.Load() == 2 {
			summary.RunID = "20000000-0000-0000-0000-000000000099"
		}
		if mode.Load() == 3 {
			current.Store(false)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "report": summary})
	}))
	defer cloud.Close()
	scope = cloudapi.SessionOwnerScope(cloud.URL, "fixture@example.com")
	report, err := db.BuildPlanReport(t.Context(), scope, run, time.Now(), true)
	if err != nil {
		t.Fatal(err)
	}
	report.SyncState = "pending"
	if _, err := db.SavePlanReportSnapshot(t.Context(), scope, report); err != nil {
		t.Fatal(err)
	}
	a := Authority{Token: "fixture-report", OwnerScope: scope, StillCurrent: current.Load}
	store := New(db)
	if sent, err := store.UploadNextReport(t.Context(), cloudapi.New(cloud.URL), "machine-A", a); err == nil || sent {
		t.Fatal("不明确回执已确认")
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
	mode.Store(2)
	if sent, err := store.UploadNextReport(t.Context(), cloudapi.New(cloud.URL), "machine-A", a); err == nil || sent {
		t.Fatal("错误运行回执已确认")
	}
	mode.Store(3)
	if sent, err := store.UploadNextReport(t.Context(), cloudapi.New(cloud.URL), "machine-A", a); err == nil || sent {
		t.Fatal("授权变化已确认迟到回执")
	}
	current.Store(true)
	mode.Store(0)
	if sent, err := store.UploadNextReport(t.Context(), cloudapi.New(cloud.URL), "machine-A", a); err != nil || !sent {
		t.Fatal("原报告不能确认", err)
	}
	first, err := db.PlanReportSnapshot(t.Context(), scope, run.ID)
	if err != nil || first.UploadState != "confirmed" {
		t.Fatal("上传标记未确认", err)
	}
	if err := db.SetPlanReportSync(t.Context(), scope, run.ID, first.BodyHash, "confirmed"); err != nil {
		t.Fatal(err)
	}
	if sent, err := store.UploadNextReport(t.Context(), cloudapi.New(cloud.URL), "machine-A", a); err != nil || !sent {
		t.Fatal("同步变化没有补传", err)
	}
	last, err := db.PlanReportSnapshot(t.Context(), scope, run.ID)
	if err != nil || last.SyncState != "confirmed" || last.Report.SyncState != "pending" || last.BodyHash != first.BodyHash {
		t.Fatal("更新同步改写原快照", err)
	}
}
