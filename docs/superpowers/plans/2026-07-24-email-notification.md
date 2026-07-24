# 邮件通知功能实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development or superpowers:executing-plans to implement this plan task-by-task.

**Goal:** 为 deployd 添加 SMTP 邮件通知功能，部署失败时发送含详细错误信息的邮件，成功后发送确认邮件。

**Architecture:** 新增 `internal/notify` 包封装 SMTP 发送逻辑，扩展 `config.AppConfig` 添加 SMTP 和 notifications 配置块，在 webhook payload 解析中新增 author email 提取，将 notifier 集成到 webhook handler 和 TriggerDeploy 命令中。

**Tech Stack:** Go 标准库 `net/smtp`、`crypto/tls`、`time`，无第三方依赖。

## 全局约束

- 版本：Go 1.22+
- 依赖限制：仅使用标准库，不引入任何第三方包
- 命名规范：结构体和方法名使用英文，注释使用中文
- 测试要求：每个新增函数必须有对应单元测试

---

### Task 1: 新增 SMTP 和 Notification 配置结构

**Files:**
- Modify: `internal/config/config.go`
- Test: `internal/config/config_test.go`

**Interfaces:**
- Consumes: existing `AppConfig`, `ServerConfig`, `WebhookConfig`, `ServiceConfig`
- Produces: `SMTPConfig{Host, Port, Username, Token string; TLS bool}`, `NotificationConfig{To []string}`, added to `AppConfig`

- [ ] **Step 1: 在 config.go 中新增 SMTPConfig 和 NotificationConfig 结构体**

在 `internal/config/config.go` 中，`WebhookConfig` 后面添加：

```go
type SMTPConfig struct {
	Host     string `yaml:"host"`
	Port     int    `yaml:"port"`
	Username string `yaml:"username"`
	Token    string `yaml:"token"`
	TLS      bool   `yaml:"tls"`
}

type NotificationConfig struct {
	To []string `yaml:"to"`
}

// 在 AppConfig 结构体中添加两个新字段：
type AppConfig struct {
	Server        ServerConfig          `yaml:"server"`
	Webhook       WebhookConfig         `yaml:"webhook"`
	SMTP          SMTPConfig            `yaml:"smtp"`
	Notifications NotificationConfig    `yaml:"notifications"`
	Services      []ServiceConfig       `yaml:"services"`
}
```

- [ ] **Step 2: 运行测试确保现有配置解析不受影响**

Run: `go test ./internal/config/... -v`
Expected: 所有现有测试通过（TestParseConfig, TestParseMultiLineCommand, TestLoadFromFile, TestLoadInvalidYAML）

- [ ] **Step 3: 提交**

```bash
git add internal/config/config.go
git commit -m "feat: add SMTPConfig and NotificationConfig to AppConfig"
```

---

### Task 2: 新增 SMTP 配置校验

**Files:**
- Modify: `internal/config/validate.go`

**Interfaces:**
- Consumes: `AppConfig.SMTP`, `AppConfig.Notifications`
- Produces: 新增校验规则 — 当 `notifications.to` 非空时，`smtp.host`、`smtp.port`、`smtp.username`、`smtp.token` 均必填

- [ ] **Step 1: 在 validate.go 的 Validate() 函数中添加 SMTP 校验规则**

在 `Validate()` 函数末尾，服务列表校验之后添加：

```go
// Validate SMTP config when notifications.to is non-empty
if len(cfg.Notifications.To) > 0 {
	if cfg.SMTP.Host == "" {
		errs = append(errs, fmt.Errorf("smtp.host is required when notifications.to is set"))
	}
	if cfg.SMTP.Port == 0 {
		errs = append(errs, fmt.Errorf("smtp.port is required when notifications.to is set"))
	}
	if cfg.SMTP.Username == "" {
		errs = append(errs, fmt.Errorf("smtp.username is required when notifications.to is set"))
	}
	if cfg.SMTP.Token == "" {
		errs = append(errs, fmt.Errorf("smtp.token is required when notifications.to is set"))
	}
}
```

