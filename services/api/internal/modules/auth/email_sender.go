package auth

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"net"
	"net/smtp"
	"strconv"
	"strings"
	"time"
)

const smtpDeliveryTimeout = 15 * time.Second

// RegistrationEmailSender delivers a newly generated registration code.
// Implementations must return only after the SMTP server accepts or rejects it.
type RegistrationEmailSender interface {
	SendRegistrationEmailVerification(ctx context.Context, recipient, code string) error
}

// SMTPConfig holds the SMTP settings supplied by the deployment environment.
// An empty Host enables the development logger instead of SMTP delivery.
type SMTPConfig struct {
	Host         string
	Port         string
	Username     string
	Password     string
	From         string
	TLSMode      string
	PlatformName string
}

type smtpRegistrationEmailSender struct {
	host         string
	port         string
	username     string
	password     string
	from         string
	tlsMode      string
	platformName string
}

type registrationEmailLogger struct{}

func (registrationEmailLogger) SendRegistrationEmailVerification(_ context.Context, _ string, _ string) error {
	return nil
}

// NewRegistrationEmailSender creates an SMTP sender when SMTP_HOST is set. It
// otherwise retains the explicit development-only logger behavior.
func NewRegistrationEmailSender(config SMTPConfig) (RegistrationEmailSender, error) {
	host := strings.TrimSpace(config.Host)
	if host == "" {
		return registrationEmailLogger{}, nil
	}
	if strings.ContainsAny(host, " \t\r\n") {
		return nil, fmt.Errorf("SMTP_HOST is invalid")
	}

	port := strings.TrimSpace(config.Port)
	if port == "" {
		port = "587"
	}
	portNumber, err := strconv.Atoi(port)
	if err != nil || portNumber < 1 || portNumber > 65535 {
		return nil, fmt.Errorf("SMTP_PORT must be between 1 and 65535")
	}

	from := normalizeEmail(config.From)
	if !validEmail(from) {
		return nil, fmt.Errorf("SMTP_FROM must be a valid email address")
	}
	username, password := strings.TrimSpace(config.Username), config.Password
	if (username == "") != (password == "") {
		return nil, fmt.Errorf("SMTP_USERNAME and SMTP_PASSWORD must be set together")
	}
	tlsMode := strings.ToLower(strings.TrimSpace(config.TLSMode))
	if tlsMode == "" {
		tlsMode = "starttls"
	}
	if tlsMode != "starttls" && tlsMode != "tls" {
		return nil, fmt.Errorf("SMTP_TLS_MODE must be starttls or tls")
	}
	platformName := strings.TrimSpace(config.PlatformName)
	if platformName == "" {
		platformName = "SOJ"
	}
	if strings.ContainsAny(platformName, "\r\n") {
		return nil, fmt.Errorf("PLATFORM_NAME is invalid")
	}

	return &smtpRegistrationEmailSender{
		host: host, port: port, username: username, password: password, from: from,
		tlsMode: tlsMode, platformName: platformName,
	}, nil
}

func (s *smtpRegistrationEmailSender) SendRegistrationEmailVerification(ctx context.Context, recipient, code string) error {
	connection, err := s.connect(ctx)
	if err != nil {
		return err
	}
	defer connection.Close()

	client, err := smtp.NewClient(connection, s.host)
	if err != nil {
		return fmt.Errorf("start SMTP client: %w", err)
	}
	defer client.Close()
	if s.tlsMode == "starttls" {
		if ok, _ := client.Extension("STARTTLS"); !ok {
			return fmt.Errorf("SMTP server does not support STARTTLS")
		}
		if err := client.StartTLS(s.tlsConfig()); err != nil {
			return fmt.Errorf("start SMTP TLS: %w", err)
		}
	}
	if s.username != "" {
		if err := client.Auth(smtp.PlainAuth("", s.username, s.password, s.host)); err != nil {
			return fmt.Errorf("authenticate SMTP: %w", err)
		}
	}
	if err := client.Mail(s.from); err != nil {
		return fmt.Errorf("set SMTP sender: %w", err)
	}
	if err := client.Rcpt(recipient); err != nil {
		return fmt.Errorf("set SMTP recipient: %w", err)
	}
	message, err := client.Data()
	if err != nil {
		return fmt.Errorf("open SMTP message: %w", err)
	}
	if _, err := message.Write([]byte(s.message(recipient, code))); err != nil {
		_ = message.Close()
		return fmt.Errorf("write SMTP message: %w", err)
	}
	if err := message.Close(); err != nil {
		return fmt.Errorf("send SMTP message: %w", err)
	}
	if err := client.Quit(); err != nil {
		return fmt.Errorf("close SMTP session: %w", err)
	}
	return nil
}

func (s *smtpRegistrationEmailSender) connect(ctx context.Context) (net.Conn, error) {
	dialer := &net.Dialer{Timeout: smtpDeliveryTimeout}
	address := net.JoinHostPort(s.host, s.port)
	var (
		connection net.Conn
		err        error
	)
	if s.tlsMode == "tls" {
		connection, err = (&tls.Dialer{NetDialer: dialer, Config: s.tlsConfig()}).DialContext(ctx, "tcp", address)
	} else {
		connection, err = dialer.DialContext(ctx, "tcp", address)
	}
	if err != nil {
		return nil, fmt.Errorf("connect SMTP: %w", err)
	}
	deadline := time.Now().Add(smtpDeliveryTimeout)
	if contextDeadline, ok := ctx.Deadline(); ok && contextDeadline.Before(deadline) {
		deadline = contextDeadline
	}
	if err := connection.SetDeadline(deadline); err != nil {
		_ = connection.Close()
		return nil, fmt.Errorf("set SMTP deadline: %w", err)
	}
	return connection, nil
}

func (s *smtpRegistrationEmailSender) tlsConfig() *tls.Config {
	return &tls.Config{MinVersion: tls.VersionTLS12, ServerName: s.host}
}

func (s *smtpRegistrationEmailSender) message(recipient, code string) string {
	subject := mimeHeader(fmt.Sprintf("%s 注册验证码", s.platformName))
	body := fmt.Sprintf("%s 的注册验证码是：%s\r\n\r\n验证码将在 10 分钟后失效。如非本人操作，请忽略此邮件。\r\n", s.platformName, code)
	return strings.Join([]string{
		"From: " + s.from,
		"To: " + recipient,
		"Subject: " + subject,
		"MIME-Version: 1.0",
		"Content-Type: text/plain; charset=UTF-8",
		"Content-Transfer-Encoding: 8bit",
		"",
		body,
	}, "\r\n")
}

func mimeHeader(value string) string {
	return "=?UTF-8?B?" + base64.StdEncoding.EncodeToString([]byte(value)) + "?="
}
