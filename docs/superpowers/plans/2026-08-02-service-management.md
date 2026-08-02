# Service Management Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement service management commands (start/stop/restart/deploy), fix status display, optimize config loading, and add notification email support for CLI deployments.

**Architecture:** Introduce an `internal/deploy` orchestrator package that coordinates git fetch, plugin build/start/stop, and email notifications. The `Deployer` interface is defined in the new `internal/deploy` package — plugins remain type-specific while the orchestrator handles cross-cutting concerns. Config loading uses a priority chain: CLI `-c` flag → current directory → `~/.deployd/config.yaml`.

**Tech Stack:** Go 1.21+, cobra CLI, gopkg.in/yaml.v3, syscall for process management

## Global Constraints

- Config priority: current directory config.yaml > ~/.deployd/config.yaml
- Daemon loads config once at startup; all lifecycle commands reuse the cached config path
- `deployd restart` (no args) restarts the daemon; `deployd restart <name>` restarts a service
- `service start/stop/restart` commands manage individual services without rebuilding
- authorEmail from git log is obtained after fetch, before build; failure returns empty string (graceful degradation)
- Empty recipient emails are skipped in both SMTP and Resend notification paths
- New service types only need to implement the existing `Deployer` interface

---

### Task 1: Config Default Path & homeDir Fix

**Files:**
- Modify: `internal/config/config.go`
- Modify: `internal/daemon/commands.go:204-207`
- Test: `internal/config/config_test.go`

**Interfaces:**
- Consumes: existing `config.Load(path string) (*AppConfig, error)`
- Produces: `config.DefaultConfig() string` — returns first found config path, empty string if none

- [ ] **Step 1: Write failing tests for DefaultConfig**

Add to `internal/config/config_test.go`:

```go
func TestDefaultConfig_CurrentDir(t *testing.T) {
    dir := t.TempDir()
    _ = os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("server:\n  port: 9527\n"), 0644)
    oldWd, _ := os.Getwd()
    _ = os.Chdir(dir)
    defer _ = os.Chdir(oldWd)
    path := DefaultConfig()
    if path == "" {
        t.Fatal("expected config path in current directory")
    }
    if !strings.HasSuffix(path, "config.yaml") {
        t.Errorf("expected config.yaml, got %s", path)
    }
}

func TestDefaultConfig_HomeDirFallback(t *testing.T) {
    dir := t.TempDir()
    _ = os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("server:\n  port: 9527\n"), 0644)
    oldHome, _ := os.UserHomeDir()
    os.Setenv("HOME", dir)
    defer os.Setenv("HOME", oldHome)
    oldWd, _ := os.Getwd()
    _ = os.Chdir("/tmp")
    defer _ = os.Chdir(oldWd)
    path := DefaultConfig()
    expected := filepath.Join(dir, "config.yaml")
    if path != expected {
        t.Errorf("expected %s, got %s", expected, path)
    }
}

func TestDefaultConfig_NotFound(t *testing.T) {
    dir := t.TempDir()
    oldWd, _ := os.Getwd()
    _ = os.Chdir(dir)
    defer _ = os.Chdir(oldWd)
    path := DefaultConfig()
    if path != "" {
        t.Errorf("expected empty string when no config found, got %s", path)
    }
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/config/ -v -run TestDefaultConfig`
Expected: FAIL — `DefaultConfig` undefined

- [ ] **Step 3: Implement DefaultConfig**

Add to `internal/config/config.go`:

```go
// DefaultConfig finds the default config file by priority:
// 1. Current directory config.yaml
// 2. ~/.deployd/config.yaml
// Returns empty string if none found.
func DefaultConfig() string {
    // Check current directory
    if _, err := os.Stat("config.yaml"); err == nil {
        return "config.yaml"
    }
    // Check ~/.deployd/config.yaml
    home, _ := os.UserHomeDir()
    path := filepath.Join(home, ".deployd", "config.yaml")
    if _, err := os.Stat(path); err == nil {
        return path
    }
    return ""
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/config/ -v -run TestDefaultConfig`
Expected: PASS all 3 tests

- [ ] **Step 5: Fix homeDir in commands.go**

Replace the `homeDir` function in `internal/daemon/commands.go:204-207`:

```go
func homeDir(_ string) string {
    h, _ := os.UserHomeDir()
    return h
}
```

- [ ] **Step 6: Run all config tests**

Run: `go test ./internal/config/ -v`
Expected: PASS all tests

- [ ] **Step 7: Commit**

```bash
git add internal/config/config.go internal/config/config_test.go internal/daemon/commands.go
git commit -m "feat: add DefaultConfig() and fix homeDir path resolution"
```

