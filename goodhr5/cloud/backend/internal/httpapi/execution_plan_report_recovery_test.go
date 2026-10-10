// 本文件验证 HRPlus 通知恢复和迟到原结果，PostgreSQL 使用专用临时数据库，不发送真实邮件。
package httpapi

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"
)

// cancelReportMailer 在通知已进入发送阶段后等待取消，只模拟原发送结束，不访问 SMTP。
type cancelReportMailer struct {
	reportRecordingMailer
	entered chan struct{}
}

// SendCustomHTMLContext 验证后台取消传到实际发送边界，返回错误后需保存原未知结论。
func (m *cancelReportMailer) SendCustomHTMLContext(ctx context.Context, _, _, _, _ string) error {
	close(m.entered)
	<-ctx.Done()
	return ctx.Err()
}

// TestReportNotificationWorkerCancel 验证实际后台循环退出，原发送中记录不会一直挂着或重发。
func TestReportNotificationWorkerCancel(t *testing.T) {
	store := NewMemoryExecutionPlanStore()
	email := "cancel-notice@fixture.invalid"
	_, summary := endedReportFixture(t, store, email)
	if _, err := store.SaveReport(t.Context(), "", email, summary, "confirmed"); err != nil {
		t.Fatal(err)
	}
	mailer := &cancelReportMailer{entered: make(chan struct{})}
	service := NewExecutionPlanService(nil, nil, store)
	service.execution = &PositionExecutionService{mailer: mailer}
	server := &Server{executionPlans: service}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan struct{})
	go func() { defer close(done); server.RunExecutionReportNotifications(ctx) }()
	select {
	case <-mailer.entered:
	case <-time.After(time.Second):
		t.Fatal("后台未领取原通知")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("后台取消后未退出")
	}
	report, err := store.GetReport(t.Context(), "", email, summary.RunID)
	if err != nil || report.NotificationState != "unknown" {
		t.Fatal("退出后原发送结论丢失", err)
	}
}

// reportRecoveryPostgres 为本次通知测试创建独占数据库，后台读取不接触其他测试的邮件记录。
func reportRecoveryPostgres(t *testing.T) ExecutionPlanStore {
	t.Helper()
	parent := planPostgresFixture(t)
	id, _ := newExecutionPlanID()
	database := "hrplus_m2_notice_" + strings.ReplaceAll(id, "-", "")
	if _, err := parent.Exec(`CREATE DATABASE ` + database); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := parent.Exec(`DROP DATABASE ` + database); err != nil {
			t.Error(err)
		}
	})
	dsn, err := url.Parse(os.Getenv("GOODHR_EXECUTION_PLAN_TEST_PG_DSN"))
	if err != nil {
		t.Fatal(err)
	}
	dsn.Path = "/" + database
	db, err := sql.Open("postgres", dsn.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := RunMigrations(db); err != nil {
		t.Fatal(err)
	}
	return NewPostgresExecutionPlanStore(db)
}

// testReportRecovery 验证未配置后可补发，中断发送不重发，原已知结果迟到仍能精确确认。
func testReportRecovery(t *testing.T, store ExecutionPlanStore) {
	t.Helper()
	email := "notice-recovery@fixture.invalid"
	_, summary := endedReportFixture(t, store, email)
	if _, err := store.SaveReport(t.Context(), "", email, summary, "confirmed"); err != nil {
		t.Fatal(err)
	}
	service := NewExecutionPlanService(nil, nil, store)
	service.execution = &PositionExecutionService{mailer: DevMailer{}}
	if err := service.notifyExecutionReport(t.Context(), "", email, summary.RunID); err != nil {
		t.Fatal(err)
	}
	if work, err := store.ReportNotificationWork(t.Context(), time.Now().Add(-5*time.Minute), false, 20); err != nil || len(work) != 0 {
		t.Fatal("未配置仍准备发送", err, work)
	}
	mailer := &reportRecordingMailer{}
	service.execution.mailer = mailer
	for range 2 {
		if err := service.processReportNotifications(t.Context(), time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	report, err := store.GetReport(t.Context(), "", email, summary.RunID)
	if err != nil || report.NotificationState != "sent" || mailer.calls != 1 || mailer.recipient != email {
		t.Fatal("配置后未补发或重复补发", err, mailer.calls)
	}
	_, interrupted := endedReportFixture(t, store, email)
	if _, err := store.SaveReport(t.Context(), "", email, interrupted, "confirmed"); err != nil {
		t.Fatal(err)
	}
	token, _ := newExecutionPlanID()
	claimed, ok, err := store.ClaimReportNotification(t.Context(), "", email, interrupted.RunID, token)
	if err != nil || !ok || claimed.NotificationStartedAt == nil {
		t.Fatal("发送开始未保存", err)
	}
	if _, err := store.SaveReport(t.Context(), "", email, interrupted, "confirmed"); err != nil {
		t.Fatal(err)
	}
	current, err := store.GetReport(t.Context(), "", email, interrupted.RunID)
	if err != nil || current.NotificationStartedAt == nil || !current.NotificationStartedAt.Equal(*claimed.NotificationStartedAt) {
		t.Fatal("报告重传延长发送开始时间", err)
	}
	if err := service.processReportNotifications(t.Context(), time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if memory, ok := store.(*MemoryExecutionPlanStore); ok {
		changes, close, subscribed := service.events.subscribe(planEventScope("", email))
		if !subscribed {
			t.Fatal("恢复事件无法订阅")
		}
		defer close()
		memory.mu.Lock()
		original := memory.reports[interrupted.RunID]
		original.NotificationState = "sending"
		original.NotificationStartedAt = nil
		memory.reports[interrupted.RunID] = original
		memory.mu.Unlock()
		if err := service.processReportNotifications(t.Context(), time.Now()); err != nil {
			t.Fatal(err)
		}
		select {
		case <-changes:
		default:
			t.Fatal("未知发送恢复未通知页面")
		}
	}
	current, err = store.GetReport(t.Context(), "", email, interrupted.RunID)
	if err != nil || current.NotificationState != "unknown" || mailer.calls != 1 {
		t.Fatal("中断发送被重新发送", err, mailer.calls)
	}
	other, _ := newExecutionPlanID()
	if _, claimed, err := store.ClaimReportNotification(t.Context(), "", email, interrupted.RunID, other); err != nil || claimed {
		t.Fatal("未知结果重新领取", err)
	}
	if err := store.FinishReportNotification(t.Context(), "", email, interrupted.RunID, other, "sent", ""); !errors.Is(err, ErrExecutionPlanRequest) {
		t.Fatal("另一发送编号结算旧发送", err)
	}
	if err := store.FinishReportNotification(t.Context(), "", email, interrupted.RunID, token, "sent", ""); err != nil {
		t.Fatal("原明确结果迟到不能确认", err)
	}
	if err := service.processReportNotifications(t.Context(), time.Now().Add(2*time.Hour)); err != nil || mailer.calls != 1 {
		t.Fatal("迟到确认后重新发送", err)
	}
}

// TestReportNotificationRecovery 执行内存和独占 PostgreSQL 数据库的相同账本恢复契约。
func TestReportNotificationRecovery(t *testing.T) {
	t.Run("memory", func(t *testing.T) { testReportRecovery(t, NewMemoryExecutionPlanStore()) })
	t.Run("postgres", func(t *testing.T) { testReportRecovery(t, reportRecoveryPostgres(t)) })
}
