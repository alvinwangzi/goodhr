// 本文件使用回环 SMTP 协议夹具验证 HRPlus 成功、接受后丢回执和取消，不连接真实邮件服务器。
package httpapi

import (
	"bufio"
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/smtp"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// TestCustomSMTPRejectsUntrustedTLS 验证有界 SMTPS 传输保留系统证书校验，不信任测试自签证书。
func TestCustomSMTPRejectsUntrustedTLS(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("未受信任连接进入了应用层") }))
	defer server.Close()
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	mailer := SMTPMailer{Host: "127.0.0.1", Port: 465}
	err := mailer.sendCustomSMTP(ctx, server.Listener.Addr().String(), smtp.PlainAuth("", "fixture", "fixture", "127.0.0.1"), "from@fixture.invalid", "owner@fixture.invalid", "fixture")
	if err == nil {
		t.Fatal("未受信任 TLS 仍被当作发送成功")
	}
}

// reportSMTPFixture 只接受本次回环连接，记录 DATA 内容，可模拟服务器收下后丢失确认。
func reportSMTPFixture(t *testing.T, lostAck bool) (SMTPMailer, *atomic.Int32, <-chan string) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	_, port, _ := net.SplitHostPort(listener.Addr().String())
	number, _ := strconv.Atoi(port)
	accepted := &atomic.Int32{}
	bodies := make(chan string, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
		_, _ = io.WriteString(conn, "220 fixture SMTP\r\n")
		reader := bufio.NewReader(conn)
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				return
			}
			switch {
			case strings.HasPrefix(line, "EHLO") || strings.HasPrefix(line, "HELO"):
				_, _ = io.WriteString(conn, "250 fixture\r\n")
			case strings.HasPrefix(line, "MAIL FROM:") || strings.HasPrefix(line, "RCPT TO:"):
				_, _ = io.WriteString(conn, "250 accepted\r\n")
			case strings.HasPrefix(line, "DATA"):
				_, _ = io.WriteString(conn, "354 data\r\n")
				var body strings.Builder
				for {
					data, err := reader.ReadString('\n')
					if err != nil {
						return
					}
					if data == ".\r\n" {
						break
					}
					body.WriteString(data)
				}
				accepted.Add(1)
				bodies <- body.String()
				if lostAck {
					return
				}
				_, _ = io.WriteString(conn, "250 stored\r\n")
			case strings.HasPrefix(line, "QUIT"):
				_, _ = io.WriteString(conn, "221 closed\r\n")
				return
			default:
				_, _ = io.WriteString(conn, "500 unsupported\r\n")
			}
		}
	}()
	return SMTPMailer{Host: "127.0.0.1", Port: number, From: "hrplus@fixture.invalid"}, accepted, bodies
}

// TestReportSMTPProtocol 验证真实 SMTP 接受回执决定通知状态，丢失回执不会自动发送第二封。
func TestReportSMTPProtocol(t *testing.T) {
	for _, lost := range []bool{false, true} {
		t.Run(strconv.FormatBool(lost), func(t *testing.T) {
			mailer, accepted, bodies := reportSMTPFixture(t, lost)
			store := NewMemoryExecutionPlanStore()
			email := "smtp-owner@fixture.invalid"
			_, summary := endedReportFixture(t, store, email)
			if _, err := store.SaveReport(t.Context(), "", email, summary, "confirmed"); err != nil {
				t.Fatal(err)
			}
			service := &ExecutionPlanService{store: store, execution: &PositionExecutionService{mailer: mailer}}
			if err := service.notifyExecutionReport(t.Context(), "", email, summary.RunID); err != nil {
				t.Fatal(err)
			}
			body := <-bodies
			if !strings.Contains(body, "To: "+email) {
				t.Fatal("实际 SMTP 收件人不属于原报告")
			}
			report, err := store.GetReport(t.Context(), "", email, summary.RunID)
			expected := "sent"
			if lost {
				expected = "unknown"
			}
			if err != nil || report.NotificationState != expected || accepted.Load() != 1 {
				t.Fatal("SMTP 结果错误", report.NotificationState, err)
			}
			if err := service.processReportNotifications(t.Context(), time.Now()); err != nil || accepted.Load() != 1 {
				t.Fatal("SMTP 结果后再次发送", err)
			}
		})
	}
}

// TestCustomSMTPCancel 取消真实已连接但未发欢迎语的 SMTP 连接，不能永久卡住发送或后台恢复。
func TestCustomSMTPCancel(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	_, port, _ := net.SplitHostPort(listener.Addr().String())
	number, _ := strconv.Atoi(port)
	connected := make(chan struct{})
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		close(connected)
		_, _ = io.Copy(io.Discard, conn)
	}()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	result := make(chan error, 1)
	go func() {
		result <- (SMTPMailer{Host: "127.0.0.1", Port: number, From: "fixture@fixture.invalid"}).SendCustomHTMLContext(ctx, "owner@fixture.invalid", "fixture", "fixture", "")
	}()
	select {
	case <-connected:
	case <-time.After(time.Second):
		t.Fatal("隔离 SMTP 未连接")
	}
	cancel()
	select {
	case err := <-result:
		if err == nil {
			t.Fatal("取消后仍显示发送成功")
		}
	case <-time.After(time.Second):
		t.Fatal("取消未关闭实际 SMTP 连接")
	}
}