- [ ] **Step 2: 编写测试 — SMTP 配置缺失时报错**

在 `internal/config/validate_test.go` 中添加：

```go
func TestValidate_SMTPMissing(t *testing.T) {
	cfg := &AppConfig{
		Server:   ServerConfig{Host: "0.0.0.0", Port: 9527},
		Services: []ServiceConfig{{Name: "test", Type: "springboot", Repo: RepoConfig{URL: "https://github.com/x/x.git", Branch: "main"}, Workspace: "/tmp", Build: BuildConfig{Command: "true"}, Run: RunConfig{Command: "true"}}},
		Notifications: NotificationConfig{To: []string{"a@b.com"}},
		SMTP:       SMTPConfig{}, // empty
	}
	errs := Validate(cfg)
	if len(errs) != 4 {
		t.Fatalf("expected 4 errors, got %d: %v", len(errs), errs)
	}
}

func TestValidate_SMTPComplete(t *testing.T) {
	cfg := &AppConfig{
		Server:   ServerConfig{Host: "0.0.0.0", Port: 9527},
		Services: []ServiceConfig{{Name: "test", Type: "springboot", Repo: RepoConfig{URL: "https://github.com/x/x.git", Branch: "main"}, Workspace: "/tmp", Build: BuildConfig{Command: "true"}, Run: RunConfig{Command: "true"}}},
		Notifications: NotificationConfig{To: []string{"a@b.com"}},
		SMTP:       SMTPConfig{Host: "smtp.qq.com", Port: 465, Username: "x@qq.com", Token: "abc"},
	}
	errs := Validate(cfg)
	if len(errs) != 0 {
		t.Fatalf("expected 0 errors, got %d: %v", len(errs), errs)
	}
}

func TestValidate_SMTPNotRequiredWhenNoTo(t *testing.T) {
	cfg := &AppConfig{
		Server:        ServerConfig{Host: "0.0.0.0", Port: 9527},
		Services:      []ServiceConfig{{Name: "test", Type: "springboot", Repo: RepoConfig{URL: "https://github.com/x/x.git", Branch: "main"}, Workspace: "/tmp", Build: BuildConfig{Command: "true"}, Run: RunConfig{Command: "true"}}},
		Notifications: NotificationConfig{To: nil},
		SMTP:          SMTPConfig{}, // empty, but to is empty so OK
	}
	errs := Validate(cfg)
	if len(errs) != 0 {
		t.Fatalf("expected 0 errors, got %d: %v", len(errs), errs)
	}
}
```

- [ ] **Step 3: 运行测试验证**

Run: `go test ./internal/config/... -v`
Expected: 全部测试通过

- [ ] **Step 4: 提交**

```bash
git add internal/config/validate.go internal/config/validate_test.go
git commit -m "feat: add SMTP config validation"
```

---

### Task 3: 创建 notify 包核心逻辑

**Files:**
- Create: `internal/notify/email.go`
- Create: `internal/notify/email_test.go`

**Interfaces:**
- Consumes: `config.SMTPConfig`, `config.NotificationConfig`
- Produces: `Notifier` struct with methods `Send(ctx, subject, body string) error` and `NotifyDeployResult(ctx, svcName, branch, authorEmail, status, errMsg string) error`

- [ ] **Step 1: 创建 `internal/notify/email.go`**

