// 本文件验证 HRPlus 状态单调保存、实际开始确认、跨窗口释放及迟到旧凭证隔离。
package httpapi

import (
	"errors"
	"testing"
)

// planRunUpdateFixture 为原占用者生成新的状态或收尾请求。
func planRunUpdateFixture(t *testing.T, c ExecutionPlanRunClaim, sequence int64, action, state string) ExecutionPlanRunUpdate {
	t.Helper()
	request, _ := newExecutionPlanID()
	return ExecutionPlanRunUpdate{PlanID: c.PlanID, RunID: c.RunID, Action: action, RequestID: request, OwnerID: c.OwnerID, MachineID: c.MachineID, Credential: c.Credential, Sequence: sequence, State: state, CleanupConfirmed: action == "release"}
}

// testPlanRunUpdateContract 验证两个存储实现均保持原开始时间和游标，旧释放不影响新占用。
func testPlanRunUpdateContract(t *testing.T, s ExecutionPlanStore) {
	t.Helper()
	email, _ := newExecutionPlanID()
	p := createArmedPlanFixture(t, s, email+"@example.com")
	c := planRunClaimFixture(t, p)
	if _, err := s.ClaimRun(t.Context(), "", p.UserEmail, c); err != nil {
		t.Fatal(err)
	}
	status := planRunUpdateFixture(t, c, 2, "status", "running")
	running, err := s.UpdateRun(t.Context(), "", p.UserEmail, status)
	if err != nil || running.Run.StartedAt == nil || running.Run.FinishedAt != nil || running.Owner.State != "running" {
		t.Fatal("实际开始未确认", err)
	}
	replay, err := s.UpdateRun(t.Context(), "", p.UserEmail, status)
	if err != nil || replay.Run.Sequence != 2 || !replay.Run.StartedAt.Equal(*running.Run.StartedAt) {
		t.Fatal("状态重试变更开始时间", err)
	}
	stale := planRunUpdateFixture(t, c, 2, "status", "running")
	if _, err = s.UpdateRun(t.Context(), "", p.UserEmail, stale); !errors.Is(err, ErrExecutionPlanSequence) {
		t.Fatal("同序号新请求覆盖状态", err)
	}
	drain := planRunUpdateFixture(t, c, 3, "status", "draining")
	drain.CurrentItem = 1
	draining, err := s.UpdateRun(t.Context(), "", p.UserEmail, drain)
	if err != nil || draining.Owner.State != "releasing" {
		t.Fatal("收尾未继续占用", err)
	}
	unsafe := planRunUpdateFixture(t, c, 4, "status", "running")
	unsafe.CurrentItem = 1
	if _, err = s.UpdateRun(t.Context(), "", p.UserEmail, unsafe); !errors.Is(err, ErrExecutionPlanSequence) {
		t.Fatal("收尾未经重领恢复运行", err)
	}
	release := planRunUpdateFixture(t, c, 4, "release", "waiting_window")
	release.CurrentItem = 1
	noCleanup := release
	noCleanup.CleanupConfirmed = false
	if _, err = s.UpdateRun(t.Context(), "", p.UserEmail, noCleanup); err == nil {
		t.Fatal("未收尾就释放")
	}
	waiting, err := s.UpdateRun(t.Context(), "", p.UserEmail, release)
	if err != nil || waiting.Owner.State != "released" || waiting.Run.FinishedAt != nil {
		t.Fatal("窗口等待错误结算或未释放", err)
	}
	next := planRunClaimFixture(t, p)
	next.RunID = c.RunID
	resumed, err := s.ClaimRun(t.Context(), "", p.UserEmail, next)
	if err != nil || resumed.Run.CurrentItem != 1 || resumed.Run.Sequence != 5 {
		t.Fatal("跨窗口重新领取丢进度", err)
	}
	resumedStatus := planRunUpdateFixture(t, next, 6, "status", "running")
	resumedStatus.CurrentItem = 1
	confirmed, err := s.UpdateRun(t.Context(), "", p.UserEmail, resumedStatus)
	if err != nil || !confirmed.Run.StartedAt.Equal(*running.Run.StartedAt) {
		t.Fatal("重领改写首次开始时间", err)
	}
	if _, err = s.UpdateRun(t.Context(), "", p.UserEmail, release); err != nil {
		t.Fatal("原释放不能幂等重试", err)
	}
	freshRelease := release
	freshRelease.RequestID, _ = newExecutionPlanID()
	freshRelease.Sequence = 7
	if _, err = s.UpdateRun(t.Context(), "", p.UserEmail, freshRelease); !errors.Is(err, ErrAccountExecutionProof) {
		t.Fatal("旧凭证清掉新占用", err)
	}
	current, err := s.GetRun(t.Context(), "", p.UserEmail, c.RunID)
	if err != nil || current.Sequence != 6 || current.OwnerID != next.OwnerID || current.State != "running" {
		t.Fatal("旧释放改变新运行", err)
	}
	completed := planRunUpdateFixture(t, next, 7, "release", "completed")
	completed.CurrentItem = len(p.Config.Items)
	finished, err := s.UpdateRun(t.Context(), "", p.UserEmail, completed)
	if err != nil || finished.Run.FinishedAt == nil || finished.Run.State != "completed" {
		t.Fatal("正常结束未结算", err)
	}
	again := planRunClaimFixture(t, p)
	again.RunID = c.RunID
	if _, err = s.ClaimRun(t.Context(), "", p.UserEmail, again); !errors.Is(err, ErrExecutionPlanBusy) {
		t.Fatal("当天终态再次启动", err)
	}
	if _, err = s.GetRun(t.Context(), "", "foreign@example.com", c.RunID); !errors.Is(err, ErrNotFound) {
		t.Fatal("读取其他用户运行", err)
	}
}

