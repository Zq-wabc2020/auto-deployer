package webhook

import (
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash"
	"io"
	"net/http"
	"net/url"
	"path"
	"path/filepath"
	"strings"

	"github.com/auto-deployer/auto-deployer/internal/build"
	"github.com/auto-deployer/auto-deployer/internal/config"
	"github.com/auto-deployer/auto-deployer/internal/deployqueue"
	"github.com/auto-deployer/auto-deployer/internal/pipeline"
)

// Webhook 签名校验用请求头。
const (
	githubSig256Header   = "X-Hub-Signature-256"
	githubSigHeader      = "X-Hub-Signature"
	giteeTokenHeader     = "X-Gitee-Token"
	giteeSigHeader       = "X-Gitee-Signature"
	giteeTimestampHeader = "X-Gitee-Timestamp"
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

	// 配置必须先加载：签名校验需要 webhook.secret，且配置损坏不能再按
	// 「宽松 200」吞掉（让 GitHub/Gitee 平台侧看到 5xx 并重试）。
	cfg, err := loadConfig()
	if err != nil {
		fmt.Printf("[webhook] config error: %v\n", err)
		http.Error(w, "config error", http.StatusInternalServerError)
		return
	}

	raw, jsonBody, err := readRawAndJSON(r)
	if err != nil {
		http.Error(w, "failed to read body", http.StatusBadRequest)
		return
	}

	source := detectSource(r)
	if err := verifySecret(r, raw, source, cfg.Webhook.Secret); err != nil {
		fmt.Printf("[webhook] rejected %s push: %v\n", source, err)
		http.Error(w, "signature verification failed", http.StatusUnauthorized)
		return
	}

	result, err := ParsePayload(jsonBody, source)
	if err != nil {
		fmt.Printf("[webhook] parse error: %v\n", err)
		http.Error(w, "parse error", http.StatusBadRequest)
		return
	}

	fmt.Printf("[webhook] received %s push to %s/%s\n", source, result.RepoURL, result.Branch)

	// Match against configured pipelines
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

// readRawAndJSON 一次性读完原始 body，并把内容归一化为 ParsePayload 所需的
// JSON：GitHub 可能以 application/x-www-form-urlencoded 投递（JSON 在 "payload"
// 字段），其余情况 raw == JSON。两个返回值都必要——签名校验必须作用于原始
// 字节（form 编码时签名覆盖 form 原文），解析则用归一化 JSON。
func readRawAndJSON(r *http.Request) (raw, jsonBody []byte, err error) {
	raw, err = io.ReadAll(r.Body)
	if err != nil {
		return nil, nil, err
	}
	if strings.Contains(r.Header.Get("Content-Type"), "application/x-www-form-urlencoded") {
		form, perr := url.ParseQuery(string(raw))
		if perr != nil {
			return raw, nil, perr
		}
		return raw, []byte(form.Get("payload")), nil
	}
	return raw, raw, nil
}

// verifySecret 校验 webhook 来源签名。secret 为空串 = 未配置校验（完全放行，
// 预留的逃生口，config.yaml.example 注明）；配置了 secret 时来源不明或签名
// 不符一律拒绝（fail-closed）。
func verifySecret(r *http.Request, rawBody []byte, source, secret string) error {
	if secret == "" {
		return nil
	}
	switch source {
	case "github":
		if verifyGitHubSig(r, rawBody, secret) {
			return nil
		}
	case "gitee":
		if verifyGiteeSig(r, secret) {
			return nil
		}
	}
	return fmt.Errorf("signature verification failed (source=%s)", source)
}

// verifyGitHubSig 校验 GitHub 签名：优先 X-Hub-Signature-256（HMAC-SHA256，
// 与 GitHub 当前投递一致）；该头存在但不匹配时**不回退** sha1——防止攻击者
// 把正确的 sha1 头配错误的 sha256 头混过校验。sha256 头缺失才接受旧版
// X-Hub-Signature（HMAC-SHA1）。两种算法的作用对象都是原始 body。
func verifyGitHubSig(r *http.Request, rawBody []byte, secret string) bool {
	if v := r.Header.Get(githubSig256Header); v != "" {
		return checkHMACSig(v, "sha256=", rawBody, secret, sha256.New)
	}
	if v := r.Header.Get(githubSigHeader); v != "" {
		return checkHMACSig(v, "sha1=", rawBody, secret, sha1.New)
	}
	return false
}

// verifyGiteeSig 校验 Gitee 签名：两方案任一命中即通过——X-Gitee-Token 等于
// 密钥（密码方案，恒时比较），或 X-Gitee-Timestamp 的 HMAC-SHA256 十六进制
// 与 X-Gitee-Signature 一致（加签方案，Gitee 对时间戳签名）。
func verifyGiteeSig(r *http.Request, secret string) bool {
	if token := r.Header.Get(giteeTokenHeader); token != "" {
		if subtle.ConstantTimeCompare([]byte(token), []byte(secret)) == 1 {
			return true
		}
	}
	ts := r.Header.Get(giteeTimestampHeader)
	sig := r.Header.Get(giteeSigHeader)
	if ts == "" || sig == "" {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(ts))
	expected := hex.EncodeToString(mac.Sum(nil))
	return subtle.ConstantTimeCompare([]byte(strings.ToLower(sig)), []byte(expected)) == 1
}

// checkHMACSig 校验 "algo=<hex>" 形式的签名头：对 rawBody 按给定哈希算法做
// HMAC，去掉头前缀后做十六进制解码并恒时比较。解码失败/格式不符一律视为不匹配。
func checkHMACSig(headerValue, prefix string, rawBody []byte, secret string, newHash func() hash.Hash) bool {
	v := strings.TrimPrefix(headerValue, prefix)
	if v == headerValue {
		return false
	}
	mac := hmac.New(newHash, []byte(secret))
	mac.Write(rawBody)
	want := mac.Sum(nil)
	got, err := hex.DecodeString(v)
	if err != nil {
		return false
	}
	return hmac.Equal(want, got)
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