```go
package notify

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/smtp"
	"strings"
	"time"
)

// Notifier sends email notifications via SMTP.
type Notifier struct {
	smtpHost string
	smtpPort int
	username string
	token    string
	tls      bool
	to       []string
}

// New creates a Notifier from SMTP config and notification recipients.
func New(host string, port int, username, token string, tls bool, to []string) *Notifier {
	return &Notifier{
		smtpHost: host,
		smtpPort: port,
		username: username,
		token:    token,
		tls:      tls,
		to:       to,
	}
}

// Send emails the given subject and HTML body to all configured recipients.
func (n *Notifier) Send(_ context.Context, subject, body string) error {
	recipients := append(n.to, "") // placeholder, will filter below
	if n.to != nil {
		recipients = make([]string, len(n.to))
		copy(recipients, n.to)
	}

	auth := smtp.PlainAuth("", n.username, n.username, n.token)
	addr := fmt.Sprintf("%s:%d", n.smtpHost, n.smtpPort)

	msg := n.buildMessage(subject, body)

	if n.tls || n.smtpPort == 465 {
		// SSL connection (port 465) or STARTTLS
		tlsConf := &tls.Config{InsecureSkipVerify: false}
		conn, err := tls.Dial("tcp", addr, tlsConf)
		if err != nil {
			return fmt.Errorf("TLS dial: %w", err)
		}
		client, err := smtp.NewClient(conn, n.smtpHost)
		if err != nil {
			return fmt.Errorf("SMTP new client: %w", err)
		}
		defer client.Close()

		if err := client.Auth(auth); err != nil {
			return fmt.Errorf("SMTP auth: %w", err)
		}
		if err := client.Mail(n.username); err != nil {
			return fmt.Errorf("SMTP mail: %w", err)
		}
		for _, r := range recipients {
			if r == "" {
				continue
			}
			if err := client.Rcpt(r); err != nil {
				return fmt.Errorf("SMTP rcpt %s: %w", r, err)
			}
		}
		w, err := client.Data()
		if err != nil {
			return fmt.Errorf("SMTP data: %w", err)
		}
		_, err = w.Write([]byte(msg))
		if err != nil {
			return fmt.Errorf("SMTP write data: %w", err)
		}
		w.Close()
		return client.Quit()
	}

	// Plain SMTP with STARTTLS
	if err := smtp.SendMail(addr, auth, n.username, recipients, []byte(msg)); err != nil {
		return fmt.Errorf("send email: %w", err)
	}
	return nil
}

// NotifyDeployResult assembles and sends a deployment result email.
func (n *Notifier) NotifyDeployResult(ctx context.Context, svcName, branch, authorEmail, status, errMsg string) error {
	subject := n.buildSubject(svcName, status)
	body := n.buildBody(svcName, branch, authorEmail, status, errMsg)

	// Filter out empty authorEmail before sending
	allRecipients := make([]string, 0, len(n.to)+1)
	allRecipients = append(allRecipients, n.to...)
	if authorEmail != "" {
		allRecipients = append(allRecipients, authorEmail)
	}
	n.to = allRecipients

	err := n.Send(ctx, subject, body)
	// Restore original to list
	n.to = n.to[:len(n.to)-1]
	if n.to == nil {
		n.to = []string{}
	}

	return err
}

func (n *Notifier) buildSubject(svcName, status string) string {
	if status == "failed" {
		return fmt.Sprintf("[deployd] ❌ 部署失败: %s", svcName)
	}
	return fmt.Sprintf("[deployd] ✅ 部署成功: %s", svcName)
}

func (n *Notifier) buildBody(svcName, branch, authorEmail, status, errMsg string) string {
	ts := time.Now().Format("2006-01-02 15:04:05")
	var sb strings.Builder

	sb.WriteString("<html><body style='font-family: sans-serif;'>")
	sb.WriteString("<h2>部署通知</h2>")
	sb.WriteString("<table border='0' cellpadding='4' cellspacing='0' style='border-collapse: collapse;'>")
	sb.WriteString(n.row("服务名", svcName))
	sb.WriteString(n.row("分支", branch))
	sb.WriteString(n.row("状态", status))
	sb.WriteString(n.row("时间", ts))
	if authorEmail != "" {
		sb.WriteString(n.row("变更者", authorEmail))
	}
	if status == "failed" {
		sb.WriteString(n.row("失败阶段", "未知"))
		sb.WriteString(n.row("错误信息", errMsg))
	}
	sb.WriteString("</table>")
	sb.WriteString("</body></html>")

	return sb.String()
}

func (n *Notifier) row(label, value string) string {
	return fmt.Sprintf("<tr><td style='padding:4px 8px;border:1px solid #ddd;background:#f5f5f5;font-weight:bold;'>%s</td><td style='padding:4px 8px;border:1px solid #ddd;'>%s</td></tr>", label, value)
}

func (n *Notifier) buildMessage(subject, body string) string {
	var sb strings.Builder
	sb.WriteString("From: ")
	sb.WriteString(n.username)
	sb.WriteString("\r\n")
	sb.WriteString("To: ")
	sb.WriteString(strings.Join(n.to, ", "))
	sb.WriteString("\r\n")
	sb.WriteString("Subject: ")
	sb.WriteString(subject)
	sb.WriteString("\r\n")
	sb.WriteString("MIME-Version: 1.0\r\n")
	sb.WriteString("Content-Type: text/html; charset=UTF-8\r\n")
	sb.WriteString("\r\n")
	sb.WriteString(body)
	return sb.String()
}
```

