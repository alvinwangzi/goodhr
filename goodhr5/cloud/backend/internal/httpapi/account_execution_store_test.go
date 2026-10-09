// 本文件在独立 PostgreSQL 验证 HRPlus 账号执行权竞态、原请求重放及严格释放。
package httpapi

import (
	"errors"
	"strings"
	"testing"
)

// accountClaimFixture 为测试建立唯一账号、占用者和原请求编号。
func accountClaimFixture(t *testing.T) AccountExecutionClaim {
	t.Helper()
	owner, err := newExecutionPlanID()
	if err != nil {
		t.Fatal(err)
	}
	request, err := newExecutionPlanID()
	if err != nil {
		t.Fatal(err)
	}
	return AccountExecutionClaim{UserEmail: owner + "@example.com", MachineID: "A", OwnerType: "plan", OwnerID: owner, RequestID: request, Credential: strings.Repeat("fixture-only-", 4), LocalReserved: true}
}

// TestAccountExecutionPostgres 验证真实事务的取得、重放、收尾证明及迟到释放隔离。
func TestAccountExecutionPostgres(t *testing.T) {
	db := planPostgresFixture(t)
	s := NewPostgresAccountExecutionStore(db)
	c := accountClaimFixture(t)
	t.Cleanup(func() {
		_, _ = db.Exec(`DELETE FROM account_execution_owners WHERE account_key=$1`, c.UserEmail)
		_, _ = db.Exec(`DELETE FROM account_execution_requests WHERE account_key=$1`, c.UserEmail)
	})
	invalid := c
	invalid.LocalReserved = false
	if _, err := s.Claim(t.Context(), invalid); err == nil {
		t.Fatal("未预留本地执行权仍获准")
	}
	o, err := s.Claim(t.Context(), c)
	if err != nil || o.State != "starting" {
		t.Fatal("领取失败或提前运行", o, err)
	}
	if _, err = s.Claim(t.Context(), c); err != nil {
		t.Fatal("原请求不能重放", err)
	}
	changed := c
	changed.MachineID = "B"
	if _, err = s.Claim(t.Context(), changed); !errors.Is(err, ErrAccountExecutionConflict) {
		t.Fatal("换电脑重放原编号", err)
	}
	if err = NewPostgresPositionStore(db).ClaimPositionStart(c.UserEmail, c.OwnerID); !errors.Is(err, ErrPositionAlreadyRunning) {
		t.Fatal("旧手动入口越过计划占用", err)
	}
	if err = s.ConfirmRunning(t.Context(), c); err != nil {
		t.Fatal(err)
	}
	release := c
	release.RequestID, _ = newExecutionPlanID()
	if err = s.Release(t.Context(), release, false); !errors.Is(err, ErrAccountExecutionProof) {
		t.Fatal("无收尾确认仍释放", err)
	}
	wrong := release
	wrong.Credential = strings.Repeat("wrong", 10)
	if err = s.Release(t.Context(), wrong, true); !errors.Is(err, ErrAccountExecutionProof) {
		t.Fatal("错误凭证仍释放", err)
	}
	if err = s.Release(t.Context(), release, true); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Claim(t.Context(), c); !errors.Is(err, ErrAccountExecutionReleased) {
		t.Fatal("迟到领取重新启动旧运行", err)
	}
	next := accountClaimFixture(t)
	next.UserEmail = c.UserEmail
	next.MachineID = "B"
	next.OwnerType = "manual"
	if _, err = s.Claim(t.Context(), next); err != nil {
		t.Fatal(err)
	}
	if err = s.Release(t.Context(), release, true); err != nil {
		t.Fatal("原释放重试未幂等", err)
	}
	if err = s.ConfirmRunning(t.Context(), next); err != nil {
		t.Fatal("迟到释放清掉新占用", err)
	}
	var rawHash string
	if err = db.QueryRow(`SELECT credential_hash FROM account_execution_owners WHERE account_key=$1`, c.UserEmail).Scan(&rawHash); err != nil || rawHash == next.Credential || len(rawHash) != 64 {
		t.Fatal("凭证保存未摘要化", err)
	}
}

// TestAccountExecutionConcurrentPostgres 验证同账号多设备及手动计划竞争只产生一个 starting。
func TestAccountExecutionConcurrentPostgres(t *testing.T) {
	db := planPostgresFixture(t)
	s := NewPostgresAccountExecutionStore(db)
	first := accountClaimFixture(t)
	t.Cleanup(func() {
		_, _ = db.Exec(`DELETE FROM account_execution_owners WHERE account_key=$1`, first.UserEmail)
		_, _ = db.Exec(`DELETE FROM account_execution_requests WHERE account_key=$1`, first.UserEmail)
	})
	barrier := make(chan struct{})
	results := make(chan error, 12)
	for i := 0; i < 12; i++ {
		c := accountClaimFixture(t)
		c.UserEmail = first.UserEmail
		if i%2 == 0 {
			c.OwnerType = "manual"
			c.MachineID = "B"
		}
		go func() { <-barrier; _, err := s.Claim(t.Context(), c); results <- err }()
	}
	close(barrier)
	successes := 0
	for i := 0; i < 12; i++ {
		err := <-results
		if err == nil {
			successes++
		} else if !errors.Is(err, ErrAccountExecutionBusy) {
			t.Fatal(err)
		}
	}
	if successes != 1 {
		t.Fatalf("同账号获准数=%d", successes)
	}
	var count int
	if err := db.QueryRow(`SELECT count(*) FROM account_execution_requests WHERE account_key=$1`, first.UserEmail).Scan(&count); err != nil || count != 1 {
		t.Fatal("等待任务提前获得成功回执", count, err)
	}
}
