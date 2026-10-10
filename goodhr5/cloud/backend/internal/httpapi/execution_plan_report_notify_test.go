// 本文件用记录邮件器和独立 PostgreSQL 验证 HRPlus 报告通知，不向真实邮箱发送。
package httpapi

import (
	"errors"
	"strings"
	"sync"
	"testing"
)

// reportRecordingMailer 只记录模板与接收人，所有 SMTP 行为均替换为本地测试。
type reportRecordingMailer struct {
	DevMailer
	calls           int
	recipient, body string
	fail            bool
	mu              sync.Mutex
}

// SendCustomHTML 记录一次发送或模拟结果不明，不实际发送邮件。
func (m *reportRecordingMailer) SendCustomHTML(email, subject, body, plain string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls++
	m.recipient = email
	m.body = body
	if m.fail {
		return errors.New("fixture ambiguous send")
	}
	return nil
}

// testReportNotifications 验证通知只发送给原所有者且重复请求不发送第二次。
func testReportNotifications(t *testing.T, store ExecutionPlanStore) {
	t.Helper()
	email, _ := newExecutionPlanID()
	email += "@fixture.test"
	_, summary := endedReportFixture(t, store, email)
	if _, err := store.SaveReport(t.Context(), "", email, summary, "confirmed"); err != nil {
		t.Fatal(err)
	}
	mailer := &reportRecordingMailer{}
	service := &ExecutionPlanService{store: store, execution: &PositionExecutionService{mailer: mailer}}
	if err := service.notifyExecutionReport(t.Context(), "", email, summary.RunID); err != nil {
		t.Fatal(err)
	}
	if err := service.notifyExecutionReport(t.Context(), "", email, summary.RunID); err != nil {
		t.Fatal(err)
	}
	if mailer.calls != 1 || mailer.recipient != email || !strings.Contains(mailer.body, "下一执行日从第一个岗位开始") {
		t.Fatal("重复通知或接收人错误")
	}
	report, err := store.GetReport(t.Context(), "", email, summary.RunID)
	if err != nil || report.NotificationState != "sent" {
		t.Fatal("通知结果未保存", err)
	}
	_, failed := endedReportFixture(t, store, email+"-second")
	if _, err := store.SaveReport(t.Context(), "", email+"-second", failed, "confirmed"); err != nil {
		t.Fatal(err)
	}
	mailer.fail = true
	if err := service.notifyExecutionReport(t.Context(), "", email+"-second", failed.RunID); err != nil {
		t.Fatal(err)
	}
	if err := service.notifyExecutionReport(t.Context(), "", email+"-second", failed.RunID); err != nil {
		t.Fatal(err)
	}
	report, err = store.GetReport(t.Context(), "", email+"-second", failed.RunID)
	if err != nil || report.NotificationState != "unknown" || mailer.calls != 2 {
		t.Fatal("不明结果被自动重复发送", err)
	}
}

// TestMemoryReportNotifications 验证内存通知及开发邮件器状态。
func TestMemoryReportNotifications(t *testing.T) {
	store := NewMemoryExecutionPlanStore()
	testReportNotifications(t, store)
	p, summary := endedReportFixture(t, store, "dev@fixture.test")
	if _, err := store.SaveReport(t.Context(), "", p.UserEmail, summary, "confirmed"); err != nil {
		t.Fatal(err)
	}
	service := &ExecutionPlanService{store: store, execution: &PositionExecutionService{mailer: DevMailer{}}}
	if err := service.notifyExecutionReport(t.Context(), "", p.UserEmail, summary.RunID); err != nil {
		t.Fatal(err)
	}
	r, err := store.GetReport(t.Context(), "", p.UserEmail, summary.RunID)
	if err != nil || r.NotificationState != "not_configured" {
		t.Fatal("开发邮件日志被当成真发送", err)
	}
}

// TestPostgresReportNotifications 验证真实迁移与通知领取、结算契约。
func TestPostgresReportNotifications(t *testing.T) {
	testReportNotifications(t, NewPostgresExecutionPlanStore(planPostgresFixture(t)))
}

// TestPostgresNotificationConcurrent 验证并发处理同一报告时只有一个发送领取，其他请求不能重复发信。
func TestPostgresNotificationConcurrent(t *testing.T) {
	store := NewPostgresExecutionPlanStore(planPostgresFixture(t))
	email, _ := newExecutionPlanID()
	email += "@fixture.test"
	_, summary := endedReportFixture(t, store, email)
	if _, err := store.SaveReport(t.Context(), "", email, summary, "confirmed"); err != nil {
		t.Fatal(err)
	}
	mailer := &reportRecordingMailer{}
	service := &ExecutionPlanService{store: store, execution: &PositionExecutionService{mailer: mailer}}
	var group sync.WaitGroup
	errs := make(chan error, 4)
	for i := 0; i < 4; i++ {
		group.Add(1)
		go func() {
			defer group.Done()
			errs <- service.notifyExecutionReport(t.Context(), "", email, summary.RunID)
		}()
	}
	group.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if mailer.calls != 1 {
		t.Fatal("同报告并发重复发信", mailer.calls)
	}
}

// TestReportHTMLSafe 验证原计划名与原因不能改变邮件 HTML。
func TestReportHTMLSafe(t *testing.T) {
	body := reportNoticeHTML(ExecutionPlanReport{Summary: ExecutionPlanReportSummary{PlanName: "<script>fixture</script>", EndReason: "<img src=x>"}})
	if strings.Contains(body, "<script>") || strings.Contains(body, "<img src=x>") {
		t.Fatal("原报告字段未转义")
	}
}
