package notify

import (
	"bytes"
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
}

// New creates a Notifier.
// If resend.APIKey is set, it uses Resend API; otherwise falls back to SMTP.
// 收件人不再固化在构造期：Send 按次传入（email 组件的 to 是参数）。
func New(smtpHost string, smtpPort int, smtpUsername, smtpToken string, smtpTLS bool,
	resendToken, resendFrom string) *Notifier {
	provider := "smtp"
	if resendToken != "" {
		provider = "resend"
	}
	return &Notifier{
		provider:    provider,
		smtpHost:    smtpHost,
		smtpPort:    smtpPort,
		username:    smtpUsername,
		token:       smtpToken,
		tls:         smtpTLS,
		resendToken: resendToken,
		resendFrom:  resendFrom,
	}
}

// Send emails the given subject and HTML body to the given recipients.
func (n *Notifier) Send(to []string, subject, body string) error {
	if len(to) == 0 {
		return fmt.Errorf("收件人列表为空")
	}
	switch n.provider {
	case "resend":
		return n.sendResend(to, subject, body)
	default:
		return n.sendSMTP(to, subject, body)
	}
}

// sendSMTP sends via SMTP (SSL/TLS).
func (n *Notifier) sendSMTP(to []string, subject, body string) error {
	recipients := to
	// 空用户名 = 无认证中继，不尝试 AUTH（smtp.SendMail 对 nil auth 也会跳过）。
	var auth smtp.Auth
	if n.username != "" {
		auth = smtp.PlainAuth("", n.username, n.token, n.smtpHost)
	}
	addr := fmt.Sprintf("%s:%d", n.smtpHost, n.smtpPort)

	msg := n.buildMessage(to, subject, body)

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

		if auth != nil {
			if err := client.Auth(auth); err != nil {
				return fmt.Errorf("SMTP auth: %w", err)
			}
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

	validRecipients := make([]string, 0, len(recipients))
	for _, r := range recipients {
		if r != "" {
			validRecipients = append(validRecipients, r)
		}
	}

	if err := smtp.SendMail(addr, auth, n.username, validRecipients, []byte(msg)); err != nil {
		return fmt.Errorf("send email: %w", err)
	}
	return nil
}

// sendResend sends via Resend HTTP API.
func (n *Notifier) sendResend(to []string, subject, body string) error {
	validRecipients := make([]string, 0, len(to))
	for _, r := range to {
		if r != "" {
			validRecipients = append(validRecipients, r)
		}
	}
	if len(validRecipients) == 0 {
		return nil
	}

	reqBody := map[string]interface{}{
		"from":    n.resendFrom,
		"to":      validRecipients,
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

func (n *Notifier) buildMessage(to []string, subject, body string) string {
	var sb strings.Builder
	sb.WriteString("From: ")
	sb.WriteString(n.username)
	sb.WriteString("\r\n")
	sb.WriteString("To: ")
	sb.WriteString(strings.Join(to, ", "))
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
