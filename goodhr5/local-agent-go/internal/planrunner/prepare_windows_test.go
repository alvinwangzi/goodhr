// 本文件验证 HRPlus 独立任务准备保留父预留和原加密请求，迟到许可只保存事实而不允许页面开始。
package planrunner

import (
	"bytes"
	"errors"
	"goodhr5/local-agent-go/internal/localdb"
	"goodhr5/local-agent-go/internal/positionrunner"
	"sync/atomic"
	"testing"
	"time"
)

// TestPrepareItemCoordination 验证准备前已持久保存原请求、模糊响应原编号重试及独立任务快照确认。
func TestPrepareItemCoordination(t *testing.T) {
	mode := &atomic.Int32{}
	c, plan, claim, authority, _, _ := acquireFixture(t, mode)
	held, err := c.Acquire(t.Context(), plan, claim, authority)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Reservation.Release(true)
	requestID := "60000000-0000-0000-0000-000000000004"
	mode.Store(2)
	if _, err = c.PrepareItem(t.Context(), held, held.Permit.Run, requestID, authority); err == nil {
		t.Fatal("模糊准备响应成为许可")
	}
	before, err := c.db.PlanOperation(t.Context(), authority.OwnerScope, requestID)
	if err != nil || before.State != "pending" {
		t.Fatal("模糊准备没有保留原请求", err)
	}
	if other, err := c.runner.ReservePlanBrowser(t.Context(), "90000000-0000-0000-0000-000000000001"); !errors.Is(err, positionrunner.ErrPlanBrowserBusy) {
		if other != nil {
			_ = other.Release(true)
		}
		t.Fatal("准备失败丢失原父占用", err)
	}
	mode.Store(0)
	result, err := c.PrepareItem(t.Context(), held, held.Permit.Run, requestID, authority)
	if err != nil || result.Run.Items[0].TaskRunID == "" || result.Run.Sequence != held.Permit.Run.Sequence+1 {
		t.Fatal("原准备重试未确认任务关联", err)
	}
	after, err := c.db.PlanOperation(t.Context(), authority.OwnerScope, requestID)
	if err != nil || after.State != "confirmed" || before.Sequence != after.Sequence || !bytes.Equal(before.Cipher, after.Cipher) {
		t.Fatal("重试替换密文或没有确认回执", err)
	}
	snapshot, err := c.db.PlanRunSnapshot(t.Context(), authority.OwnerScope, claim.RunID)
	if err != nil || snapshot.Items[0].TaskRunID != result.Run.Items[0].TaskRunID {
		t.Fatal("没有保存岗位任务关联", err)
	}
}

// TestPrepareItemLateAndWrongBatch 验证时间段外不登记、迟到关联保留事实，以及错误批次不确认原请求。
func TestPrepareItemLateAndWrongBatch(t *testing.T) {
	for _, scenario := range []string{"outside", "late", "wrong_batch"} {
		t.Run(scenario, func(t *testing.T) {
			mode := &atomic.Int32{}
			c, plan, claim, authority, _, clock := acquireFixture(t, mode)
			held, err := c.Acquire(t.Context(), plan, claim, authority)
			if err != nil {
				t.Fatal(err)
			}
			defer held.Reservation.Release(true)
			requestID := "60000000-0000-0000-0000-000000000004"
			switch scenario {
			case "outside":
				clock.Store(time.Date(2026, 10, 10, 4, 0, 0, 0, time.UTC).UnixNano())
			case "late":
				mode.Store(3)
			case "wrong_batch":
				mode.Store(5)
			}
			result, err := c.PrepareItem(t.Context(), held, held.Permit.Run, requestID, authority)
			if err == nil {
				t.Fatal("不允许的准备成为页面许可")
			}
			record, lookupErr := c.db.PlanOperation(t.Context(), authority.OwnerScope, requestID)
			switch scenario {
			case "outside":
				if lookupErr == nil {
					t.Fatal("时间段外登记了新的任务准备")
				}
			case "late":
				if !errors.Is(err, ErrPlanNeedsSettlement) || lookupErr != nil || record.State != "confirmed" || result.Run.Items[0].TaskRunID == "" || held.CanPrepare() {
					t.Fatal("迟到关联丢失事实或仍可打开页面", err, lookupErr)
				}
			case "wrong_batch":
				if !errors.Is(err, localdb.ErrPlanRequestConflict) || lookupErr != nil || record.State != "pending" {
					t.Fatal("错误批次确认了原请求", err, lookupErr)
				}
			}
		})
	}
}