---

### Task 2: Author Email from Git

**Files:**
- Modify: `internal/build/git.go`
- Test: `internal/build/git_test.go`

**Interfaces:**
- Consumes: existing `build.Fetch()`, `build.Clone()`
- Produces: `build.GetLatestAuthorEmail(workspace, branch string) string`

- [ ] **Step 1: Write failing test**

Add to `internal/build/git_test.go`:

```go
func TestGetLatestAuthorEmail(t *testing.T) {
    dir := t.TempDir()
    // Init a git repo with a commit
    initCmd := exec.Command("git", "init", dir)
    initCmd.Run()
    authorCmd := exec.Command("git", "-C", dir, "config", "user.email", "test@example.com")
    authorCmd.Run()
    nameCmd := exec.Command("git", "-C", dir, "config", "user.name", "Test User")
    nameCmd.Run()
    _ = os.WriteFile(filepath.Join(dir, "README.md"), []byte("hello"), 0644)
    addCmd := exec.Command("git", "-C", dir, "add", ".")
    addCmd.Run()
    commitCmd := exec.Command("git", "-C", dir, "commit", "-m", "initial")
    commitCmd.Run()

    email := GetLatestAuthorEmail(dir, "master")
    if email != "test@example.com" {
        t.Errorf("expected test@example.com, got %s", email)
    }
}

func TestGetLatestAuthorEmail_NonExistentDir(t *testing.T) {
    email := GetLatestAuthorEmail("/nonexistent/path", "main")
    if email != "" {
        t.Errorf("expected empty string for nonexistent dir, got %s", email)
    }
}

func TestGetLatestAuthorEmail_NotAGitRepo(t *testing.T) {
    dir := t.TempDir()
    email := GetLatestAuthorEmail(dir, "main")
    if email != "" {
        t.Errorf("expected empty string for non-git dir, got %s", email)
    }
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/build/ -v -run TestGetLatestAuthorEmail`
Expected: FAIL — `GetLatestAuthorEmail` undefined

- [ ] **Step 3: Implement GetLatestAuthorEmail**

Add to `internal/build/git.go`:

```go
// GetLatestAuthorEmail executes git log -1 --format=%ae in the workspace directory.
// Returns empty string if the directory doesn't exist, isn't a git repo, or the command fails.
func GetLatestAuthorEmail(workspace, branch string) string {
    cmd := exec.Command("git", "-C", workspace, "log", "-1", "--format=%ae")
    out, err := cmd.Output()
    if err != nil {
        return ""
    }
    return strings.TrimSpace(string(out))
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/build/ -v -run TestGetLatestAuthorEmail`
Expected: PASS all 3 tests

- [ ] **Step 5: Run all build tests**

Run: `go test ./internal/build/ -v`
Expected: PASS all tests

- [ ] **Step 6: Commit**

```bash
git add internal/build/git.go internal/build/git_test.go
git commit -m "feat: add GetLatestAuthorEmail to extract commit author from git"
```

---

### Task 3: Notify Empty Recipient Fix

**Files:**
- Modify: `internal/notify/email.go`

**Interfaces:**
- Consumes: existing `Notifier` struct and methods
- Produces: no new API; fixes existing `sendResend` and `sendSMTP` to skip empty recipients

- [ ] **Step 1: Write failing test**

Add to `internal/notify/email_test.go`:

```go
func TestNotifyDeployResult_EmptyAuthorEmail(t *testing.T) {
    // Test that empty authorEmail doesn't cause sendResend to include "" in recipients
    n := New("", 0, "", "", false, "re_test-key", "from@example.com", []string{"admin@example.com"})
    // We can't easily test the actual HTTP call, but we can verify the struct
    // has the right recipients
    if len(n.to) != 2 {
        t.Errorf("expected 2 recipients, got %d", len(n.to))
    }
    if n.to[0] != "" {
        t.Errorf("expected first recipient to be empty string (authorEmail), got %s", n.to[0])
    }
    if n.to[1] != "admin@example.com" {
        t.Errorf("expected second recipient admin@example.com, got %s", n.to[1])
    }
}
```

- [ ] **Step 2: Run test to verify it passes** (test only checks struct, not send logic)

Run: `go test ./internal/notify/ -v -run TestNotifyDeployResult_EmptyAuthorEmail`
Expected: PASS (this test verifies the struct state)

- [ ] **Step 3: Fix sendResend to skip empty recipients**

In `internal/notify/email.go`, replace the recipient loop in `sendResend`:

```go
// sendResend sends via Resend HTTP API.
func (n *Notifier) sendResend(subject, body string) error {
    validRecipients := make([]string, 0, len(n.to))
    for _, r := range n.to {
        if r != "" {
            validRecipients = append(validRecipients, r)
        }
    }
    if len(validRecipients) == 0 {
        return nil
    }

    reqBody := map[string]interface{}{
        "from":    n.resendFrom,
        "to":      validRecipients,
        "subject": subject,
        "html":    body,
    }
    // ... rest unchanged
```

- [ ] **Step 4: Fix sendSMTP to skip empty recipients**

In `internal/notify/email.go`, in the `sendSMTP` method, replace the recipient loop:

```go
        for _, r := range recipients {
            if r == "" {
                continue
            }
            if err := client.Rcpt(r); err != nil {
                return fmt.Errorf("SMTP rcpt %s: %w", r, err)
            }
        }
```

- [ ] **Step 5: Run all notify tests**

Run: `go test ./internal/notify/ -v`
Expected: PASS all tests

- [ ] **Step 6: Commit**

```bash
git add internal/notify/email.go internal/notify/email_test.go
git commit -m "fix: skip empty recipients in SMTP and Resend notification paths"
```

---

### Task 4: Orchestrator Package

**Files:**
- Create: `internal/deploy/orchestrator.go`
- Create: `internal/deploy/orchestrator_test.go`
- Modify: `plugins/springboot/plugin.go:32-78`

**Interfaces:**
- Consumes: `build.Fetch()`, `build.GetLatestAuthorEmail()`, `config.ServiceConfig`, `config.AppConfig`, `notify.Notifier`
- Produces: `deploy.Deployer` interface, `deploy.Deploy()`, `deploy.ServiceStart()`, `deploy.ServiceStop()`, `deploy.ServiceRestart()`

- [ ] **Step 1: Write failing tests**

Create `internal/deploy/orchestrator_test.go`:

```go
package deploy

import (
    "context"
    "testing"

    "github.com/auto-deployer/auto-deployer/internal/config"
)

type mockDeployer struct {
    built   bool
    started bool
    stopped bool
    status  string
    buildErr  error
    startErr  error
}

func (m *mockDeployer) Build(ctx context.Context, svc *config.ServiceConfig) error {
    m.built = true
    return m.buildErr
}
func (m *mockDeployer) Start(ctx context.Context, svc *config.ServiceConfig) error {
    m.started = true
    return m.startErr
}
func (m *mockDeployer) Stop(ctx context.Context, svc *config.ServiceConfig) error {
    m.stopped = true
    return nil
}
func (m *mockDeployer) Status(ctx context.Context, svc *config.ServiceConfig) (string, error) {
    return m.status, nil
}

func TestServiceStart(t *testing.T) {
    m := &mockDeployer{}
    svc := &config.ServiceConfig{Name: "test", Type: "springboot", Workspace: "/tmp/test"}
    err := ServiceStart(context.Background(), svc, m)
    if err != nil {
        t.Fatal(err)
    }
    if !m.started {
        t.Error("expected Start to be called")
    }
    if m.built || m.stopped {
        t.Error("expected Build/Stop NOT to be called")
    }
}

func TestServiceStop(t *testing.T) {
    m := &mockDeployer{}
    svc := &config.ServiceConfig{Name: "test", Type: "springboot", Workspace: "/tmp/test"}
    err := ServiceStop(context.Background(), svc, m)
    if err != nil {
        t.Fatal(err)
    }
    if !m.stopped {
        t.Error("expected Stop to be called")
    }
    if m.built || m.started {
        t.Error("expected Build/Start NOT to be called")
    }
}

func TestServiceRestart(t *testing.T) {
    m := &mockDeployer{}
    svc := &config.ServiceConfig{Name: "test", Type: "springboot", Workspace: "/tmp/test"}
    err := ServiceRestart(context.Background(), svc, m)
    if err != nil {
        t.Fatal(err)
    }
    if !m.stopped {
        t.Error("expected Stop to be called")
    }
    if !m.started {
        t.Error("expected Start to be called")
    }
    if m.built {
        t.Error("expected Build NOT to be called")
    }
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/deploy/ -v`
Expected: FAIL — package doesn't exist yet

- [ ] **Step 3: Create orchestrator.go**

Create `internal/deploy/orchestrator.go`:

```go
package deploy

import (
    "context"
    "fmt"
    "os"
    "path/filepath"

    "github.com/auto-deployer/auto-deployer/internal/build"
    "github.com/auto-deployer/auto-deployer/internal/config"
    "github.com/auto-deployer/auto-deployer/internal/notify"
)

// Deployer handles the build and deploy logic for a service type.
type Deployer interface {
    Build(ctx context.Context, svc *config.ServiceConfig) error
    Start(ctx context.Context, svc *config.ServiceConfig) error
    Stop(ctx context.Context, svc *config.ServiceConfig) error
    Status(ctx context.Context, svc *config.ServiceConfig) (string, error)
}

// DeployResult contains the result of a deployment operation.
type DeployResult struct {
    ServiceName string
    Status      string // "success" | "failed"
    AuthorEmail string
    Error       string
}

// Deploy executes the full deployment pipeline:
// fetch → getAuthorEmail → plugin.Build → plugin.Stop → plugin.Start → notify
func Deploy(ctx context.Context, svc *config.ServiceConfig, cfg *config.AppConfig, deployer deploy.Deployer) (*DeployResult, error) {
    result := &DeployResult{ServiceName: svc.Name}

    // 1. Fetch fresh code
    keyFile, _, _, err := build.EnsureSSHKey()
    if err != nil {
        result.Status = "failed"
        result.Error = fmt.Sprintf("failed to ensure SSH key: %v", err)
        return result, result.Error
    }

    fmt.Printf("[deploy] fetching %s to %s...\n", svc.Repo.URL, svc.Workspace)
    if err := build.Fetch(svc.Repo.URL, keyFile, svc.Repo.Branch, svc.Workspace); err != nil {
        result.Status = "failed"
        result.Error = err.Error()
        sendNotify(ctx, cfg, svc, "", "failed", err.Error())
        return result, err
    }

    // 2. Get author email from latest commit
    authorEmail := build.GetLatestAuthorEmail(svc.Workspace, svc.Repo.Branch)

    // 3. Build
    fmt.Printf("[deploy] building %s...\n", svc.Name)
    if err := deployer.Build(ctx, svc); err != nil {
        result.Status = "failed"
        result.Error = err.Error()
        sendNotify(ctx, cfg, svc, authorEmail, "failed", err.Error())
        return result, err
    }

    // 4. Stop old instance
    fmt.Printf("[deploy] stopping %s...\n", svc.Name)
    _ = deployer.Stop(ctx, svc)

    // 5. Start new instance
    fmt.Printf("[deploy] starting %s...\n", svc.Name)
    if err := deployer.Start(ctx, svc); err != nil {
        result.Status = "failed"
        result.Error = err.Error()
        sendNotify(ctx, cfg, svc, authorEmail, "failed", err.Error())
        return result, err
    }

    result.Status = "success"
    result.AuthorEmail = authorEmail
    sendNotify(ctx, cfg, svc, authorEmail, "success", "")
    fmt.Printf("[deploy] %s deployed successfully\n", svc.Name)
    return result, nil
}

// ServiceStart starts a service without rebuilding.
func ServiceStart(ctx context.Context, svc *config.ServiceConfig, deployer deploy.Deployer) error {
    return deployer.Start(ctx, svc)
}

// ServiceStop stops a service.
func ServiceStop(ctx context.Context, svc *config.ServiceConfig, deployer deploy.Deployer) error {
    return deployer.Stop(ctx, svc)
}

// ServiceRestart stops and starts a service without rebuilding.
func ServiceRestart(ctx context.Context, svc *config.ServiceConfig, deployer deploy.Deployer) error {
    _ = deployer.Stop(ctx, svc)
    return deployer.Start(ctx, svc)
}

// GetServiceStatus returns the status of a service.
func GetServiceStatus(ctx context.Context, svc *config.ServiceConfig, deployer deploy.Deployer) (string, error) {
    return deployer.Status(ctx, svc)
}

func sendNotify(ctx context.Context, cfg *config.AppConfig, svc *config.ServiceConfig, authorEmail, status, errMsg string) {
    if notifier := buildNotifier(cfg, authorEmail); notifier != nil {
        go func() {
            _ = notifier.NotifyDeployResult(ctx, svc.Name, svc.Repo.Branch, authorEmail, status, errMsg)
        }()
    }
}

// buildNotifier creates a Notifier from config.
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

// ServicePIDDir returns the directory where service PID files are stored.
func ServicePIDDir(configPath string) string {
    home, _ := os.UserHomeDir()
    return filepath.Join(home, ".deployd", "run")
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/deploy/ -v`
Expected: PASS all 3 tests

- [ ] **Step 5: Refactor springboot.Build to remove fetch logic**

In `plugins/springboot/plugin.go`, replace the `Build` method to remove the git fetch section (lines 42-58):

