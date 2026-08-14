package webhook

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/auto-deployer/auto-deployer/internal/build"
	"github.com/auto-deployer/auto-deployer/internal/config"
	"github.com/auto-deployer/auto-deployer/internal/deploy"
	"github.com/auto-deployer/auto-deployer/internal/deployqueue"
	"github.com/auto-deployer/auto-deployer/internal/registry"
)

// GitHubPushPayload represents a GitHub push webhook event.
type GitHubPushPayload struct {
	Ref        string          `json:"ref"`
	Repository struct {
		CloneURL string `json:"clone_url"`
	} `json:"repository"`
	Commits []GitHubCommit `json:"commits"`
}

// GiteePushPayload represents a Gitee push webhook event.
type GiteePushPayload struct {
	Ref        string          `json:"ref"`
	Repository struct {
		GitHTTPURL string `json:"git_http_url"`
	} `json:"repository"`
	Commits []GitHubCommit `json:"commits"`
}

// DispatchResult contains the parsed dispatch information from a webhook payload.
type DispatchResult struct {
	ServiceName  string
	Branch       string
	RepoURL      string
	AuthorEmail  string
}

// GitSignature represents author/committer info from a webhook commit.
type GitSignature struct {
	Email string `json:"email"`
	Name  string `json:"name"`
}

// GitHubCommit represents a commit in a GitHub/Gitee push payload.
type GitHubCommit struct {
	Author    GitSignature `json:"author"`
	Committer GitSignature `json:"committer"`
}