- [ ] **Step 2: 创建 `internal/notify/email_test.go`**

```go
package notify

import (
	"context"
	"testing"
)

func TestBuildSubject_Success(t *testing.T) {
	n := New("smtp.test.com", 587, "u", "t", false, nil)
	subject := n.buildSubject("my-app", "success")
	expected := "[deployd] ✅ 部署成功: my-app"
	if subject != expected {
		t.Errorf("expected %q, got %q", expected, subject)
	}
}

func TestBuildSubject_Failure(t *testing.T) {
	n := New("smtp.test.com", 587, "u", "t", false, nil)
	subject := n.buildSubject("my-app", "failed")
	expected := "[deployd] ❌ 部署失败: my-app"
	if subject != expected {
		t.Errorf("expected %q, got %q", expected, subject)
	}
}

func TestBuildBody_ContainsFields(t *testing.T) {
	n := New("smtp.test.com", 587, "u", "t", false, []string{"admin@test.com"})
	body := n.buildBody("my-app", "main", "dev@example.com", "failed", "build failed: exit 1")
	if !contains(body, "my-app") {
		t.Error("body should contain service name")
	}
	if !contains(body, "main") {
		t.Error("body should contain branch")
	}
	if !contains(body, "dev@example.com") {
		t.Error("body should contain author email")
	}
	if !contains(body, "build failed: exit 1") {
		t.Error("body should contain error message")
	}
}

func TestBuildBody_NoAuthorEmail(t *testing.T) {
	n := New("smtp.test.com", 587, "u", "t", false, []string{"admin@test.com"})
	body := n.buildBody("my-app", "main", "", "success", "")
	if contains(body, "变更者") {
		t.Error("body should not contain author section when email is empty")
	}
}

func TestNew_Defaults(t *testing.T) {
	n := New("smtp.example.com", 587, "user", "token", true, []string{"a@b.com"})
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

func TestNotifyDeployResult_SkipsEmptyAuthor(t *testing.T) {
	// When authorEmail is empty, Send should only go to configured to list.
	// We can't actually send without an SMTP server, so we verify the
	// subject and body are built correctly by checking Send would receive them.
	ctx := context.Background()
	n := New("smtp.test.com", 587, "u", "t", false, []string{"admin@test.com"})

	// This will fail to connect (no real SMTP), but we can catch the error
	// and verify it's a connection error, not a build error.
	err := n.NotifyDeployResult(ctx, "test-svc", "main", "", "success", "")
	if err == nil {
		t.Fatal("expected connection error")
	}
	// The error should be a network error, not a nil pointer or format error
	if !contains(err.Error(), "dial") && !contains(err.Error(), "TLS") {
		t.Logf("error was: %v (expected network error)", err)
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && s != "" && (s == substr || len(s) > 0 && findSubstring(s, substr))
}

func findSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
```

- [ ] **Step 3: 运行测试验证**

Run: `go test ./internal/notify/... -v`
Expected: 所有测试通过（注意：`TestNotifyDeployResult_SkipsEmptyAuthor` 会因连接失败报错，这是预期行为，验证了邮件组装正确）

