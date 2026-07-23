package webhook

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandle_GitHubPush(t *testing.T) {
	body := `{"ref":"refs/heads/main","repository":{"clone_url":"https://github.com/user/repo.git"}}`
	req := httptest.NewRequest(http.MethodPost, "/webhook", strings.NewReader(body))
	req.Header.Set("X-GitHub-Event", "push")
	req.Header.Set("Content-Type", "application/json")

	rr := httptest.NewRecorder()
	Handle(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}
}

func TestHandle_GiteePush(t *testing.T) {
	body := `{"ref":"main","repository":{"git_http_url":"https://gitee.com/user/repo.git"}}`
	req := httptest.NewRequest(http.MethodPost, "/webhook", strings.NewReader(body))
	req.Header.Set("X-Gitee-Event", "Push Hook")
	req.Header.Set("Content-Type", "application/json")

	rr := httptest.NewRecorder()
	Handle(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}
}

func TestHandle_MethodNotAllowed(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/webhook", nil)
	rr := httptest.NewRecorder()
	Handle(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", rr.Code)
	}
}

func TestParsePayload_GitHub(t *testing.T) {
	body := []byte(`{"ref":"refs/heads/develop","repository":{"clone_url":"https://github.com/user/repo.git"}}`)
	result, err := ParsePayload(body, "github")
	if err != nil {
		t.Fatal(err)
	}
	if result.Branch != "develop" {
		t.Errorf("expected branch develop, got %s", result.Branch)
	}
	if result.RepoURL != "https://github.com/user/repo.git" {
		t.Errorf("unexpected repo URL: %s", result.RepoURL)
	}
}

func TestParsePayload_Gitee(t *testing.T) {
	body := []byte(`{"ref":"master","repository":{"git_http_url":"https://gitee.com/user/repo.git"}}`)
	result, err := ParsePayload(body, "gitee")
	if err != nil {
		t.Fatal(err)
	}
	if result.Branch != "master" {
		t.Errorf("expected branch master, got %s", result.Branch)
	}
}

func TestParsePayload_UnknownSource(t *testing.T) {
	body := []byte("{}")
	_, err := ParsePayload(body, "unknown")
	if err == nil {
		t.Fatal("expected error for unknown source")
	}
}

func TestDetectSource_GitHub(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.Header.Set("X-GitHub-Event", "push")
	if detectSource(req) != "github" {
		t.Error("expected github source")
	}
}

func TestDetectSource_Gitee(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.Header.Set("X-Gitee-Event", "Push Hook")
	if detectSource(req) != "gitee" {
		t.Error("expected gitee source")
	}
}
