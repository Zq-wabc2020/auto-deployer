package webhook

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/auto-deployer/auto-deployer/internal/config"
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

func TestHandle_GitHubPush_FormEncoded(t *testing.T) {
	// GitHub webhooks can be delivered as application/x-www-form-urlencoded
	// with the JSON payload in the "payload" form field.
	form := url.Values{}
	form.Set("payload", `{"ref":"refs/heads/main","repository":{"clone_url":"https://github.com/user/repo.git"}}`)
	req := httptest.NewRequest(http.MethodPost, "/webhook", strings.NewReader(form.Encode()))
	req.Header.Set("X-GitHub-Event", "push")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	rr := httptest.NewRecorder()
	Handle(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200 for form-encoded GitHub push, got %d", rr.Code)
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

func TestParsePayload_GitHubWithCommits(t *testing.T) {
	body := []byte(`{
		"ref":"refs/heads/main",
		"repository":{"clone_url":"https://github.com/user/repo.git"},
		"commits":[{"author":{"email":"dev@example.com","name":"Dev"},"committer":{"email":"committer@example.com","name":"Committer"}}]
	}`)
	result, err := ParsePayload(body, "github")
	if err != nil {
		t.Fatal(err)
	}
	if result.AuthorEmail != "dev@example.com" {
		t.Errorf("expected dev@example.com, got %s", result.AuthorEmail)
	}
}

func TestParsePayload_GiteeWithCommits(t *testing.T) {
	body := []byte(`{
		"ref":"main",
		"repository":{"git_http_url":"https://gitee.com/user/repo.git"},
		"commits":[{"author":{"email":"dev@gitee.com","name":"Dev"}}]
	}`)
	result, err := ParsePayload(body, "gitee")
	if err != nil {
		t.Fatal(err)
	}
	if result.AuthorEmail != "dev@gitee.com" {
		t.Errorf("expected dev@gitee.com, got %s", result.AuthorEmail)
	}
}

func TestParsePayload_NoCommits(t *testing.T) {
	body := []byte(`{"ref":"refs/heads/main","repository":{"clone_url":"https://github.com/user/repo.git"}}`)
	result, err := ParsePayload(body, "github")
	if err != nil {
		t.Fatal(err)
	}
	if result.AuthorEmail != "" {
		t.Errorf("expected empty author email, got %s", result.AuthorEmail)
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

func TestMatchPipelines(t *testing.T) {
	pipelines := []config.PipelineConfig{
		{Name: "app", TriggerURL: "https://github.com/u/r.git", TriggerBranches: []string{"main"}},
		{Name: "app-glob", TriggerURL: "https://github.com/u/r.git", TriggerBranches: []string{"release/*"}},
		{Name: "other-repo", TriggerURL: "https://github.com/u/other.git", TriggerBranches: []string{"main"}},
		{Name: "manual-only", TriggerURL: "", TriggerBranches: nil}, // 无 git 节点
	}
	cases := []struct {
		branch string
		want   []string
	}{
		{"main", []string{"app"}},             // 精确命中
		{"release/1.2", []string{"app-glob"}}, // glob 命中
		{"dev", nil},                          // 无命中
	}
	for _, c := range cases {
		got := MatchPipelines(pipelines, &DispatchResult{
			RepoURL: "https://github.com/u/r.git", Branch: c.branch,
		})
		var names []string
		for _, p := range got {
			names = append(names, p.Name)
		}
		if fmt.Sprint(names) != fmt.Sprint(c.want) {
			t.Errorf("branch=%s got %v want %v", c.branch, names, c.want)
		}
	}
}

func TestParsePayloadCommitMessage(t *testing.T) {
	body := []byte(`{"ref":"refs/heads/main","repository":{"clone_url":"https://github.com/u/r.git"},
		"commits":[{"id":"abc123def","message":"fix: something","author":{"email":"a@b.c"}}]}`)
	res, err := ParsePayload(body, "github")
	if err != nil {
		t.Fatal(err)
	}
	if res.Commit != "abc123def" || res.Message != "fix: something" || res.AuthorEmail != "a@b.c" {
		t.Fatalf("res=%+v", res)
	}
}
