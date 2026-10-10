// 本文件复用 HRPlus 邮件器通知原报告所有者，发送不明保留单独状态，报告失败不停止计划。
package httpapi

import (
	"context"
	"errors"
	"fmt"
	"html"
	"strings"
	"time"
)

// reportMailerReady 区分开发记录邮件器和已配置 SMTP，不把开发日志作为真实通知成功。
func reportMailerReady(m Mailer) bool {
	if m == nil {
		return false
	}
	switch value := m.(type) {
	case DevMailer, *DevMailer:
		return false
	case SMTPMailer:
		return strings.TrimSpace(value.Host) != ""
	case *SMTPMailer:
		return value != nil && strings.TrimSpace(value.Host) != ""
	default:
		return true
	}
}

// reportNoticeHTML 生成简明摘要，原计划名及结束原因经过 HTML 转义。
func reportNoticeHTML(r ExecutionPlanReport) string {
	var body strings.Builder
	body.WriteString("<h2>HRPlus 执行计划报告</h2><p>计划：" + html.EscapeString(r.Summary.PlanName) + "</p><p>执行日期：" + html.EscapeString(r.Summary.ExecutionDate) + "</p>")
	if r.Summary.Kind == "day_incomplete" {
		body.WriteString("<p>这个计划当天没有跑完，请调整岗位顺序或执行时间。报告不会停用周期计划；周期计划保持启用时，下一执行日从第一个岗位开始。一次性计划没有次日安排。</p>")
		if r.Summary.EndReason == "plan_never_started" {
			body.WriteString("<p>当天任务已登记排队，但未实际开始，所有岗位动作均未执行。</p>")
		}
	}
	body.WriteString("<p>结束原因：" + html.EscapeString(r.Summary.EndReason) + "</p><ul>")
	for _, item := range r.Summary.Items {
		state := map[string]string{"pending": "未开始", "running": "执行中", "completed": "完成", "failed": "失败", "stopped": "未完成或已停止"}[item.State]
		if state == "" {
			state = "待核对"
		}
		body.WriteString(fmt.Sprintf("<li>第 %d 项，状态：%s", item.Order+1, state))
		for _, action := range []string{"greeting", "auto_reply", "re_greet"} {
			if count, ok := item.Actions[action]; ok {
				label := map[string]string{"greeting": "打招呼", "auto_reply": "自动回复", "re_greet": "复打招呼"}[action]
				body.WriteString(fmt.Sprintf("；%s %d，待核对 %d", label, count.Confirmed, count.Unknown))
			}
		}
		body.WriteString("</li>")
	}
	body.WriteString("</ul><p>报告已保存在 HRPlus，可在网页查看详细结果。</p>")
	return body.String()
}

// notifyExecutionReport 只使用原报告所有者邮箱，领取后发送一次；不明确错误不自动再次发送。
func (s *ExecutionPlanService) notifyExecutionReport(ctx context.Context, tenant, email, runID string) error {
	if s.execution == nil {
		return errors.New("报告通知邮件器尚未就绪")
	}
	if !reportMailerReady(s.execution.mailer) {
		report, err := s.store.GetReport(ctx, tenant, email, runID)
		if err != nil {
			return err
		}
		if report.NotificationState == "not_configured" {
			return nil
		}
	}
	token, err := newExecutionPlanID()
	if err != nil {
		return err
	}
	report, claimed, err := s.store.ClaimReportNotification(ctx, tenant, email, runID, token)
	if err != nil || !claimed {
		return err
	}
	if !reportMailerReady(s.execution.mailer) {
		return s.store.FinishReportNotification(ctx, tenant, email, runID, token, "not_configured", "邮件服务未配置，报告已保存")
	}
	sendCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	var sendErr error
	if sender, ok := s.execution.mailer.(interface {
		SendCustomHTMLContext(context.Context, string, string, string, string) error
	}); ok {
		sendErr = sender.SendCustomHTMLContext(sendCtx, report.NotificationRecipient, "HRPlus 执行计划报告", reportNoticeHTML(report), "")
	} else {
		sendErr = s.execution.mailer.SendCustomHTML(report.NotificationRecipient, "HRPlus 执行计划报告", reportNoticeHTML(report), "")
	}
	finishCtx, finishCancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer finishCancel()
	if sendErr != nil {
		// Mailer 不区分 SMTP 已接受后断线，不能声称确定未发，也不能自动重复。
		return s.store.FinishReportNotification(finishCtx, tenant, email, runID, token, "unknown", "发送结果待核对，报告已保存")
	}
	return s.store.FinishReportNotification(finishCtx, tenant, email, runID, token, "sent", "")
}

// ClaimReportNotification 在内存锁内固定接收人及发送编号，重复报告不重复领取。
func (s *MemoryExecutionPlanStore) ClaimReportNotification(ctx context.Context, tenant, email, id, token string) (ExecutionPlanReport, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return ExecutionPlanReport{}, false, err
	}
	run, exists := s.runs[id]
	p, owned := s.plans[run.PlanID]
	r, has := s.reports[id]
	if !exists || !owned || !has || p.UserEmail != email || p.TenantID != tenant {
		return r, false, ErrNotFound
	}
	if r.NotificationState != "pending" && r.NotificationState != "not_configured" {
		return cloneExecutionReport(r), false, nil
	}
	if !executionPlanUUID.MatchString(token) {
		return r, false, ErrExecutionPlanRequest
	}
	r.NotificationState = "sending"
	r.NotificationRecipient = p.UserEmail
	r.NotificationToken = token
	started := time.Now().UTC()
	r.NotificationStartedAt = &started
	r.UpdatedAt = time.Now().UTC()
	s.reports[id] = r
	if s.reportNotifyChanged != nil {
		s.reportNotifyChanged(tenant, email)
	}
	return cloneExecutionReport(r), true, nil
}

// FinishReportNotification 只结算原发送编号，未知或未配置不被误写为通知成功。
func (s *MemoryExecutionPlanStore) FinishReportNotification(ctx context.Context, tenant, email, id, token, state, reason string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	run, exists := s.runs[id]
	p, owned := s.plans[run.PlanID]
	r, has := s.reports[id]
	if !exists || !owned || !has || p.UserEmail != email || p.TenantID != tenant {
		return ErrNotFound
	}
	if state != "sent" && state != "unknown" && state != "not_configured" {
		return ErrExecutionPlanRequest
	}
	if (r.NotificationState != "sending" && !(r.NotificationState == "unknown" && state == "sent")) || r.NotificationToken != token {
		return ErrExecutionPlanRequest
	}
	r.NotificationState = state
	r.NotificationError = reason
	r.UpdatedAt = time.Now().UTC()
	s.reports[id] = r
	if s.reportNotifyChanged != nil {
		s.reportNotifyChanged(tenant, email)
	}
	return nil
}
