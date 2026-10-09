// 本文件验证 HRPlus 释放事实先落盘再让出本地页面，存储失败、不明确确认及重复请求不丢失原占用。
package planrunner

import (
	"context"
	"errors"
	"goodhr5/local-agent-go/internal/localdb"
	"goodhr5/local-agent-go/internal/positionrunner"
	"sync/atomic"
	"testing"
)

// TestReleaseAfterCleanupOrdering 验证无收尾确认保留本地占用，落盘后只声明等待云端确认。
func TestReleaseAfterCleanupOrdering(t *testing.T) {
	mode := &atomic.Int32{}
	c, p, input, a, _, _ := acquireFixture(t, mode)
	held, err := c.Acquire(t.Context(), p, input, a)
	if err != nil {
		t.Fatal(err)
	}
	request := "70000000-0000-0000-0000-000000000001"
	if _, err = c.ReleaseAfterCleanup(t.Context(), held, held.Permit.Run, request, "waiting_window", "window_ended", false); err == nil {
		t.Fatal("无收尾确认释放")
	}
	if _, err = c.runner.ReservePlanBrowser(t.Context(), "90000000-0000-0000-0000-000000000001"); !errors.Is(err, positionrunner.ErrPlanBrowserBusy) {
		t.Fatal("未收尾允许另一任务", err)
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err = c.ReleaseAfterCleanup(cancelled, held, held.Permit.Run, request, "waiting_window", "window_ended", true); err == nil || !held.Reservation.Valid() {
		t.Fatal("存储失败仍释放本地", err)
	}
	op, err := c.ReleaseAfterCleanup(t.Context(), held, held.Permit.Run, request, "waiting_window", "window_ended", true)
	if err != nil || op.State != "pending" || op.Kind != "release" || op.RunSequence != held.Permit.Run.Sequence+1 || held.Reservation.Valid() {
		t.Fatal("原释放未持久登记或冒称云端确认", err)
	}
	stored, err := c.requests.OriginalUpdate(t.Context(), a.OwnerScope, request)
	if err != nil || !stored.CleanupConfirmed || stored.Credential != input.Credential || stored.RunID != input.RunID {
		t.Fatal("丢失原收尾事实", err)
	}
	next, err := c.runner.ReservePlanBrowser(t.Context(), "90000000-0000-0000-0000-000000000001")
	if err != nil {
		t.Fatal(err)
	}
	defer next.Release(true)
	repeated, err := c.ReleaseAfterCleanup(t.Context(), held, held.Permit.Run, request, "waiting_window", "window_ended", true)
	if err != nil || repeated.Sequence != op.Sequence || !next.Valid() {
		t.Fatal("旧重复释放影响新本地占用", err)
	}
	if _, err = c.ReleaseAfterCleanup(t.Context(), held, held.Permit.Run, request, "stopped", "user_stopped", true); !errors.Is(err, localdb.ErrPlanRequestConflict) {
		t.Fatal("相同编号换释放结论", err)
	}
}

// TestReleaseLatePermit 验证没有打开页面的迟到许可仍登记原释放，不能遗忘已取得的云端占用。
func TestReleaseLatePermit(t *testing.T) {
	mode := &atomic.Int32{}
	mode.Store(3)
	c, p, input, a, _, _ := acquireFixture(t, mode)
	held, err := c.Acquire(t.Context(), p, input, a)
	if !errors.Is(err, ErrOutsidePlanWindow) || held == nil {
		t.Fatal(err)
	}
	request := "70000000-0000-0000-0000-000000000002"
	op, err := c.ReleaseAfterCleanup(t.Context(), held, held.Permit.Run, request, "waiting_window", "late_permit", true)
	if err != nil || op.State != "pending" {
		t.Fatal("迟到占用没有留下释放事实", err)
	}
	original, err := c.requests.OriginalUpdate(t.Context(), a.OwnerScope, request)
	if err != nil || original.OwnerID != input.OwnerID || original.CurrentItem != 0 || original.Sequence != 2 {
		t.Fatal("迟到许可被当作新轮次", err)
	}
}
