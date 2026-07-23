package e2e

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"auto-deployer/internal/webhook"
)

func TestWebhook_ParsesGitHubPayload(t *testing.T) {
	body := `{"ref":"refs/heads/main","repository":{"clone_url":"https://github.com/user/repo.git"}}`
	req := httptest.NewRequest(http.MethodPost, "/webhook", strings.NewReader(body))
	req.Header.Set("X-GitHub-Event", "push")
	req.Header.Set("Content-Type", "application/json")

	result, err := webhook.ParsePayload([]byte(body), "github")
	if err != nil {
		t.Fatal(err)
	}
	if result.Branch != "main" {
		t.Errorf("expected branch main, got %s", result.Branch)
	}
	if result.RepoURL != "https://github.com/user/repo.git" {
		t.Errorf("unexpected repo URL: %s", result.RepoURL)
	}
}

func TestWebhook_ParsesGiteePayload(t *testing.T) {
	body := `{"ref":"develop","repository":{"git_http_url":"https://gitee.com/user/repo.git"}}`
	req := httptest.NewRequest(http.MethodPost, "/webhook", strings.NewReader(body))
	req.Header.Set("X-Gitee-Event", "Push Hook")
	req.Header.Set("Content-Type", "application/json")

	result, err := webhook.ParsePayload([]byte(body), "gitee")
	if err != nil {
		t.Fatal(err)
	}
	if result.Branch != "develop" {
		t.Errorf("expected branch develop, got %s", result.Branch)
	}
}

func TestWebhook_HandlerReturns200(t *testing.T) {
	body := `{"ref":"refs/heads/main","repository":{"clone_url":"https://github.com/user/repo.git"}}`
	req := httptest.NewRequest(http.MethodPost, "/webhook", strings.NewReader(body))
	req.Header.Set("X-GitHub-Event", "push")

	rr := httptest.NewRecorder()
	webhook.Handle(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}
}