// TestPlanRunUpdateStores 分别运行内存与明确启用的独立 PostgreSQL 全生命周期状态契约。
func TestPlanRunUpdateStores(t *testing.T) {
	t.Run("memory", func(t *testing.T) { testPlanRunUpdateContract(t, NewMemoryExecutionPlanStore()) })
	t.Run("postgres", func(t *testing.T) {
		testPlanRunUpdateContract(t, NewPostgresExecutionPlanStore(planPostgresFixture(t)))
	})
}

// TestPlanRunStopReleaseMemory 验证用户停止后不能继续运行或进入下一窗口，收尾后才开放编辑。
func TestPlanRunStopReleaseMemory(t *testing.T) {
	s := NewMemoryExecutionPlanStore()
	p := createArmedPlanFixture(t, s, "stop-release@example.com")
	c := planRunClaimFixture(t, p)
	if _, err := s.ClaimRun(t.Context(), "", p.UserEmail, c); err != nil {
		t.Fatal(err)
	}
	stop := planIntentFixture(t, "stop")
	stop.ActivationID = p.ActivationID
	p, err := s.Intent(t.Context(), "", p.UserEmail, p.ID, stop)
	if err != nil {
		t.Fatal(err)
	}
	status := planRunUpdateFixture(t, c, 2, "status", "running")
	if _, err = s.UpdateRun(t.Context(), "", p.UserEmail, status); !errors.Is(err, ErrExecutionPlanSequence) {
		t.Fatal("停止后仍开始执行", err)
	}
	waiting := planRunUpdateFixture(t, c, 2, "release", "waiting_window")
	if _, err = s.UpdateRun(t.Context(), "", p.UserEmail, waiting); !errors.Is(err, ErrExecutionPlanSequence) {
		t.Fatal("停止后继续等窗口", err)
	}
	stopped := planRunUpdateFixture(t, c, 2, "release", "stopped")
	if _, err = s.UpdateRun(t.Context(), "", p.UserEmail, stopped); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Save(t.Context(), p, 1); !errors.Is(err, ErrExecutionPlanBusy) {
		t.Fatal("未确认计划收尾就开放编辑", err)
	}
	if _, err = s.ConfirmStopped(t.Context(), "", p.UserEmail, p.ID, planStopConfirmationFixture(t, p)); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Save(t.Context(), p, 1); err != nil {
		t.Fatal("真实释放与确认后仍锁定", err)
	}
}

