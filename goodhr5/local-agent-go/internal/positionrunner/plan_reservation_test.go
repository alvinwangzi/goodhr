// 本文件验证 HRPlus 父计划与手动任务及收尾共用执行权，取消或迟到释放不能越过安全边界。
package positionrunner

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

// TestPlanReservationSharesBrowser 验证计划被手动与收尾挡住，计划占用期间也挡住手动与收尾。
func TestPlanReservationSharesBrowser(t *testing.T) {
	r := newTestRunner(t, openRunnerTestDB(t), &fakeWorker{})
	id := "10000000-0000-0000-0000-000000000001"
	if !r.setRunning("manual", func() {}, StartOptions{}) {
		t.Fatal("手动测试启动失败")
	}
	if _, err := r.ReservePlanBrowser(t.Context(), id); !errors.Is(err, ErrPlanBrowserBusy) {
		t.Fatal("计划越过手动占用", err)
	}
	r.clear("manual")
	release, _, ok := r.reserveResumeBrowser("manual")
	if !ok {
		t.Fatal("收尾预留失败")
	}
	if _, err := r.ReservePlanBrowser(t.Context(), id); !errors.Is(err, ErrPlanBrowserBusy) {
		t.Fatal("计划越过收尾占用", err)
	}
	release()
	plan, err := r.ReservePlanBrowser(t.Context(), id)
	if err != nil || !plan.Valid() {
		t.Fatal(err)
	}
	if r.setRunning("other", func() {}, StartOptions{}) {
		t.Fatal("手动越过父计划")
	}
	if _, _, ok = r.reserveResumeBrowser("other"); ok {
		t.Fatal("后台收尾越过父计划")
	}
	if r.StopAll("退出登录") != 1 || plan.Context().Err() == nil || plan.Valid() {
		t.Fatal("全局停止没有取消父计划")
	}
	if err = plan.Release(false); !errors.Is(err, ErrPlanBrowserCleanup) {
		t.Fatal("没有收尾证明就释放", err)
	}
	wait, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
	defer cancel()
	if err = r.StopAllAndWait(wait, "切换登录"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("取消被当作页面已清理", err)
	}
	if err = plan.Release(true); err != nil {
		t.Fatal(err)
	}
	if !r.setRunning("other", func() {}, StartOptions{}) {
		t.Fatal("收尾后未允许手动")
	}
	r.clear("other")
}

// TestPlanReservationCompetition 验证十二个计划同时预留只有一个成功，重复旧释放不清掉新预留。
func TestPlanReservationCompetition(t *testing.T) {
	r := newTestRunner(t, openRunnerTestDB(t), &fakeWorker{})
	barrier := make(chan struct{})
	results := make(chan *PlanBrowserReservation, 12)
	failures := make(chan error, 12)
	for index := 0; index < 12; index++ {
		id := fmt.Sprintf("10000000-0000-0000-0000-%012x", index+1)
		go func() {
			<-barrier
			p, e := r.ReservePlanBrowser(t.Context(), id)
			if e != nil {
				failures <- e
				results <- nil
			} else {
				results <- p
			}
		}()
	}
	close(barrier)
	var winner *PlanBrowserReservation
	successes := 0
	for index := 0; index < 12; index++ {
		p := <-results
		if p != nil {
			winner = p
			successes++
		}
	}
	if successes != 1 {
		t.Fatalf("预留成功数=%d", successes)
	}
	for index := 0; index < 11; index++ {
		if err := <-failures; !errors.Is(err, ErrPlanBrowserBusy) {
			t.Fatal(err)
		}
	}
	if err := winner.Release(true); err != nil {
		t.Fatal(err)
	}
	next, err := r.ReservePlanBrowser(t.Context(), "20000000-0000-0000-0000-000000000001")
	if err != nil {
		t.Fatal(err)
	}
	if err = winner.Release(true); err != nil || !next.Valid() {
		t.Fatal("迟到旧释放清掉新预留", err)
	}
	if err = next.Release(true); err != nil {
		t.Fatal(err)
	}
}

// TestPlanReservationParentCancellation 验证上层退出只取消授权，页面执行权必须等明确收尾。
func TestPlanReservationParentCancellation(t *testing.T) {
	r := newTestRunner(t, openRunnerTestDB(t), &fakeWorker{})
	parent, cancel := context.WithCancel(t.Context())
	p, err := r.ReservePlanBrowser(parent, "10000000-0000-0000-0000-000000000001")
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	if p.Valid() || p.Context().Err() == nil {
		t.Fatal("上层取消后预留仍可执行")
	}
	if _, err = r.ReservePlanBrowser(t.Context(), "20000000-0000-0000-0000-000000000001"); !errors.Is(err, ErrPlanBrowserBusy) {
		t.Fatal("取消直接允许新页面任务", err)
	}
	if err = p.Release(true); err != nil {
		t.Fatal(err)
	}
	if _, err = r.ReservePlanBrowser(parent, "20000000-0000-0000-0000-000000000001"); !errors.Is(err, context.Canceled) {
		t.Fatal("取消的上层可以预留", err)
	}
}
