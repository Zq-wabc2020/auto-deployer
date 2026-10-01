package notify

import (
	"strings"
	"testing"
)

func TestNew_Defaults(t *testing.T) {
	n := New("smtp.example.com", 587, "user", "token", true, "", "")
	if n.provider != "smtp" {
		t.Errorf("expected provider smtp, got %s", n.provider)
	}
	if n.smtpHost != "smtp.example.com" {
		t.Errorf("unexpected host: %s", n.smtpHost)
	}
	if n.smtpPort != 587 {
		t.Errorf("unexpected port: %d", n.smtpPort)
	}
	if !n.tls {
		t.Error("expected TLS enabled")
	}
}

func TestNew_ResendProvider(t *testing.T) {
	n := New("", 0, "", "", false, "re_xxx", "test@example.com")
	if n.provider != "resend" {
		t.Errorf("expected provider resend, got %s", n.provider)
	}
	if n.resendToken != "re_xxx" {
		t.Errorf("unexpected resend token: %s", n.resendToken)
	}
	if n.resendFrom != "test@example.com" {
		t.Errorf("unexpected resend from: %s", n.resendFrom)
	}
}

func TestNew_SMTPProvider(t *testing.T) {
	n := New("smtp.qq.com", 465, "user@qq.com", "token", true, "", "")
	if n.provider != "smtp" {
		t.Errorf("expected provider smtp, got %s", n.provider)
	}
	if n.smtpHost != "smtp.qq.com" {
		t.Errorf("unexpected host: %s", n.smtpHost)
	}
	if n.smtpPort != 465 {
		t.Errorf("unexpected port: %d", n.smtpPort)
	}
	if n.username != "user@qq.com" {
		t.Errorf("unexpected username: %s", n.username)
	}
	if n.token != "token" {
		t.Errorf("unexpected token: %s", n.token)
	}
	if !n.tls {
		t.Error("expected TLS enabled")
	}
}

// 空收件人必须报错（不再静默跳过）。
func TestSend_EmptyRecipients(t *testing.T) {
	n := New("smtp.test.com", 587, "u", "t", false, "", "")
	err := n.Send(nil, "subject", "body")
	if err == nil || !strings.Contains(err.Error(), "收件人") {
		t.Fatalf("expected empty-recipients error, got %v", err)
	}
}
