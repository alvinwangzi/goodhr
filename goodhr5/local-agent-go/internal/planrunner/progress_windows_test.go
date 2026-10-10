// 本文件用真实 Windows 原请求加密、SQLite 与受控云端验证 HRPlus 安全步骤上报和原编号重试。
package planrunner

import (
	"bytes"
	"database/sql"
	"errors"
	"goodhr5/local-agent-go/internal/planmodel"
	"sync/atomic"
	"testing"
)

// TestPersistProgressOriginalRetry 验证发送前保存原状态、不明确回执不推进、原重试保存规范时间和完整数量。
func TestPersistProgressOriginalRetry(t *testing.T) {
	mode := &atomic.Int32{}
	c, plan, claim, authority, _, _ := acquireFixture(t, mode)
	held, err := c.Acquire(t.Context(), plan, claim, authority)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Reservation.Release(true)
	prepared, err := c.PrepareItem(t.Context(), held, held.Permit.Run, "60000000-0000-0000-0000-000000000004", authority)
	if err != nil {
		t.Fatal(err)
	}
	next := prepared.Run
	next.Sequence++
	next.State = "running"
	next.Items[0].State = "running"
	for action := range next.Items[0].Actions {
		next.Items[0].Actions[action] = planmodel.ActionProgress{State: "active", Count: 1, UnknownCount: 1}
	}
	request := "60000000-0000-0000-0000-000000000009"
	mode.Store(2)
	if _, err = c.PersistProgress(t.Context(), held, next, request, authority); err == nil {
		t.Fatal("泛成功确认了步骤")
	}
	before, err := c.db.PlanOperation(t.Context(), authority.OwnerScope, request)
	if err != nil || before.State != "pending" {
		t.Fatal("不明确结果丢失原请求", err)
	}
	if _, err = c.PersistProgress(t.Context(), held, next, "60000000-0000-0000-0000-000000000010", authority); err == nil {
		t.Fatal("原请求未确认就登记下一请求")
	}
	if _, err = c.db.PlanOperation(t.Context(), authority.OwnerScope, "60000000-0000-0000-0000-000000000010"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("未确认步骤补造新编号", err)
	}
	mode.Store(0)
	permit, err := c.PersistProgress(t.Context(), held, next, request, authority)
	if err != nil || permit.Run.Sequence != next.Sequence || permit.Run.StartedAt == nil {
		t.Fatal("原步骤无法核对规范回执", err)
	}
	after, err := c.db.PlanOperation(t.Context(), authority.OwnerScope, request)
	if err != nil || after.State != "confirmed" || before.Sequence != after.Sequence || !bytes.Equal(before.Cipher, after.Cipher) {
		t.Fatal("原重试改变密文或未确认", err)
	}
	snapshot, err := c.db.PlanRunSnapshot(t.Context(), authority.OwnerScope, next.ID)
	if err != nil || snapshot.Sequence != next.Sequence || snapshot.StartedAt == nil || snapshot.Items[0].Actions["auto_reply"].Count != 1 || snapshot.Items[0].Actions["auto_reply"].UnknownCount != 1 {
		t.Fatal("规范进度或未知数量未保存", err)
	}
	if !held.Reservation.Valid() {
		t.Fatal("上报释放了父占用")
	}
}

// TestPersistProgressAuthorizationChange 验证原回执到达时授权已变，原请求保持待核对且不保存新进度。
func TestPersistProgressAuthorizationChange(t *testing.T) {
	mode := &atomic.Int32{}
	c, plan, claim, authority, _, _ := acquireFixture(t, mode)
	held, err := c.Acquire(t.Context(), plan, claim, authority)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Reservation.Release(true)
	next := held.Permit.Run
	next.Sequence++
	next.State = "running"
	authority.ConfirmCurrent = func(func() error) error { return ErrPlanAuthority }
	request := "60000000-0000-0000-0000-000000000009"
	if _, err = c.PersistProgress(t.Context(), held, next, request, authority); !errors.Is(err, ErrPlanAuthority) {
		t.Fatal("授权变化仍确认回执", err)
	}
	op, err := c.db.PlanOperation(t.Context(), authority.OwnerScope, request)
	if err != nil || op.State != "pending" {
		t.Fatal("旧回执未保留", err)
	}
	snapshot, err := c.db.PlanRunSnapshot(t.Context(), authority.OwnerScope, next.ID)
	if err != nil || snapshot.Sequence != held.Permit.Run.Sequence {
		t.Fatal("授权变化保存了新进度", err)
	}
}
