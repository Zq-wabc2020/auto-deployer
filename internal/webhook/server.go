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
	"github.com/auto-deployer/auto-deployer/internal/notify"
	"github.com/auto-deployer/auto-deployer/plugins/springboot"
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

// Deployer handles the build and deploy logic for a service.
type Deployer interface {
	Build(ctx context.Context, svc *config.ServiceConfig) error
	Start(ctx context.Context, svc *config.ServiceConfig) error
	Stop(ctx context.Context, svc *config.ServiceConfig) error
	Status(ctx context.Context, svc *config.ServiceConfig) (string, error)
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

	// Dispatch to plugin for build + restart
	ctx := context.Background()
	var deployer Deployer
	switch matched.Type {
	case "springboot":
		deployer = springboot.New()
	default:
		fmt.Printf("[webhook] unknown service type: %s\n", matched.Type)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
		return
	}

	// Build
	fmt.Printf("[deploy] building %s...\n", matched.Name)
	if err := deployer.Build(ctx, matched); err != nil {
		fmt.Printf("[deploy] build failed: %v\n", err)
		if notifier := buildNotifier(cfg, result.AuthorEmail); notifier != nil {
			go func() {
				_ = notifier.NotifyDeployResult(ctx, matched.Name, result.Branch, result.AuthorEmail, "failed", err.Error())
			}()
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
		return
	}

	// Stop old instance
	fmt.Printf("[deploy] stopping %s...\n", matched.Name)
	_ = deployer.Stop(ctx, matched)

	// Start new instance
	fmt.Printf("[deploy] starting %s...\n", matched.Name)
	if err := deployer.Start(ctx, matched); err != nil {
		fmt.Printf("[deploy] start failed: %v\n", err)
		if notifier := buildNotifier(cfg, result.AuthorEmail); notifier != nil {
			go func() {
				_ = notifier.NotifyDeployResult(ctx, matched.Name, result.Branch, result.AuthorEmail, "failed", err.Error())
			}()
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
		return
	}

	fmt.Printf("[deploy] %s deployed successfully\n", matched.Name)
	if notifier := buildNotifier(cfg, result.AuthorEmail); notifier != nil {
		go func() {
			_ = notifier.NotifyDeployResult(ctx, matched.Name, result.Branch, result.AuthorEmail, "success", "")
		}()
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

// buildNotifier creates a Notifier from config.
// Always includes authorEmail as the default recipient.
// notifications.To are additional recipients appended to the list.
// Returns nil only if no notification provider (SMTP or Resend) is configured.
func buildNotifier(cfg *config.AppConfig, authorEmail string) *notify.Notifier {
	hasSMTP := cfg != nil && cfg.SMTP.Host != ""
	hasResend := cfg != nil && cfg.Resend.APIKey != ""
	if !hasSMTP && !hasResend {
		return nil
	}
	recipients := []string{authorEmail}
	recipients = append(recipients, cfg.Notifications.To...)
	return notify.New(
		cfg.SMTP.Host,
		cfg.SMTP.Port,
		cfg.SMTP.Username,
		cfg.SMTP.Token,
		cfg.SMTP.TLS,
		cfg.Resend.APIKey,
		cfg.Resend.From,
		recipients,
	)
}
