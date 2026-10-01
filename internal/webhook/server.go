package webhook

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path"
	"path/filepath"
	"strings"

	"github.com/auto-deployer/auto-deployer/internal/build"
	"github.com/auto-deployer/auto-deployer/internal/config"
	"github.com/auto-deployer/auto-deployer/internal/deployqueue"
	"github.com/auto-deployer/auto-deployer/internal/pipeline"
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
	Branch      string
	RepoURL     string
	AuthorEmail string
	Commit      string
	Message     string
}

// GitSignature represents author/committer info from a webhook commit.
type GitSignature struct {
	Email string `json:"email"`
	Name  string `json:"name"`
}

// GitHubCommit represents a commit in a GitHub/Gitee push payload.
type GitHubCommit struct {
	ID        string       `json:"id"`
	Message   string       `json:"message"`
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

	// Match against configured pipelines
	cfg, err := loadConfig()
	if err != nil {
		fmt.Printf("[webhook] config error: %v\n", err)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
		return
	}

	matched := MatchPipelines(cfg.Pipelines, result)
	if len(matched) == 0 {
		fmt.Printf("[webhook] no pipeline matched for %s/%s\n", result.RepoURL, result.Branch)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
		return
	}

	// Every matching pipeline gets its own queue entry: the same repo+branch
	// may feed multiple pipelines (e.g. a jvm backend and a static frontend
	// from one monorepo branch), and coalescing is per-pipeline, so they
	// deploy independently without merging into each other.
	for _, m := range matched {
		fmt.Printf("[webhook] matched pipeline: %s\n", m.Name)

		// Submit to the per-pipeline coalescing queue. The queue serializes
		// same-pipeline deploys and merges rapid triggers; ExecutePipeline runs
		// the pipeline.
		task := deployqueue.Task{
			PipelineName: m.Name,
			Branch:       result.Branch,
			RepoURL:      result.RepoURL,
			AuthorEmail:  result.AuthorEmail,
			Commit:       result.Commit,
			Message:      result.Message,
			Source:       source,
		}
		if scheduler != nil {
			scheduler.Submit(task)
			fmt.Printf("[webhook] %s deploy queued (pending: %d)\n", m.Name, scheduler.Pending(m.Name))
		} else {
			// Fallback when no scheduler is wired (e.g. tests): run directly.
			go func(t deployqueue.Task) {
				_ = ExecutePipeline(context.Background(), t, []string{t.AuthorEmail})
			}(task)
			fmt.Printf("[webhook] %s deploy started (no queue)\n", m.Name)
		}
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
			Commit:      extractCommit(payload.Commits),
			Message:     extractMessage(payload.Commits),
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
			Commit:      extractCommit(payload.Commits),
			Message:     extractMessage(payload.Commits),
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

// extractCommit 返回第一个 commit 的 id（GitHub/Gitee 同构字段）。
func extractCommit(commits []GitHubCommit) string {
	if len(commits) == 0 {
		return ""
	}
	return commits[0].ID
}

// extractMessage 返回第一个 commit 的提交说明。
func extractMessage(commits []GitHubCommit) string {
	if len(commits) == 0 {
		return ""
	}
	return commits[0].Message
}

// MatchPipelines 返回 URL（双方 HTTPSToSSH 归一化）与 branch（path.Match，
// 无通配符即精确）命中的所有工作项——monorepo 一 push 多工作项各自独立入队。
func MatchPipelines(pipelines []config.PipelineConfig, result *DispatchResult) []*config.PipelineConfig {
	pushURL := build.HTTPSToSSH(result.RepoURL)
	var matched []*config.PipelineConfig
	for i := range pipelines {
		p := &pipelines[i]
		if p.TriggerURL == "" {
			continue // 无 git 节点，仅手动
		}
		if build.HTTPSToSSH(p.TriggerURL) != pushURL {
			continue
		}
		for _, pattern := range p.TriggerBranches {
			if ok, _ := path.Match(pattern, result.Branch); ok {
				matched = append(matched, p)
				break
			}
		}
	}
	return matched
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

// ExecutePipeline is the scheduler's deploy executor: it loads the config, finds
// the pipeline by name, and runs it with the merged operator emails as
// notification recipients. A failed/cancelled run is recorded in the runstate
// file by pipeline.Run itself, so only load/lookup errors are returned here.
func ExecutePipeline(ctx context.Context, task deployqueue.Task, operatorEmails []string) error {
	cfg, err := loadConfig()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	pl := config.FindPipeline(cfg, task.PipelineName)
	if pl == nil {
		return fmt.Errorf("工作项 %s 不在配置中", task.PipelineName)
	}
	pipeline.Run(ctx, pl, pipeline.Options{
		Trigger: "webhook", Pushers: operatorEmails, Branch: task.Branch,
		Commit: task.Commit, Author: task.AuthorEmail, Message: task.Message, Cfg: cfg,
	})
	return nil
}