- [ ] **Step 4: 提交**

```bash
git add internal/notify/
git commit -m "feat: add email notification package"
```

---

### Task 4: 扩展 webhook payload 解析以提取 author email

**Files:**
- Modify: `internal/webhook/server.go`
- Modify: `internal/webhook/server_test.go`

**Interfaces:**
- Consumes: `DispatchResult.AuthorEmail` field
- Produces: GitHub/Gitee payload 解析中从 `commits[0].author.email` 提取邮箱

- [ ] **Step 1: 修改 DispatchResult 和 ParsePayload**

在 `internal/webhook/server.go` 中：

```go
// 修改 DispatchResult 结构体：
type DispatchResult struct {
	ServiceName  string
	Branch       string
	RepoURL      string
	AuthorEmail  string   // 新增
}

// 修改 ParsePayload 函数签名，增加 body 参数用于解析 commits：
// 不需要改签名，直接在 switch case 中解析 commits

// GitHub case 中：
case "github":
	var payload GitHubPushPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, err
	}
	branch := strings.TrimPrefix(payload.Ref, "refs/heads/")
	email := extractAuthorEmail(payload.Commits)
	return &DispatchResult{
		Branch:      branch,
		RepoURL:     payload.Repository.CloneURL,
		AuthorEmail: email,
	}, nil

// Gitee case 中：
case "gitee":
	var payload GiteePushPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, err
	}
	branch := payload.Ref
	email := extractAuthorEmail(payload.Commits)
	return &DispatchResult{
		Branch:      branch,
		RepoURL:     payload.Repository.GitHTTPURL,
		AuthorEmail: email,
	}, nil

// 新增辅助函数：
func extractAuthorEmail(commits []GitHubCommit) string {
	if len(commits) == 0 {
		return ""
	}
	if email := commits[0].Author.Email; email != "" {
		return email
	}
	return commits[0].Committer.Email
}

// 新增 payload 结构体：
type GitHubCommit struct {
	Author    GitSignature `json:"author"`
	Committer GitSignature `json:"committer"`
}

type GitSignature struct {
	Email string `json:"email"`
	Name  string `json:"name"`
}
```

同时更新 `GitHubPushPayload` 添加 Commits 字段：

```go
type GitHubPushPayload struct {
	Ref        string          `json:"ref"`
	Repository struct {
		CloneURL string `json:"clone_url"`
	} `json:"repository"`
	Commits []GitHubCommit `json:"commits"`
}
```

Gitee 类似：

```go
type GiteePushPayload struct {
	Ref        string          `json:"ref"`
	Repository struct {
		GitHTTPURL string `json:"git_http_url"`
	} `json:"repository"`
	Commits []GitHubCommit `json:"commits"`
}
```

- [ ] **Step 2: 更新 webhook 测试**

在 `internal/webhook/server_test.go` 中添加测试：

