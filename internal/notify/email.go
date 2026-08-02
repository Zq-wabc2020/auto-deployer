package notify

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net/http"
	"net/smtp"
	"strings"
	"time"
)

// Notifier sends email notifications.
// It supports two providers: SMTP and Resend (HTTP API).
// The provider is selected based on configuration at construction time.
type Notifier struct {
	provider string // "smtp" or "resend"

	// SMTP fields
	smtpHost string
	smtpPort int
	username string
	token    string
	tls      bool

	// Resend fields
	resendToken string
	resendFrom  string

	// Common fields
	to []string
}

// New creates a Notifier.
// If resend.APIKey is set, it uses Resend API; otherwise falls back to SMTP.
func New(smtpHost string, smtpPort int, smtpUsername, smtpToken string, smtpTLS bool,
	resendToken, resendFrom string, to []string) *Notifier {
	provider := "smtp"
	if resendToken != "" {
		provider = "resend"
	}
	return &Notifier{
		provider: provider,
		smtpHost: smtpHost,
		smtpPort: smtpPort,
		username: smtpUsername,
		token:    smtpToken,
		tls:      smtpTLS,
		resendToken: resendToken,
		resendFrom:  resendFrom,
		to:       to,
	}
}

// Send emails the given subject and HTML body to all configured recipients.
func (n *Notifier) Send(_ context.Context, subject, body string) error {
	switch n.provider {
	case "resend":
		return n.sendResend(subject, body)
	default:
		return n.sendSMTP(subject, body)
	}
}

// sendSMTP sends via SMTP (SSL/TLS).
func (n *Notifier) sendSMTP(subject, body string) error {
	recipients := n.to
	auth := smtp.PlainAuth("", n.username, n.username, n.token)
	addr := fmt.Sprintf("%s:%d", n.smtpHost, n.smtpPort)

	msg := n.buildMessage(subject, body)

	if n.tls || n.smtpPort == 465 {
		tlsConf := &tls.Config{InsecureSkipVerify: false}
		conn, err := tls.Dial("tcp", addr, tlsConf)
		if err != nil {
			return fmt.Errorf("TLS dial: %w", err)
		}
		client, err := smtp.NewClient(conn, n.smtpHost)
		if err != nil {
			return fmt.Errorf("SMTP new client: %w", err)
		}
		defer client.Close()

		if err := client.Auth(auth); err != nil {
			return fmt.Errorf("SMTP auth: %w", err)
		}
		if err := client.Mail(n.username); err != nil {
			return fmt.Errorf("SMTP mail: %w", err)
		}
		for _, r := range recipients {
			if r == "" {
				continue
			}
			if err := client.Rcpt(r); err != nil {
				return fmt.Errorf("SMTP rcpt %s: %w", r, err)
			}
		}
		w, err := client.Data()
		if err != nil {
			return fmt.Errorf("SMTP data: %w", err)
		}
		_, err = w.Write([]byte(msg))
		if err != nil {
			return fmt.Errorf("SMTP write data: %w", err)
		}
		w.Close()
		return client.Quit()
	}

	if err := smtp.SendMail(addr, auth, n.username, recipients, []byte(msg)); err != nil {
		return fmt.Errorf("send email: %w", err)
	}
	return nil
}

// sendResend sends via Resend HTTP API.
func (n *Notifier) sendResend(subject, body string) error {
	recipients := n.to

	reqBody := map[string]interface{}{
		"from":    n.resendFrom,
		"to":      recipients,
		"subject": subject,
		"html":    body,
	}

	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("marshal resend request: %w", err)
	}

	url := "https://api.resend.com/emails"
	req, err := http.NewRequest("POST", url, bytes.NewReader(jsonData))
	if err != nil {
		return fmt.Errorf("create resend request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+n.resendToken)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	client := &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: false},
		},
	}

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("resend request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		buf := new(bytes.Buffer)
		buf.ReadFrom(resp.Body)
		return fmt.Errorf("resend API error: status=%d body=%s", resp.StatusCode, buf.String())
	}

	return nil
}

// NotifyDeployResult assembles and sends a deployment result email.
// Recipients are set by buildNotifier: authorEmail is the default recipient,
// notifications.to are additional recipients.
func (n *Notifier) NotifyDeployResult(ctx context.Context, svcName, branch, authorEmail, status, errMsg string) error {
	subject := n.buildSubject(svcName, status)
	body := n.buildBody(svcName, branch, authorEmail, status, errMsg)
	return n.Send(ctx, subject, body)
}

func (n *Notifier) buildSubject(svcName, status string) string {
	if status == "failed" {
		return fmt.Sprintf("[deployd] ❌ 部署失败: %s", svcName)
	}
	return fmt.Sprintf("[deployd] ✅ 部署成功: %s", svcName)
}

func (n *Notifier) buildBody(svcName, branch, authorEmail, status, errMsg string) string {
	ts := time.Now().Format("2006-01-02 15:04:05")
	var sb strings.Builder

	sb.WriteString("<html><body style='font-family: sans-serif;'>")
	sb.WriteString("<h2>部署通知</h2>")
	sb.WriteString("<table border='0' cellpadding='4' cellspacing='0' style='border-collapse: collapse;'>")
	sb.WriteString(n.row("服务名", svcName))
	sb.WriteString(n.row("分支", branch))
	sb.WriteString(n.row("状态", status))
	sb.WriteString(n.row("时间", ts))
	if authorEmail != "" {
		sb.WriteString(n.row("变更者", authorEmail))
	}
	if status == "failed" {
		sb.WriteString(n.row("失败阶段", "未知"))
		sb.WriteString(n.row("错误信息", errMsg))
	}
	sb.WriteString("</table>")
	sb.WriteString("</body></html>")

	return sb.String()
}

func (n *Notifier) row(label, value string) string {
	return fmt.Sprintf("<tr><td style='padding:4px 8px;border:1px solid #ddd;background:#f5f5f5;font-weight:bold;'>%s</td><td style='padding:4px 8px;border:1px solid #ddd;'>%s</td></tr>", label, value)
}

func (n *Notifier) buildMessage(subject, body string) string {
	var sb strings.Builder
	sb.WriteString("From: ")
	sb.WriteString(n.username)
	sb.WriteString("\r\n")
	sb.WriteString("To: ")
	sb.WriteString(strings.Join(n.to, ", "))
	sb.WriteString("\r\n")
	sb.WriteString("Subject: ")
	sb.WriteString(subject)
	sb.WriteString("\r\n")
	sb.WriteString("MIME-Version: 1.0\r\n")
	sb.WriteString("Content-Type: text/html; charset=UTF-8\r\n")
	sb.WriteString("\r\n")
	sb.WriteString(body)
	return sb.String()
}
