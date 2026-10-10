// 本文件用真实 SQLite 重开和受控 HTTP 验证 HRPlus 原排队上报不变更时间且明确确认后不重发。
package planrunner

import (
	"encoding/json"
	"goodhr5/local-agent-go/internal/cloudapi"
	"goodhr5/local-agent-go/internal/config"
	"goodhr5/local-agent-go/internal/localdb"
	"goodhr5/local-agent-go/internal/planmodel"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
)

// TestWaitingFactsRestart 验证丢失回执和换登录保留原事实，重开后已确认记录不再次发送。
func TestWaitingFactsRestart(t *testing.T) {
	c, plan, _, a, _, _ := acquireFixture(t, &atomic.Int32{})
	var posts atomic.Int32
	var mode atomic.Int32
	var current atomic.Bool
	current.Store(true)
	var original cloudapi.PlanWaitFact
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var input cloudapi.PlanWaitFact
		if json.NewDecoder(r.Body).Decode(&input) != nil {
			t.Error("原事实未发送")
		}
		if posts.Add(1) == 1 {
			original = input
		} else if input.RequestID != original.RequestID || !input.TriggeredAt.Equal(original.TriggeredAt) || input.QueuedAt == nil || !input.QueuedAt.Equal(*original.QueuedAt) {
			t.Error("重试替换原排队时刻")
		}
		if mode.Load() == 0 {
			w.WriteHeader(503)
			return
		}
		if mode.Load() == 1 {
			current.Store(false)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "wait": input})
	}))
	defer server.Close()
	c.client = cloudapi.New(server.URL)
	a.OwnerScope = cloudapi.SessionOwnerScope(server.URL, plan.UserEmail)
	a.StillCurrent = current.Load
	scheduler := NewScheduler(c)
	plans := []planmodel.Plan{plan}
	if err := scheduler.EnqueueDue(t.Context(), plans, plan.MachineID, a); err != nil {
		t.Fatal(err)
	}
	request, err := c.db.NextPlanRequest(t.Context(), a.OwnerScope)
	if err != nil {
		t.Fatal(err)
	}
	if err := scheduler.SyncWaitingFacts(t.Context(), plans, plan.MachineID, a); err == nil {
		t.Fatal("无回执仍确认")
	}
	_, hash, err := c.db.PlanWaitReceipt(t.Context(), a.OwnerScope, request.RequestID)
	if err != nil || hash != "" {
		t.Fatal("失败记录已确认", err)
	}
	mode.Store(1)
	if err := scheduler.SyncWaitingFacts(t.Context(), plans, plan.MachineID, a); err == nil {
		t.Fatal("换登录后确认原回执")
	}
	current.Store(true)
	mode.Store(2)
	if err := scheduler.SyncWaitingFacts(t.Context(), plans, plan.MachineID, a); err != nil {
		t.Fatal(err)
	}
	path := filepath.Dir(c.db.Path())
	if err := c.db.Close(); err != nil {
		t.Fatal(err)
	}
	c.db, err = localdb.Open(&config.Config{DataDir: path})
	if err != nil {
		t.Fatal(err)
	}
	defer c.db.Close()
	if err := scheduler.SyncWaitingFacts(t.Context(), plans, plan.MachineID, a); err != nil || posts.Load() != 3 {
		t.Fatal("重开后重复发送已确认原事实", err, posts.Load())
	}
	queued, hash, err := c.db.PlanWaitReceipt(t.Context(), a.OwnerScope, request.RequestID)
	if err != nil || hash == "" || queued == nil || !queued.Equal(*original.QueuedAt) {
		t.Fatal("重开改变原入队时刻", err)
	}
}
