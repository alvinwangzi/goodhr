// 本文件为 HRPlus 自定义邮件提供标准 SMTP/SMTPS 有界传输，不关闭 TLS 校验或自动重试结果不明的发送。
package httpapi

import (
	"context"
	"crypto/tls"
	"net"
	"net/smtp"
	"time"
)

// sendCustomSMTP 连接、握手和协议读写共用期限，兼容原 465 和 STARTTLS 行为。
func (m SMTPMailer) sendCustomSMTP(ctx context.Context, addr string, auth smtp.Auth, from, to, message string) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	conn, err := (&net.Dialer{Timeout: 10 * time.Second}).DialContext(ctx, "tcp", addr)
	if err != nil {
		return err
	}
	defer conn.Close()
	rawConn := conn
	stop := context.AfterFunc(ctx, func() { _ = rawConn.Close() })
	defer stop()
	if deadline, ok := ctx.Deadline(); ok {
		if err = conn.SetDeadline(deadline); err != nil {
			return err
		}
	}
	if m.Port == 465 {
		secure := tls.Client(conn, &tls.Config{MinVersion: tls.VersionTLS12, ServerName: m.Host})
		if err = secure.HandshakeContext(ctx); err != nil {
			return err
		}
		conn = secure
	}
	client, err := smtp.NewClient(conn, m.Host)
	if err != nil {
		return err
	}
	defer client.Close()
	if m.Port != 465 {
		if ok, _ := client.Extension("STARTTLS"); ok {
			if err = client.StartTLS(&tls.Config{MinVersion: tls.VersionTLS12, ServerName: m.Host}); err != nil {
				return err
			}
		}
	}
	if m.Port == 465 {
		if err = client.Auth(auth); err != nil {
			return err
		}
	} else if ok, _ := client.Extension("AUTH"); ok {
		if err = client.Auth(auth); err != nil {
			return err
		}
	}
	if err = client.Mail(from); err != nil {
		return err
	}
	if err = client.Rcpt(to); err != nil {
		return err
	}
	writer, err := client.Data()
	if err != nil {
		return err
	}
	if _, err = writer.Write([]byte(message)); err != nil {
		_ = writer.Close()
		return err
	}
	if err = writer.Close(); err != nil {
		return err
	}
	return client.Quit()
}