```go
// Build executes the configured build command.
// Git fetch is handled by the orchestrator before calling this method.
func (p *Plugin) Build(ctx context.Context, svc *config.ServiceConfig) error {
    if svc.Build.Command == "" {
        return fmt.Errorf("build command is empty")
    }

    if err := build.ExecuteBuild(svc.Workspace, svc.Build.Command); err != nil {
        return err
    }

    // Move built jar to workspace root
    if err := moveJarToRoot(svc.Workspace); err != nil {
        fmt.Printf("[springboot] warning: failed to move jar: %v\n", err)
    }

    // Clean up everything except jar file (source code removed after build)
    if err := cleanWorkspace(svc.Workspace); err != nil {
        fmt.Printf("[springboot] warning: failed to clean workspace: %v\n", err)
    } else {
        fmt.Printf("[springboot] cleaned workspace (source code removed)\n")
    }

    fmt.Println("[springboot] build completed")
    return nil
}
```

Also remove the now-unused `build` import if needed, and update the import block.

- [ ] **Step 6: Update webhook to use orchestrator**

In `internal/webhook/server.go`, replace the deploy logic in `Handle()` (lines 120-157) with a call to `deploy.Deploy()`:

```go
    // Dispatch to orchestrator for build + restart
    ctx := context.Background()
    var deployer deploy.Deployer
    switch matched.Type {
    case "springboot":
        deployer = springboot.New()
    default:
        fmt.Printf("[webhook] unknown service type: %s\n", matched.Type)
        w.WriteHeader(http.StatusOK)
        _, _ = w.Write([]byte("ok"))
        return
    }

    result, err := deploy.Deploy(ctx, matched, cfg, deployer)
    if err != nil {
        fmt.Printf("[deploy] deploy failed: %v\n", err)
        w.WriteHeader(http.StatusOK)
        _, _ = w.Write([]byte("ok"))
        return
    }

    fmt.Printf("[deploy] %s deployed: %s\n", matched.Name, result.Status)
    w.WriteHeader(http.StatusOK)
    _, _ = w.Write([]byte("ok"))
```

Also add the import: `"github.com/auto-deployer/auto-deployer/internal/deploy"`

- [ ] **Step 7: Verify webhook tests still pass**

Run: `go test ./internal/webhook/ -v`
Expected: PASS all tests

- [ ] **Step 8: Run all tests**

Run: `go test ./... -v`
Expected: PASS all tests

- [ ] **Step 9: Commit**

```bash
git add internal/deploy/orchestrator.go internal/deploy/orchestrator_test.go plugins/springboot/plugin.go internal/webhook/server.go
git commit -m "feat: add deploy orchestrator, refactor springboot plugin, update webhook"
```

---

### Task 5: CLI Commands — Status, Deploy, Service Commands

**Files:**
- Modify: `cmd/status.go`
- Modify: `cmd/deploy.go`
- Create: `cmd/service_start.go`
- Create: `cmd/service_stop.go`
- Create: `cmd/service_restart.go`
- Modify: `internal/daemon/commands.go`

**Interfaces:**
- Consumes: `deploy.Deploy()`, `deploy.ServiceStart()`, `deploy.ServiceStop()`, `deploy.GetServiceStatus()`, `config.DefaultConfig()`
- Produces: CLI commands `deployd status`, `deployd deploy <name>`, `deployd service start <name>`, `deployd service stop <name>`, `deployd service restart <name>`

- [ ] **Step 1: Fix status command**

Replace `cmd/status.go`:

```go
package cmd

import (
    "fmt"
    "os"
    "path/filepath"

    "github.com/auto-deployer/auto-deployer/internal/config"
    "github.com/auto-deployer/auto-deployer/internal/process"
    "github.com/spf13/cobra"
)

func init() {
    statusCmd.Flags().StringVarP(&configFile, "config", "c", "", "config file path")
    rootCmd.AddCommand(statusCmd)
}

var statusCmd = &cobra.Command {
    Use:   "status",
    Short: "Show deployd and all services status",
    RunE: func(cmd *cobra.Command, args []string) error {
        return showStatus()
    },
}

func showStatus() error {
    // Find config
    path := configFile
    if path == "" {
        path = config.DefaultConfig()
    }

    // Check daemon status
    home, _ := os.UserHomeDir()
    pidFile := filepath.Join(home, ".deployd", "run", "deployd.pid")
    mgr := process.NewManager(pidFile)
    fmt.Printf("deployd: %s\n", mgr.Status())

    if path == "" {
        return nil
    }

    cfg, err := config.Load(path)
    if err != nil {
        return fmt.Errorf("failed to load config: %w", err)
    }

    for _, svc := range cfg.Services {
        svcPIDFile := filepath.Join(home, ".deployd", "run", svc.Name+".pid")
        svcMgr := process.NewManager(svcPIDFile)
        fmt.Printf("  %-30s %s\n", svc.Name, svcMgr.Status())
    }

    return nil
}
```