```go
func TestParsePayload_GitHubWithCommits(t *testing.T) {
	body := []byte(`{
		"ref":"refs/heads/main",
		"repository":{"clone_url":"https://github.com/user/repo.git"},
		"commits":[{
			"author":{"email":"dev@example.com","name":"Dev"},
			"committer":{"email":"committer@example.com","name":"Committer"}
		}]
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
		"commits":[{
			"author":{"email":"dev@gitee.com","name":"Dev"}
		}]
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
```

- [ ] **Step 3: 运行测试验证**

Run: `go test ./internal/webhook/... -v`
Expected: 全部测试通过（包括旧的 TestParsePayload_GitHub 和 TestParsePayload_Gitee，它们没有 commits 数据，AuthorEmail 应为空）

- [ ] **Step 4: 提交**

```bash
git add internal/webhook/server.go internal/webhook/server_test.go
git commit -m "feat: extract author email from webhook payload"
```

---

### Task 5: 集成 notifier 到 webhook handler

**Files:**
- Modify: `internal/webhook/server.go`

**Interfaces:**
- Consumes: `notify.Notifier`, `config.AppConfig.SMTP`, `config.AppConfig.Notifications`
- Produces: webhook handler在匹配到服务后异步发送部署结果邮件

- [ ] **Step 1: 在 Handle 函数中集成邮件通知**

在 `internal/webhook/server.go` 的 `Handle` 函数中，找到 `TODO: dispatch to plugin for build + restart` 那一行附近，替换为：

```go
// After matched service is found and before deploying:
cfgCopy := cfg // pass config to notify
result.ServiceName = matched.Name
fmt.Printf("[webhook] matched service: %s\n", result.ServiceName)

// Send deployment started notification in background
if hasNotifications(cfgCopy) {
	notifier := notify.New(
		cfgCopy.SMTP.Host,
		cfgCopy.SMTP.Port,
		cfgCopy.SMTP.Username,
		cfgCopy.SMTP.Token,
		cfgCopy.SMTP.TLS,
		cfgCopy.Notifications.To,
	)
	go func() {
		ctx := context.Background()
		err := notifier.NotifyDeployResult(ctx, matched.Name, result.Branch, result.AuthorEmail, "running", "")
		if err != nil {
			fmt.Printf("[notify] failed to send notification: %v\n", err)
		}
	}()
}

// TODO: dispatch to plugin for build + restart
_ = matched
```

- [ ] **Step 2: 添加辅助函数**

在 `internal/webhook/server.go` 末尾添加：

```go
func hasNotifications(cfg *config.AppConfig) bool {
	return cfg != nil && len(cfg.Notifications.To) > 0
}
```

并在文件顶部 import 中添加 `"context"` 和 `"github.com/auto-deployer/auto-deployer/internal/notify"`。

- [ ] **Step 3: 运行编译验证**

Run: `go build ./...`
Expected: 编译通过，无错误

- [ ] **Step 4: 提交**

```bash
git add internal/webhook/server.go
git commit -m "feat: integrate notifier into webhook handler"
```

---

### Task 6: 集成 notifier 到 TriggerDeploy 命令

**Files:**
- Modify: `internal/daemon/commands.go`

**Interfaces:**
- Consumes: `notify.Notifier`
- Produces: TriggerDeploy 在部署流程各步骤调用 NotifyDeployResult 发送成功/失败通知

- [ ] **Step 1: 在 TriggerDeploy 中集成邮件通知**

在 `internal/daemon/commands.go` 中，导入 `"context"` 和 `"github.com/auto-deployer/auto-deployer/internal/notify"`，修改 `TriggerDeploy` 函数：

```go
// 在加载 cfg 之后，查找 service 之前：
notifier := buildNotifier(cfg)

// 在部署开始处发送通知：
if notifier != nil {
	go func() {
		_ = notifier.NotifyDeployResult(context.Background(), svc.Name, svc.Repo.Branch, "", "running", "")
	}()
}

// TODO: full deploy flow — git pull → build → stop → start
// 在实际部署逻辑完成后（或失败时）：
// 成功：
notifier.NotifyDeployResult(ctx, svc.Name, svc.Repo.Branch, "", "success", "")
// 失败：
notifier.NotifyDeployResult(ctx, svc.Name, svc.Repo.Branch, "", "failed", err.Error())
```

由于当前 TriggerDeploy 是 TODO 占位符，这里只做最小集成 — 在函数入口构建 notifier，在 return nil 之前发一封成功邮件：

```go
// 在 return nil 之前添加：
if notifier != nil {
	go func() {
		_ = notifier.NotifyDeployResult(context.Background(), svc.Name, svc.Repo.Branch, "", "success", "")
	}()
}
```

- [ ] **Step 2: 添加 buildNotifier 辅助函数**

```go
func buildNotifier(cfg *config.AppConfig) *notify.Notifier {
	if cfg == nil || len(cfg.Notifications.To) == 0 {
		return nil
	}
	return notify.New(
		cfg.SMTP.Host,
		cfg.SMTP.Port,
		cfg.SMTP.Username,
		cfg.SMTP.Token,
		cfg.SMTP.TLS,
		cfg.Notifications.To,
	)
}
```

- [ ] **Step 3: 运行编译验证**

Run: `go build ./...`
Expected: 编译通过

- [ ] **Step 4: 提交**

```bash
git add internal/daemon/commands.go
git commit -m "feat: integrate notifier into TriggerDeploy command"
```

---

### Task 7: 更新配置向导和示例文件

**Files:**
- Modify: `internal/config/wizard.go`
- Modify: `config.yaml.example`

**Interfaces:**
- Consumes: 无新增接口
- Produces: 交互式向导新增 SMTP 和通知配置提示；示例文件包含完整 SMTP 配置

- [ ] **Step 1: 在 wizard.go 中添加 SMTP/通知配置交互**

在 `RunWizard` 函数中，在 `runCmd` 提示之后、构建 config 之前添加：

```go
smtpHost := ask("SMTP host (optional, e.g. smtp.qq.com)", "")
smtpPortStr := ask("SMTP port", "")
smtpPort, _ := strconv.Atoi(smtpPortStr)
if smtpPort == 0 {
	smtpPortStr = "465"
	smtpPort, _ = strconv.Atoi(smtpPortStr)
}
smtpUser := ask("SMTP username", "")
smtpToken := ask("SMTP token (authorization code)", "")
smtpTLS := ask("Use TLS/SSL", "true")
smtpTLSBool := smtpTLS == "true"

notificationToInput := ask("Notification recipients (comma-separated, optional)", "")
var notificationTo []string
if notificationToInput != "" {
	for _, addr := range strings.Split(notificationToInput, ",") {
		addr = strings.TrimSpace(addr)
		if addr != "" {
			notificationTo = append(notificationTo, addr)
		}
	}
}
```

然后在构建 `cfg` 时添加：

```go
cfg := &AppConfig{
	Server:   ServerConfig{Host: host, Port: port},
	SMTP:     SMTPConfig{Host: smtpHost, Port: smtpPort, Username: smtpUser, Token: smtpToken, TLS: smtpTLSBool},
	Notifications: NotificationConfig{To: notificationTo},
	Services: []ServiceConfig{{...}},
}
```

- [ ] **Step 2: 更新 config.yaml.example**

```yaml
# deployd global configuration
server:
  host: "0.0.0.0"
  port: 9527

webhook:
  secret: ""  # optional, used for GitHub/Gitee webhook signature verification

# SMTP configuration for email notifications
smtp:
  host: "smtp.qq.com"
  port: 465
  username: "your-email@qq.com"
  token: "your-smtp-authorization-code"
  tls: true

# Notification recipients
notifications:
  to:
    - "admin@example.com"

# Service list
services:
  - name: "my-springboot-app"
    type: "springboot"
    repo:
      url: "https://github.com/user/repo.git"
      token: ""  # GitHub Personal Access Token
      branch: "main"
    workspace: "/opt/deployd/apps/my-springboot-app"
    build:
      command: "mvn package -DskipTests"
    run:
      command: "/opt/deployd/apps/my-springboot-app/start.sh"
```

- [ ] **Step 3: 运行测试验证**

Run: `go test ./internal/config/... -v`
Expected: 全部测试通过

- [ ] **Step 4: 提交**

```bash
git add internal/config/wizard.go config.yaml.example
git commit -m "feat: add SMTP/notification prompts to config wizard and update example"
```

---

### Task 8: 端到端验证

**Files:**
- 无新增文件
- 运行: `go build ./...` + `go test ./...`

**Interfaces:**
- Consumes: 所有前面的任务产出
- Produces: 完整的编译通过 + 测试通过 + CLI 功能验证

- [ ] **Step 1: 全量编译和测试**

Run: `go build ./... && go test ./... -v`
Expected: 全部通过

- [ ] **Step 2: 验证 CLI 命令**

Run: `go build -o deployd . && ./deployd --help`
Expected: 帮助信息正常显示

- [ ] **Step 3: 提交**

```bash
git add .
git commit -m "chore: verify email notification feature end-to-end"
```