// TestPlanRunReleaseUnblocksLegacyMemory 验证真实释放清掉共享占用快照，允许下个旧手动任务启动。
func TestPlanRunReleaseUnblocksLegacyMemory(t *testing.T) {
	positions := NewMemoryPositionStore()
	plans := NewMemoryExecutionPlanStore()
	plans.positions = positions
	email := "release-legacy@example.com"
	position, err := positions.SavePosition(Position{UserEmail: email, PlatformID: "boss", Name: "fixture"})
	if err != nil {
		t.Fatal(err)
	}
	p := createArmedPlanFixture(t, plans, email)
	c := planRunClaimFixture(t, p)
	if _, err = plans.ClaimRun(t.Context(), "", email, c); err != nil {
		t.Fatal(err)
	}
	if err = positions.ClaimPositionStart(email, position.ID); !errors.Is(err, ErrPositionAlreadyRunning) {
		t.Fatal("释放前启动手动", err)
	}
	if _, err = plans.UpdateRun(t.Context(), "", email, planRunUpdateFixture(t, c, 2, "release", "waiting_window")); err != nil {
		t.Fatal(err)
	}
	if err = positions.ClaimPositionStart(email, position.ID); err != nil {
		t.Fatal("释放后仍挡手动", err)
	}
}

// TestPlanRunSequenceCompetitionPostgres 验证并发迟到消息不能覆盖更高序号与已推进游标。
func TestPlanRunSequenceCompetitionPostgres(t *testing.T) {
	db := planPostgresFixture(t)
	s := NewPostgresExecutionPlanStore(db)
	email, _ := newExecutionPlanID()
	p := createArmedPlanFixture(t, s, email+"@example.com")
	c := planRunClaimFixture(t, p)
	if _, err := s.ClaimRun(t.Context(), "", p.UserEmail, c); err != nil {
		t.Fatal(err)
	}
	low := planRunUpdateFixture(t, c, 2, "status", "running")
	high := planRunUpdateFixture(t, c, 3, "status", "running")
	high.CurrentItem = 1
	barrier := make(chan struct{})
	results := make(chan error, 2)
	for _, update := range []ExecutionPlanRunUpdate{low, high} {
		go func() { <-barrier; _, err := s.UpdateRun(t.Context(), "", p.UserEmail, update); results <- err }()
	}
	close(barrier)
	for i := 0; i < 2; i++ {
		err := <-results
		if err != nil && !errors.Is(err, ErrExecutionPlanSequence) {
			t.Fatal(err)
		}
	}
	current, err := s.GetRun(t.Context(), "", p.UserEmail, c.RunID)
	if err != nil || current.Sequence != 3 || current.CurrentItem != 1 {
		t.Fatal("更低序号覆盖进度", current, err)
	}
}

// TestPlanRunReleaseRollbackPostgres 验证释放回执冲突时运行状态与账号占用一同保留。
func TestPlanRunReleaseRollbackPostgres(t *testing.T) {
	db := planPostgresFixture(t)
	s := NewPostgresExecutionPlanStore(db)
	email, _ := newExecutionPlanID()
	p := createArmedPlanFixture(t, s, email+"@example.com")
	c := planRunClaimFixture(t, p)
	if _, err := s.ClaimRun(t.Context(), "", p.UserEmail, c); err != nil {
		t.Fatal(err)
	}
	release := planRunUpdateFixture(t, c, 2, "release", "waiting_window")
	_, err := db.Exec(`INSERT INTO account_execution_requests(account_key,request_id,kind,body_hash,owner_id) VALUES($1,$2,'release','fixture-conflict',$3)`, p.UserEmail, release.RequestID, c.OwnerID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.UpdateRun(t.Context(), "", p.UserEmail, release); !errors.Is(err, ErrAccountExecutionConflict) {
		t.Fatal("冲突回执仍释放", err)
	}
	current, err := s.GetRun(t.Context(), "", p.UserEmail, c.RunID)
	if err != nil || current.State != "starting" || current.Sequence != 1 {
		t.Fatal("释放失败未回滚运行", current, err)
	}
	var held int
	if err = db.QueryRow(`SELECT count(*) FROM account_execution_owners WHERE account_key=$1 AND owner_id=$2`, p.UserEmail, c.OwnerID).Scan(&held); err != nil || held != 1 {
		t.Fatal("释放失败清掉占用", held, err)
	}
}