// Handle is the HTTP handler for webhook events from both GitHub and Gitee.
func Handle(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := readBody(r)
	if err != nil {
		http.Error(w, "failed to read body", http.StatusBadRequest)
		return
	}

	source := detectSource(r)
	result, err := ParsePayload(body, source)
	if err != nil {
		fmt.Printf("[webhook] parse error: %v\n", err)
		http.Error(w, "parse error", http.StatusBadRequest)
		return
	}

	fmt.Printf("[webhook] received %s push to %s/%s\n", source, result.RepoURL, result.Branch)

	// Match against configured services
	cfg, err := loadConfig()
	if err != nil {
		fmt.Printf("[webhook] config error: %v\n", err)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
		return
	}

	matched := MatchService(cfg.Services, result)
	if matched == nil {
		fmt.Printf("[webhook] no service matched for %s/%s\n", result.RepoURL, result.Branch)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
		return
	}

	result.ServiceName = matched.Name
	fmt.Printf("[webhook] matched service: %s\n", result.ServiceName)

	// Validate service type up front so a misconfigured service doesn't keep
	// queuing undeployable tasks.
	if _, err := registry.Get(matched.Type); err != nil {
		fmt.Printf("[webhook] unknown service type: %s\n", matched.Type)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
		return
	}

	// Submit to the per-service coalescing queue. The queue serializes same-
	// service deploys and merges rapid triggers; ExecuteDeploy runs the pipeline.
	task := deployqueue.Task{
		ServiceName: matched.Name,
		Branch:      result.Branch,
		RepoURL:     result.RepoURL,
		AuthorEmail: result.AuthorEmail,
		Source:      source,
	}
	if scheduler != nil {
		scheduler.Submit(task)
		fmt.Printf("[webhook] %s deploy queued (pending: %d)\n", matched.Name, scheduler.Pending(matched.Name))
	} else {
		// Fallback when no scheduler is wired (e.g. tests): run directly.
		go func() {
			_ = ExecuteDeploy(context.Background(), task, []string{task.AuthorEmail})
		}()
		fmt.Printf("[webhook] %s deploy started (no queue)\n", matched.Name)
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

// ParsePayload parses the webhook body and extracts dispatch info.
func ParsePayload(body []byte, source string) (*DispatchResult, error) {
	switch source {
	case "github":
		var payload GitHubPushPayload
		if err := json.Unmarshal(body, &payload); err != nil {
			return nil, err
		}
		branch := strings.TrimPrefix(payload.Ref, "refs/heads/")
		return &DispatchResult{
			Branch:      branch,
			RepoURL:     payload.Repository.CloneURL,
			AuthorEmail: extractAuthorEmail(payload.Commits),
		}, nil

	case "gitee":
		var payload GiteePushPayload
		if err := json.Unmarshal(body, &payload); err != nil {
			return nil, err
		}
		branch := strings.TrimPrefix(payload.Ref, "refs/heads/")
		return &DispatchResult{
			Branch:      branch,
			RepoURL:     payload.Repository.GitHTTPURL,
			AuthorEmail: extractAuthorEmail(payload.Commits),
		}, nil

	default:
		return nil, fmt.Errorf("unknown webhook source: %s", source)
	}
}

// extractAuthorEmail returns the author email from the first commit,
// falling back to committer email if author email is empty.
func extractAuthorEmail(commits []GitHubCommit) string {
	if len(commits) == 0 {
		return ""
	}
	if email := commits[0].Author.Email; email != "" {
		return email
	}
	return commits[0].Committer.Email
}

// MatchService finds the first service whose repo URL and branch match the dispatch result.
// It normalizes both URLs to SSH format for comparison.
func MatchService(services []config.ServiceConfig, result *DispatchResult) *config.ServiceConfig {
	configURL := build.HTTPSToSSH(result.RepoURL)
	for i := range services {
		svc := &services[i]
		svcURL := build.HTTPSToSSH(svc.Repo.URL)
		if svcURL == configURL && svc.Repo.Branch == result.Branch {
			return svc
		}
	}
	return nil
}

// readBody extracts the JSON payload from the request body.
// GitHub webhooks may be delivered as application/x-www-form-urlencoded with
// the JSON in a "payload" form field, while Gitee and GitHub JSON deliveries
// send raw JSON. Both are normalized to the raw JSON bytes here so that
// ParsePayload only ever deals with JSON.
func readBody(r *http.Request) ([]byte, error) {
	if strings.Contains(r.Header.Get("Content-Type"), "application/x-www-form-urlencoded") {
		if err := r.ParseForm(); err != nil {
			return nil, err
		}
		return []byte(r.PostForm.Get("payload")), nil
	}
	return io.ReadAll(r.Body)
}

func detectSource(r *http.Request) string {
	if r.Header.Get("X-GitHub-Event") != "" {
		return "github"
	}
	if r.Header.Get("X-Gitee-Event") != "" {
		return "gitee"
	}
	ct := r.Header.Get("Content-Type")
	if strings.Contains(ct, "github") {
		return "github"
	}
	if strings.Contains(ct, "gitee") {
		return "gitee"
	}
	return "unknown"
}

var configPath = filepath.Join("/", "tmp", "placeholder.yaml")

// SetConfigPath sets the config file path for the webhook handler.
func SetConfigPath(path string) {
	configPath = path
}

func loadConfig() (*config.AppConfig, error) {
	return config.Load(configPath)
}

var scheduler *deployqueue.Scheduler

// SetScheduler sets the deploy queue scheduler used to serialize and merge
// same-service webhook deploys. If not set, Handle falls back to running the
// deploy directly (used by tests).
func SetScheduler(s *deployqueue.Scheduler) {
	scheduler = s
}

// ExecuteDeploy is the scheduler's deploy executor: it loads the config, finds
// the service by name, creates the deployer, and runs the pipeline with the
// merged operator emails as notification recipients.
func ExecuteDeploy(ctx context.Context, task deployqueue.Task, operatorEmails []string) error {
	cfg, err := loadConfig()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	var svc *config.ServiceConfig
	for i := range cfg.Services {
		if cfg.Services[i].Name == task.ServiceName {
			svc = &cfg.Services[i]
			break
		}
	}
	if svc == nil {
		return fmt.Errorf("service %s not found in config", task.ServiceName)
	}
	deployer, err := registry.Get(svc.Type)
	if err != nil {
		return err
	}
	_, err = deploy.Deploy(ctx, svc, cfg, deployer, operatorEmails)
	return err
}