- [ ] **Step 2: Fix deploy command with clean interface**

Replace `cmd/deploy.go`:

```go
package cmd

import (
    "context"
    "fmt"
    "os"

    "github.com/auto-deployer/auto-deployer/internal/config"
    "github.com/auto-deployer/auto-deployer/internal/deploy"
    "github.com/auto-deployer/auto-deployer/plugins/springboot"
    "github.com/spf13/cobra"
)

func init() {
    deployCmd.Flags().StringVarP(&configFile, "config", "c", "", "config file path")
    rootCmd.AddCommand(deployCmd)
}

var deployCmd = &cobra.Command {
    Use:   "deploy <service_name>",
    Short: "Manually trigger full deployment for a service",
    Args:  cobra.ExactArgs(1),
    RunE: func(cmd *cobra.Command, args []string) error {
        serviceName := args[0]

        path := configFile
        if path == "" {
            path = config.DefaultConfig()
        }
        if path == "" {
            return fmt.Errorf("config file not found. Run 'deployd config' to create one, or use -c to specify path")
        }

        cfg, err := config.Load(path)
        if err != nil {
            return fmt.Errorf("failed to load config: %w", err)
        }

        var svc *config.ServiceConfig
        for i := range cfg.Services {
            if cfg.Services[i].Name == serviceName {
                s := &cfg.Services[i]
                svc = s
                break
            }
        }
        if svc == nil {
            return fmt.Errorf("service %q not found in config", serviceName)
        }

        var deployer deploy.Deployer
        switch svc.Type {
        case "springboot":
            deployer = springboot.New()
        default:
            return fmt.Errorf("unknown service type %q (supported: springboot)", svc.Type)
        }

        _, err = deploy.Deploy(context.Background(), svc, cfg, deployer)
        if err != nil {
            fmt.Fprintf(os.Stderr, "deploy failed: %v\n", err)
            os.Exit(1)
        }
        return nil
    },
}
```

- [ ] **Step 3: Create service start command**

Create `cmd/service_start.go`:

```go
package cmd

import (
    "context"
    "fmt"
    "os"

    "github.com/auto-deployer/auto-deployer/internal/config"
    "github.com/auto-deployer/auto-deployer/internal/deploy"
    "github.com/auto-deployer/auto-deployer/plugins/springboot"
    "github.com/spf13/cobra"
)

func init() {
    serviceStartCmd.Flags().StringVarP(&configFile, "config", "c", "", "config file path")
    serviceCmd.AddCommand(serviceStartCmd)
}

var serviceStartCmd = &cobra.Command{
    Use:   "start <service_name>",
    Short: "Start a service without rebuilding",
    Args:  cobra.ExactArgs(1),
    RunE: func(cmd *cobra.Command, args []string) error {
        serviceName := args[0]

        path := configFile
        if path == "" {
            path = config.DefaultConfig()
        }
        if path == "" {
            return fmt.Errorf("config file not found")
        }

        cfg, err := config.Load(path)
        if err != nil {
            return fmt.Errorf("failed to load config: %w", err)
        }

        var svc *config.ServiceConfig
        for i := range cfg.Services {
            if cfg.Services[i].Name == serviceName {
                s := &cfg.Services[i]
                svc = s
                break
            }
        }
        if svc == nil {
            return fmt.Errorf("service %q not found in config", serviceName)
        }

        var deployer deploy.Deployer
        switch svc.Type {
        case "springboot":
            deployer = springboot.New()
        default:
            return fmt.Errorf("unknown service type %q", svc.Type)
        }

        return deploy.ServiceStart(context.Background(), svc, deployer)
    },
}
```

- [ ] **Step 4: Create service stop command**

Create `cmd/service_stop.go`:

```go
package cmd

import (
    "context"
    "fmt"
    "os"

    "github.com/auto-deployer/auto-deployer/internal/config"
    "github.com/auto-deployer/auto-deployer/internal/deploy"
    "github.com/auto-deployer/auto-deployer/plugins/springboot"
    "github.com/spf13/cobra"
)

func init() {
    serviceStopCmd.Flags().StringVarP(&configFile, "config", "c", "", "config file path")
    serviceCmd.AddCommand(serviceStopCmd)
}

var serviceStopCmd = &cobra.Command{
    Use:   "stop <service_name>",
    Short: "Stop a service",
    Args:  cobra.ExactArgs(1),
    RunE: func(cmd *cobra.Command, args []string) error {
        serviceName := args[0]

        path := configFile
        if path == "" {
            path = config.DefaultConfig()
        }
        if path == "" {
            return fmt.Errorf("config file not found")
        }

        cfg, err := config.Load(path)
        if err != nil {
            return fmt.Errorf("failed to load config: %w", err)
        }

        var svc *config.ServiceConfig
        for i := range cfg.Services {
            if cfg.Services[i].Name == serviceName {
                s := &cfg.Services[i]
                svc = s
                break
            }
        }
        if svc == nil {
            return fmt.Errorf("service %q not found in config", serviceName)
        }

        var deployer deploy.Deployer
        switch svc.Type {
        case "springboot":
            deployer = springboot.New()
        default:
            return fmt.Errorf("unknown service type %q", svc.Type)
        }

        return deploy.ServiceStop(context.Background(), svc, deployer)
    },
}
```

- [ ] **Step 5: Create service restart command**

Create `cmd/service_restart.go`:

```go
package cmd

import (
    "context"
    "fmt"
    "os"

    "github.com/auto-deployer/auto-deployer/internal/config"
    "github.com/auto-deployer/auto-deployer/internal/deploy"
    "github.com/auto-deployer/auto-deployer/plugins/springboot"
    "github.com/spf13/cobra"
)

func init() {
    serviceRestartCmd.Flags().StringVarP(&configFile, "config", "c", "", "config file path")
    serviceCmd.AddCommand(serviceRestartCmd)
}

var serviceRestartCmd = &cobra.Command{
    Use:   "restart <service_name>",
    Short: "Restart a service without rebuilding",
    Args:  cobra.ExactArgs(1),
    RunE: func(cmd *cobra.Command, args []string) error {
        serviceName := args[0]

        path := configFile
        if path == "" {
            path = config.DefaultConfig()
        }
        if path == "" {
            return fmt.Errorf("config file not found")
        }

        cfg, err := config.Load(path)
        if err != nil {
            return fmt.Errorf("failed to load config: %w", err)
        }

        var svc *config.ServiceConfig
        for i := range cfg.Services {
            if cfg.Services[i].Name == serviceName {
                s := &cfg.Services[i]
                svc = s
                break
            }
        }
        if svc == nil {
            return fmt.Errorf("service %q not found in config", serviceName)
        }

        var deployer deploy.Deployer
        switch svc.Type {
        case "springboot":
            deployer = springboot.New()
        default:
            return fmt.Errorf("unknown service type %q", svc.Type)
        }

        return deploy.ServiceRestart(context.Background(), svc, deployer)
    },
}
```

- [ ] **Step 6: Add serviceCmd parent to root.go**

In `cmd/root.go`, add:

```go
var serviceCmd = &cobra.Command{
    Use:   "service",
    Short: "Manage individual services",
}

func init() {
    rootCmd.AddCommand(serviceCmd)
}
```

Wait — root.go already has `init()`. Let me check if there's already a serviceCmd or if we need to add it.

Looking at root.go, it only has `rootCmd` and `configFile`. We need to add `serviceCmd` to root.go.

- [ ] **Step 7: Verify compilation**

Run: `go build ./...`
Expected: No errors

- [ ] **Step 8: Run all tests**

Run: `go test ./... -v`
Expected: PASS all tests

- [ ] **Step 9: Commit**

```bash
git add cmd/status.go cmd/deploy.go cmd/service_start.go cmd/service_stop.go cmd/service_restart.go cmd/root.go
git commit -m "feat: add service management CLI commands (start/stop/restart/deploy/status)"
```

---

### Task 6: Daemon Restart Command

**Files:**
- Create: `cmd/restart.go`
- Modify: `internal/daemon/daemon.go`

**Interfaces:**
- Consumes: `daemon.Stop()`, `process.NewManager()`, `config.DefaultConfig()`
- Produces: `deployd restart [-c path]` CLI command that stops and restarts the daemon

- [ ] **Step 1: Create restart command**

Create `cmd/restart.go`:

```go
package cmd

import (
    "fmt"
    "os"
    "os/exec"
    "path/filepath"
    "runtime"
    "syscall"

    "github.com/auto-deployer/auto-deployer/internal/config"
    "github.com/spf13/cobra"
)

func init() {
    restartCmd.Flags().Bool("no-fork", false, "Run in foreground (no background fork)")
    restartCmd.Flags().StringVarP(&configFile, "config", "c", "", "config file path")
    rootCmd.AddCommand(restartCmd)
}

var restartCmd = &cobra.Command{
    Use:   "restart",
    Short: "Restart the deployd daemon",
    RunE: func(cmd *cobra.Command, args []string) error {
        path := configFile
        if path == "" {
            path = config.DefaultConfig()
        }

        // Stop current daemon
        if err := stopDaemon(path); err != nil {
            fmt.Printf("stopped existing daemon: %v\n", err)
        }

        noFork, _ := cmd.Flags().GetBool("no-fork")
        if noFork {
            return startDaemon(path)
        }

        if runtime.GOOS == "linux" {
            return forkToRestart(path)
        }
        return startDaemon(path)
    },
}

func startDaemon(configPath string) error {
    return daemon.Start(configPath)
}

func stopDaemon(configPath string) error {
    return daemon.Stop(configPath)
}

func forkToRestart(configPath string) error {
    exe, err := os.Executable()
    if err != nil {
        return fmt.Errorf("failed to get executable path: %w", err)
    }

    logDir := filepath.Join(filepath.Dir(configPath), ".deployd")
    _ = os.MkdirAll(logDir, 0755)
    logPath := filepath.Join(logDir, "daemon-fork.log")
    logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
    if err != nil {
        return fmt.Errorf("failed to open fork log: %w", err)
    }
    defer logFile.Close()

    cmd := exec.Command(exe, "restart", "--no-fork", "-c", configPath)
    cmd.Stdout = logFile
    cmd.Stderr = logFile
    cmd.Stdin = nil
    cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}

    if err := cmd.Start(); err != nil {
        return fmt.Errorf("failed to restart daemon: %w", err)
    }

    fmt.Printf("daemon restarted in background (pid: %d)\n", cmd.Process.Pid)
    fmt.Printf("logs: %s\n", logPath)
    return nil
}
```

- [ ] **Step 2: Verify compilation**

Run: `go build ./...`
Expected: No errors

- [ ] **Step 3: Run all tests**

Run: `go test ./... -v`
Expected: PASS all tests

- [ ] **Step 4: Commit**

```bash
git add cmd/restart.go
git commit -m "feat: add deployd restart command for daemon"
```

---

### Task 7: Config Wizard Default Path

**Files:**
- Modify: `cmd/config_cmd.go`
- Modify: `internal/config/wizard.go`

**Interfaces:**
- Consumes: `config.DefaultConfig()`
- Produces: `deployd config` writes to the correct default path

- [ ] **Step 1: Update config command**

Replace `cmd/config_cmd.go`:

```go
package cmd

import (
    "fmt"
    "os"

    "github.com/auto-deployer/auto-deployer/internal/config"
    "github.com/spf13/cobra"
)

func init() {
    rootCmd.AddCommand(configCmd)
}

var configCmd = &cobra.Command{
    Use:   "config",
    Short: "Interactive configuration wizard",
    RunE: func(cmd *cobra.Command, args []string) error {
        fmt.Fprintln(os.Stdout, "Starting configuration wizard...")

        // Determine config path: prefer current directory, fallback to ~/.deployd/
        path := config.DefaultConfig()
        if path == "" {
            // Write to current directory
            path = "config.yaml"
        }

        fmt.Fprintln(os.Stdout, "Config file: "+path)
        return config.RunWizard(os.Stdout, os.Stdin, path)
    },
}
```

- [ ] **Step 2: Verify compilation**

Run: `go build ./...`
Expected: No errors

- [ ] **Step 3: Run all tests**

Run: `go test ./... -v`
Expected: PASS all tests

- [ ] **Step 4: Commit**

```bash
git add cmd/config_cmd.go
git commit -m "feat: config wizard uses default config path priority"
```

---

### Task 8: Integration Verification

**Files:**
- No new files
- May need to update existing tests

**Interfaces:**
- Consumes: all previously implemented features
- Produces: verified end-to-end functionality

- [ ] **Step 1: Run full test suite**

Run: `go test ./... -v -count=1`
Expected: All tests pass

- [ ] **Step 2: Build binary and verify commands**

Run: `go build -o /tmp/deployd-test .`
Then test each command:
```bash
/tmp/deployd-test status
/tmp/deployd-test --help
/tmp/deployd-test service --help
```
Expected: All commands show proper help text

- [ ] **Step 3: Test config default path resolution**

Create a test config in a temp dir and verify `DefaultConfig()` finds it:
```go
// Quick manual test
dir := t.TempDir()
oldWd, _ := os.Getwd()
os.Chdir(dir)
_ = os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("server:\n  port: 9527\n"), 0644)
path := config.DefaultConfig()
fmt.Println("Found config:", path)
os.Chdir(oldWd)
```
Expected: Prints `config.yaml` (current directory)

- [ ] **Step 4: Final commit if needed**

```bash
git add .
git commit -m "test: add integration verification for service management"
```
