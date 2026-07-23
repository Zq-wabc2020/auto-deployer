package webhook

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"auto-deployer/internal/config"
)

// GitHubPushPayload represents a GitHub push webhook event.
type GitHubPushPayload struct {
	Ref        string `json:"ref"`
	Repository struct {
		CloneURL string `json:"clone_url"`
	} `json:"repository"`
}

// GiteePushPayload represents a Gitee push webhook event.
type GiteePushPayload struct {
	Ref        string `json:"ref"`
	Repository struct {
		GitHTTPURL string `json:"git_http_url"`
	} `json:"repository"`
}

// DispatchResult contains the parsed dispatch information from a webhook payload.
type DispatchResult struct {
	ServiceName string
	Branch      string
	RepoURL     string
}

// Handle is the HTTP handler for webhook events from both GitHub and Gitee.
func Handle(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(r.Body)
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

	// TODO: dispatch to plugin for build + restart
	_ = matched // suppress unused warning

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
			Branch:  branch,
			RepoURL: payload.Repository.CloneURL,
		}, nil

	case "gitee":
		var payload GiteePushPayload
		if err := json.Unmarshal(body, &payload); err != nil {
			return nil, err
		}
		return &DispatchResult{
			Branch:  payload.Ref,
			RepoURL: payload.Repository.GitHTTPURL,
		}, nil

	default:
		return nil, fmt.Errorf("unknown webhook source: %s", source)
	}
}

// MatchService finds the first service whose repo URL and branch match the dispatch result.
func MatchService(services []config.ServiceConfig, result *DispatchResult) *config.ServiceConfig {
	for i := range services {
		svc := &services[i]
		if svc.Repo.URL == result.RepoURL && svc.Repo.Branch == result.Branch {
			return svc
		}
	}
	return nil
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

func loadConfig() (*config.AppConfig, error) {
	home, _ := os.UserHomeDir()
	return config.Load(filepath.Join(home, "config.yaml"))
}
