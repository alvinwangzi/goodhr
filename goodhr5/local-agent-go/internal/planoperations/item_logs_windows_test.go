// 本文件验证 HRPlus 日志丢回执、错误摘要、登录变化和 SQLite 重开的原内容补传，不发送招聘动作。
package planoperations

import (
	"encoding/json"
	"goodhr5/local-agent-go/internal/cloudapi"
	"goodhr5/local-agent-go/internal/localdb"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

// TestItemLogsOriginalReceipt 日志未知回执不能确认，恢复重试使用原编号、原文字和原生成时间。
func TestItemLogsOriginalReceipt(t *testing.T) {
	db, cfg, permit, _ := operationFixture(t)
	var mode, posts atomic.Int32
	var current atomic.Bool
	current.Store(true)
	var original []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/auth/me" {
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "user": map[string]any{"email": "log-owner@example.com"}})
			return
		}
		var input struct {
			Logs []localdb.PlanItemLog `json:"logs"`
		}
		if json.NewDecoder(r.Body).Decode(&input) != nil || len(input.Logs) != 1 {
			t.Error("原日志批次不完整")
			return
		}
		raw, _ := json.Marshal(input.Logs)
		if posts.Add(1) == 1 {
			original = raw
		} else if string(raw) != string(original) {
			t.Error("原日志重试内容改变")
		}
		if mode.Load() == 0 {
			w.WriteHeader(503)
			return
		}
		hash := cloudapi.PlanItemLogHash(input.Logs)
		if mode.Load() == 1 {
			hash = "wrong-receipt"
		}
		if mode.Load() == 2 {
			current.Store(false)
		}
		entry := input.Logs[0]
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "run_id": entry.PlanRunID, "item_run_id": entry.ItemRunID, "task_run_id": entry.TaskRunID, "batch_hash": hash, "count": 1})
	}))
	defer server.Close()
	scope := cloudapi.SessionOwnerScope(server.URL, "log-owner@example.com")
	item := permit.Run.Items[0]
	cp, err := db.EnsurePlanActionRun(t.Context(), localdb.ActionCheckpoint{PlanRunID: permit.Run.ID, ItemRunID: item.ID, CloudRunID: item.ID, OwnerScope: scope, PositionID: "same-job", Platform: "boss", ProfileScope: "fixture", TaskType: "greeting"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.AddPlanItemLog(t.Context(), scope, cp.RunID, "info", "原候选人详细日志"); err != nil {
		t.Fatal(err)
	}
	store := New(db)
	authority := Authority{Token: "fixture-only", OwnerScope: scope, StillCurrent: current.Load}
	client := cloudapi.New(server.URL)
	for _, value := range []int32{0, 1, 2} {
		mode.Store(value)
		if sent, err := store.UploadNextItemLogs(t.Context(), client, "A", authority); err == nil || sent {
			t.Fatal("未知或错登录日志被确认", value, err)
		}
		if _, err := db.NextPlanItemLogBatch(t.Context(), scope); err != nil {
			t.Fatal("原日志待办丢失", err)
		}
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
	current.Store(true)
	mode.Store(3)
	if sent, err := store.UploadNextItemLogs(t.Context(), client, "A", authority); err != nil || !sent {
		t.Fatal("重开后原日志未确认", err)
	}
	if sent, err := store.UploadNextItemLogs(t.Context(), client, "A", authority); err != nil || sent || posts.Load() != 4 {
		t.Fatal("确认后重复补传", err)
	}
	logs, err := db.ListPlanItemLogs(t.Context(), scope, permit.Run.ID, item.ID, 0, 100)
	if err != nil || len(logs) != 1 {
		t.Fatal("确认后本地原日志被删除", err)
	}
}
