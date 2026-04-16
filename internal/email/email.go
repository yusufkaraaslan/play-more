package email

import (
	"crypto/tls"
	"fmt"
	"net"
	"net/smtp"
	"time"
)

var (
	Host    string
	Port    int
	User    string
	Pass    string
	From    string
	BaseURL string
)

func Configured() bool {
	return Host != "" && From != ""
}

// HealthCheck tests TCP connectivity to the configured SMTP server.
// Returns nil if reachable, an error otherwise.
func HealthCheck() error {
	if !Configured() {
		return fmt.Errorf("SMTP not configured")
	}
	addr := fmt.Sprintf("%s:%d", Host, Port)
	conn, err := net.DialTimeout("tcp", addr, 3*time.Second)
	if err != nil {
		return err
	}
	conn.Close()
	return nil
}

// IsLocalBridge reports whether the configured SMTP host is a localhost bridge
// (ProtonMail Bridge, local SMTP relay, etc.)
func IsLocalBridge() bool {
	return Host == "127.0.0.1" || Host == "localhost" || Host == "::1"
}

func Send(to, subject, body string) error {
	if !Configured() {
		return fmt.Errorf("SMTP not configured")
	}

	msg := fmt.Sprintf("From: %s\r\nTo: %s\r\nSubject: %s\r\nMIME-Version: 1.0\r\nContent-Type: text/html; charset=UTF-8\r\n\r\n%s",
		From, to, subject, body)

	addr := fmt.Sprintf("%s:%d", Host, Port)

	// For local bridges (ProtonMail Bridge, etc.) use custom TLS config
	// to accept self-signed certificates
	if IsLocalBridge() {
		return sendWithCustomTLS(addr, to, []byte(msg))
	}

	auth := smtp.PlainAuth("", User, Pass, Host)
	return smtp.SendMail(addr, auth, From, []string{to}, []byte(msg))
}

func sendWithCustomTLS(addr, to string, msg []byte) error {
	conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		return err
	}
	c, err := smtp.NewClient(conn, Host)
	if err != nil {
		return err
	}
	defer c.Close()

	// STARTTLS with self-signed cert accepted
	tlsConfig := &tls.Config{
		InsecureSkipVerify: true,
		ServerName:         Host,
	}
	if err := c.StartTLS(tlsConfig); err != nil {
		return err
	}
	auth := smtp.PlainAuth("", User, Pass, Host)
	if err := c.Auth(auth); err != nil {
		return err
	}
	if err := c.Mail(From); err != nil {
		return err
	}
	if err := c.Rcpt(to); err != nil {
		return err
	}
	w, err := c.Data()
	if err != nil {
		return err
	}
	if _, err := w.Write(msg); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return c.Quit()
}

func SendVerification(to, username, token string) error {
	link := BaseURL + "/#verify/" + token
	body := fmt.Sprintf(`<h2>Welcome to PlayMore, %s!</h2>
<p>Please verify your email address by clicking the link below:</p>
<p><a href="%s" style="display:inline-block;padding:12px 24px;background:#66c0f4;color:#fff;text-decoration:none;border-radius:4px;">Verify Email</a></p>
<p>Or copy this link: %s</p>
<p style="color:#888;font-size:12px;">This link expires in 24 hours.</p>`,
		username, link, link)
	return Send(to, "Verify your PlayMore email", body)
}

func SendPasswordReset(to, username, token string) error {
	link := BaseURL + "/#reset/" + token
	body := fmt.Sprintf(`<h2>Password Reset</h2>
<p>Hi %s, you requested a password reset for your PlayMore account.</p>
<p><a href="%s" style="display:inline-block;padding:12px 24px;background:#66c0f4;color:#fff;text-decoration:none;border-radius:4px;">Reset Password</a></p>
<p>Or copy this link: %s</p>
<p style="color:#888;font-size:12px;">This link expires in 1 hour. If you didn't request this, ignore this email.</p>`,
		username, link, link)
	return Send(to, "PlayMore password reset", body)
}
