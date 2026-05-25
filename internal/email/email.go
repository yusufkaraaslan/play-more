package email

import (
	"crypto/tls"
	"fmt"
	"net"
	"net/smtp"
	"strconv"
	"strings"
	"time"
)

// sanitizeHeader strips CRLF from header values to prevent SMTP header injection.
func sanitizeHeader(s string) string {
	s = strings.ReplaceAll(s, "\r", "")
	s = strings.ReplaceAll(s, "\n", "")
	return s
}

type Config struct {
	Host    string
	Port    int
	User    string
	Pass    string
	From    string
	BaseURL string
}

// CurrentConfig is the active email configuration. Set it during startup.
var CurrentConfig *Config

func Configured() bool {
	return CurrentConfig != nil && CurrentConfig.Host != "" && CurrentConfig.From != ""
}

// HealthCheck tests TCP connectivity to the configured SMTP server.
// Returns nil if reachable, an error otherwise.
func HealthCheck() error {
	cfg := CurrentConfig
	if cfg == nil || cfg.Host == "" || cfg.From == "" {
		return fmt.Errorf("SMTP not configured")
	}
	addr := net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port))
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
	if CurrentConfig == nil {
		return false
	}
	return CurrentConfig.Host == "127.0.0.1" || CurrentConfig.Host == "localhost" || CurrentConfig.Host == "::1"
}

func Send(to, subject, body string) error {
	cfg := CurrentConfig
	if cfg == nil || cfg.Host == "" || cfg.From == "" {
		return fmt.Errorf("SMTP not configured")
	}

	msg := fmt.Sprintf("From: %s\r\nTo: %s\r\nSubject: %s\r\nMIME-Version: 1.0\r\nContent-Type: text/html; charset=UTF-8\r\n\r\n%s",
		sanitizeHeader(cfg.From), sanitizeHeader(to), sanitizeHeader(subject), body)

	addr := net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port))

	// Always go through our custom path so we can enforce STARTTLS. The
	// stdlib smtp.SendMail will silently fall back to plaintext if the server
	// does not advertise STARTTLS — unacceptable for credential transit.
	// Local bridges use a self-signed cert (InsecureSkipVerify=true).
	return sendWithSTARTTLS(addr, to, []byte(msg), IsLocalBridge())
}

// sendWithSTARTTLS connects, requires STARTTLS (aborting if the server doesn't
// advertise it), authenticates, and sends. Set localBridge=true to skip
// hostname verification on self-signed certs (ProtonMail Bridge etc.).
func sendWithSTARTTLS(addr, to string, msg []byte, localBridge bool) error {
	cfg := CurrentConfig
	conn, err := net.DialTimeout("tcp", addr, 10*time.Second)
	if err != nil {
		return err
	}
	c, err := smtp.NewClient(conn, cfg.Host)
	if err != nil {
		return err
	}
	defer c.Close()

	if ok, _ := c.Extension("STARTTLS"); !ok {
		return fmt.Errorf("SMTP server does not support STARTTLS — refusing to send credentials in plaintext")
	}
	tlsConfig := &tls.Config{
		ServerName:         cfg.Host,
		InsecureSkipVerify: localBridge,
		MinVersion:         tls.VersionTLS13,
	}
	if err := c.StartTLS(tlsConfig); err != nil {
		return err
	}
	auth := smtp.PlainAuth("", cfg.User, cfg.Pass, cfg.Host)
	if err := c.Auth(auth); err != nil {
		return err
	}
	if err := c.Mail(cfg.From); err != nil {
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
	cfg := CurrentConfig
	link := cfg.BaseURL + "/#verify/" + token
	body := fmt.Sprintf(`<h2>Welcome to PlayMore, %s!</h2>
<p>Please verify your email address by clicking the link below:</p>
<p><a href="%s" style="display:inline-block;padding:12px 24px;background:#66c0f4;color:#fff;text-decoration:none;border-radius:4px;">Verify Email</a></p>
<p>Or copy this link: %s</p>
<p style="color:#888;font-size:12px;">This link expires in 24 hours.</p>`,
		username, link, link)
	return Send(to, "Verify your PlayMore email", body)
}

func SendPasswordReset(to, username, token string) error {
	cfg := CurrentConfig
	link := cfg.BaseURL + "/#reset/" + token
	body := fmt.Sprintf(`<h2>Password Reset</h2>
<p>Hi %s, you requested a password reset for your PlayMore account.</p>
<p><a href="%s" style="display:inline-block;padding:12px 24px;background:#66c0f4;color:#fff;text-decoration:none;border-radius:4px;">Reset Password</a></p>
<p>Or copy this link: %s</p>
<p style="color:#888;font-size:12px;">This link expires in 1 hour. If you didn't request this, ignore this email.</p>`,
		username, link, link)
	return Send(to, "PlayMore password reset", body)
}
