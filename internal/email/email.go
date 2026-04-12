package email

import (
	"fmt"
	"net/smtp"
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

func Send(to, subject, body string) error {
	if !Configured() {
		return fmt.Errorf("SMTP not configured")
	}

	msg := fmt.Sprintf("From: %s\r\nTo: %s\r\nSubject: %s\r\nMIME-Version: 1.0\r\nContent-Type: text/html; charset=UTF-8\r\n\r\n%s",
		From, to, subject, body)

	addr := fmt.Sprintf("%s:%d", Host, Port)
	auth := smtp.PlainAuth("", User, Pass, Host)
	return smtp.SendMail(addr, auth, From, []string{to}, []byte(msg))
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
