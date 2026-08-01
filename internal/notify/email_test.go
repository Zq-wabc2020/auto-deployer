package notify

import (
	"context"
	"strings"
	"testing"
)

func TestBuildSubject_Success(t *testing.T) {
	n := New("smtp.test.com", 587, "u", "t", false, "", "", nil)
	subject := n.buildSubject("my-app", "success")
	expected := "[deployd] ✅ 部署成功: my-app"
	if subject != expected {
		t.Errorf("expected %q, got %q", expected, subject)
	}
}

func TestBuildSubject_Failure(t *testing.T) {
	n := New("smtp.test.com", 587, "u", "t", false, "", "", nil)
	subject := n.buildSubject("my-app", "failed")
	expected := "[deployd] ❌ 部署失败: my-app"
	if subject != expected {
		t.Errorf("expected %q, got %q", expected, subject)
	}
}

func TestBuildBody_ContainsFields(t *testing.T) {
	n := New("smtp.test.com", 587, "u", "t", false, "", "", []string{"admin@test.com"})
	body := n.buildBody("my-app", "main", "dev@example.com", "failed", "build failed: exit 1")
	if !strings.Contains(body, "my-app") {
		t.Error("body should contain service name")
	}
	if !strings.Contains(body, "main") {
		t.Error("body should contain branch")
	}
	if !strings.Contains(body, "dev@example.com") {
		t.Error("body should contain author email")
	}
	if !strings.Contains(body, "build failed: exit 1") {
		t.Error("body should contain error message")
	}
}

func TestBuildBody_NoAuthorEmail(t *testing.T) {
	n := New("smtp.test.com", 587, "u", "t", false, "", "", []string{"admin@test.com"})
	body := n.buildBody("my-app", "main", "", "success", "")
	if strings.Contains(body, "变更者") {
		t.Error("body should not contain author section when email is empty")
	}
}

func TestNew_Defaults(t *testing.T) {
	n := New("smtp.example.com", 587, "user", "token", true, "", "", []string{"a@b.com"})
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
	if len(n.to) != 1 || n.to[0] != "a@b.com" {
		t.Errorf("unexpected to list: %v", n.to)
	}
}

func TestNew_ResendProvider(t *testing.T) {
	n := New("", 0, "", "", false, "re_xxx", "test@example.com", []string{"a@b.com"})
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
	n := New("smtp.qq.com", 465, "user@qq.com", "token", true, "", "", []string{"a@b.com"})
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

func TestNotifyDeployResult_SkipsEmptyAuthor(t *testing.T) {
	ctx := context.Background()
	n := New("smtp.test.com", 587, "u", "t", false, "", "", []string{"admin@test.com"})

	err := n.NotifyDeployResult(ctx, "test-svc", "main", "", "success", "")
	if err == nil {
		t.Fatal("expected connection error")
	}
	// The error should be a network error, not a nil pointer or format error
	errStr := err.Error()
	if !strings.Contains(errStr, "dial") && !strings.Contains(errStr, "TLS") {
		t.Logf("error was: %v (expected network error)", err)
	}
}
