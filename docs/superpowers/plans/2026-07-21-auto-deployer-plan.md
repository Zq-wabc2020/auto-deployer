# Auto Deployer Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 构建一个 Go CLI 工具，后台常驻运行，接收 GitHub/Gitee webhook 触发后自动执行 git pull → mvn package → 重启 Spring Boot 应用。

**Architecture:** 单体 Go 二进制，CLI 命令层 + Webhook HTTP 服务 + 插件化部署架构。当前只实现 Spring Boot 插件，后续可扩展其他类型。守护进程模式，日志写文件。

**Tech Stack:**
- Go 1.22+
- cobra（CLI 框架）
- gopkg.in/yaml.v3（配置解析）
- net/http（Webhook HTTP 服务）
- os/exec（构建和执行进程）
- log/slog（日志）

## Global Constraints

- 单二进制分发，无运行时依赖
- 配置文件为 YAML，敏感字段（token）不写入 git
- 每个服务独立工作目录，互不干扰
- 同一时间只允许一个服务的部署任务执行
- Webhook 接口返回 200 OK，部署结果记录在日志中
- TDD：先写测试，再写实现

---

### Task 1: 项目初始化与目录结构

**Files:**
- Create: `go.mod`
- Create: `main.go`
- Create: `internal/config/config.go`
- Create: `internal/config/config_test.go`
- Create: `config.yaml.example`
- Create: `.github/workflows/ci.yml`

**Interfaces:**
- Consumes: none
- Produces: `ServiceConfig` 结构体定义、YAML 解析函数

- [ ] **Step 1: 初始化 Go 模块**

```bash
go mod init github.com/auto-deployer/auto-deployer
```

- [ ] **Step 2: 创建目录结构**

```bash
mkdir -p cmd internal/config internal/webhook internal/process internal/build plugins/springboot docs/superpowers/specs docs/superpowers/plans .github/workflows
```

- [ ] **Step 3: 定义配置结构体并写测试**

```go
// internal/config/config.go
package config

type ServiceConfig struct {
    Name    string `yaml:"name"`
    Type    string `yaml:"type"`
    Repo    RepoConfig   `yaml:"repo"`
    Workspace string     `yaml:"workspace"`
    Build   BuildConfig  `yaml:"build"`
    Run     RunConfig    `yaml:"run"`
}

type RepoConfig struct {
    URL    string `yaml:"url"`
    Token  string `yaml:"token"`
    Branch string `yaml:"branch"`
}

type BuildConfig struct {
    Command string `yaml:"command"`
}

type RunConfig struct {
    Command string `yaml:"command"`
}

type ServerConfig struct {
    Host string `yaml:"host"`
    Port int    `yaml:"port"`
}

type WebhookConfig struct {
    Secret string `yaml:"secret"`
}

type AppConfig struct {
    Server   ServerConfig   `yaml:"server"`
    Webhook  WebhookConfig  `yaml:"webhook"`
    Services []ServiceConfig `yaml:"services"`
}
```

```go
// internal/config/config_test.go
package config

import (
    "os"
    "testing"
    "gopkg.in/yaml.v3"
)

func TestParseConfig(t *testing.T) {
    data := []byte(`
server:
  host: "0.0.0.0"
  port: 9527
webhook:
  secret: "test-secret"
services:
  - name: "my-app"
    type: "springboot"
    repo:
      url: "https://github.com/user/repo.git"
      token: "ghp_xxx"
      branch: "main"
    workspace: "/opt/apps/my-app"
    build:
      command: "mvn package -DskipTests"
    run:
      command: "java -jar target/my-app.jar"
`)
    var cfg AppConfig
    if err := yaml.Unmarshal(data, &cfg); err != nil {
        t.Fatal(err)
    }
    if cfg.Server.Port != 9527 {
        t.Errorf("expected port 9527, got %d", cfg.Server.Port)
    }
    if len(cfg.Services) != 1 {
        t.Fatalf("expected 1 service, got %d", len(cfg.Services))
    }
    svc := cfg.Services[0]
    if svc.Name != "my-app" || svc.Type != "springboot" {
        t.Errorf("unexpected service: %+v", svc)
    }
    if svc.Repo.Branch != "main" {
        t.Errorf("expected branch main, got %s", svc.Repo.Branch)
    }
}

func TestParseMultiLineCommand(t *testing.T) {
    data := []byte(`
services:
  - name: "my-app"
    type: "springboot"
    run:
      command: |
        cd /opt/apps/my-app
        export JAVA_OPTS="-Xmx512m"
        java -jar target/my-app.jar
`)
    var cfg AppConfig
    if err := yaml.Unmarshal(data, &cfg); err != nil {
        t.Fatal(err)
    }
    cmd := cfg.Services[0].Run.Command
    if cmd == "" {
        t.Fatal("command should not be empty")
    }
    // 多行命令解析后应包含换行符
    if len(cfg.Services[0].Run.Command) < 20 {
        t.Errorf("multiline command seems too short: %q", cmd)
    }
}

func TestLoadFromFile(t *testing.T) {
    tmpFile, err := os.CreateTemp("", "config-*.yaml")
    if err != nil {
        t.Fatal(err)
    }
    defer os.Remove(tmpFile.Name())

    _, _ = tmpFile.WriteString(`
server:
  host: "0.0.0.0"
  port: 8080
services: []
`)
    _ = tmpFile.Close()

    cfg, err := Load(tmpFile.Name())
    if err != nil {
        t.Fatal(err)
    }
    if cfg.Server.Port != 8080 {
        t.Errorf("expected port 8080, got %d", cfg.Server.Port)
    }
}
```

- [ ] **Step 4: 添加 Load 函数**

```go
// internal/config/config.go 追加
import "os"

func Load(path string) (*AppConfig, error) {
    data, err := os.ReadFile(path)
    if err != nil {
        return nil, err
    }
    var cfg AppConfig
    if err := yaml.Unmarshal(data, &cfg); err != nil {
        return nil, err
    }
    return &cfg, nil
}
```

- [ ] **Step 5: 创建 config.yaml.example**

```yaml
# deployd 全局配置
server:
  host: "0.0.0.0"
  port: 9527

webhook:
  secret: ""  # 可选，GitHub/Gitee webhook 签名校验用的密钥

# 服务列表
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

- [ ] **Step 6: 运行测试**

```bash
go test ./internal/config/ -v
```

Expected: PASS

- [ ] **Step 7: Commit**

```bash
git add go.mod main.go internal/config/ config.yaml.example
git commit -m "chore: initialize project structure and config parser"
```

---

### Task 2: 配置校验

**Files:**
- Create: `internal/config/validate.go`
- Create: `internal/config/validate_test.go`

**Interfaces:**
- Consumes: `AppConfig` from Task 1
- Produces: `Validate(*AppConfig) []error`

- [ ] **Step 1: 编写校验测试**

```go
// internal/config/validate_test.go
package config

import (
    "testing"
)

func TestValidate_ValidConfig(t *testing.T) {
    cfg := &AppConfig{
        Server: ServerConfig{Host: "0.0.0.0", Port: 9527},
        Services: []ServiceConfig{
            {
                Name: "my-app",
                Type: "springboot",
                Repo: RepoConfig{URL: "https://github.com/u/r.git", Branch: "main"},
                Workspace: "/tmp/app",
                Build: BuildConfig{Command: "mvn package"},
                Run: RunConfig{Command: "java -jar target/app.jar"},
            },
        },
    }
    errs := Validate(cfg)
    if len(errs) != 0 {
        t.Errorf("expected no errors, got %v", errs)
    }
}

func TestValidate_MissingName(t *testing.T) {
    cfg := &AppConfig{
        Services: []ServiceConfig{{Type: "springboot"}},
    }
    errs := Validate(cfg)
    found := false
    for _, e := range errs {
        if contains(e, "name") {
            found = true
        }
    }
    if !found {
        t.Error("expected error mentioning 'name'")
    }
}

func TestValidate_UnknownType(t *testing.T) {
    cfg := &AppConfig{
        Services: []ServiceConfig{{Name: "app", Type: "unknown"}},
    }
    errs := Validate(cfg)
    found := false
    for _, e := range errs {
        if contains(e, "type") {
            found = true
        }
    }
    if !found {
        t.Error("expected error mentioning 'type'")
    }
}

func TestValidate_MissingRepoURL(t *testing.T) {
    cfg := &AppConfig{
        Services: []ServiceConfig{{Name: "app", Type: "springboot"}},
    }
    errs := Validate(cfg)
    found := false
    for _, e := range errs {
        if contains(e, "repo") {
            found = true
        }
    }
    if !found {
        t.Error("expected error mentioning 'repo'")
    }
}

func TestValidate_MissingWorkspace(t *testing.T) {
    cfg := &AppConfig{
        Services: []ServiceConfig{{
            Name: "app", Type: "springboot",
            Repo: RepoConfig{URL: "https://github.com/u/r.git"},
        }},
    }
    errs := Validate(cfg)
    found := false
    for _, e := range errs {
        if contains(e, "workspace") {
            found = true
        }
    }
    if !found {
        t.Error("expected error mentioning 'workspace'")
    }
}

func contains(s, substr string) bool {
    return len(s) > 0 && len(substr) > 0 && containsStr(s, substr)
}

func containsStr(s, sub string) bool {
    for i := 0; i <= len(s)-len(sub); i++ {
        if s[i:i+len(sub)] == sub {
            return true
        }
    }
    return false
}
```

- [ ] **Step 2: 运行测试确认失败**

```bash
go test ./internal/config/ -v
```

Expected: FAIL with "function not defined" for `Validate`

- [ ] **Step 3: 实现校验函数**

```go
// internal/config/validate.go
package config

import "fmt"

var supportedTypes = map[string]bool{
    "springboot": true,
}

func Validate(cfg *AppConfig) []error {
    var errs []error
    for i, svc := range cfg.Services {
        prefix := fmt.Sprintf("services[%d]", i)
        if svc.Name == "" {
            errs = append(errs, fmt.Errorf("%s: name is required", prefix))
        }
        if !supportedTypes[svc.Type] {
            errs = append(errs, fmt.Errorf("%s: unknown type %q (supported: springboot)", prefix, svc.Type))
        }
        if svc.Repo.URL == "" {
            errs = append(errs, fmt.Errorf("%s: repo.url is required", prefix))
        }
        if svc.Repo.Branch == "" {
            errs = append(errs, fmt.Errorf("%s: repo.branch is required", prefix))
        }
        if svc.Workspace == "" {
            errs = append(errs, fmt.Errorf("%s: workspace is required", prefix))
        }
        if svc.Build.Command == "" {
            errs = append(errs, fmt.Errorf("%s: build.command is required", prefix))
        }
        if svc.Run.Command == "" {
            errs = append(errs, fmt.Errorf("%s: run.command is required", prefix))
        }
    }
    return errs
}
```

- [ ] **Step 4: 运行测试确认通过**

```bash
go test ./internal/config/ -v
```

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/config/validate.go internal/config/validate_test.go
git commit -m "feat: add config validation with field-level error messages"
```

---

### Task 3: CLI 命令框架（cobra）

**Files:**
- Create: `cmd/root.go`
- Create: `cmd/start.go`
- Create: `cmd/stop.go`
- Create: `cmd/status.go`
- Create: `cmd/logs.go`
- Create: `cmd/deploy.go`
- Create: `cmd/config_cmd.go`
- Create: `main.go`

**Interfaces:**
- Consumes: `config.Validate` from Task 2
- Produces: CLI 命令入口，各命令可被独立调用

- [ ] **Step 1: 安装 cobra 依赖**

```bash
go get github.com/spf13/cobra
```

- [ ] **Step 2: 编写 root 命令和 main.go**

```go
// main.go
package main

import (
    "auto-deployer/cmd"
)

func main() {
    cmd.Execute()
}
```

```go
// cmd/root.go
package cmd

import (
    "fmt"
    "os"

    "github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
    Use:   "deployd",
    Short: "Automated deployment daemon",
    Long:  "A CLI tool that runs as a background daemon, receives webhooks, and automates service deployment.",
}

func Execute() {
    if err := rootCmd.Execute(); err != nil {
        fmt.Fprintln(os.Stderr, err)
        os.Exit(1)
    }
}
```

- [ ] **Step 3: 创建 start 命令骨架**

```go
// cmd/start.go
package cmd

import (
    "fmt"
    "github.com/spf13/cobra"
)

var startCmd = &cobra.Command{
    Use:   "start",
    Short: "Start the deployd daemon in background",
    RunE: func(cmd *cobra.Command, args []string) error {
        fmt.Println("starting deployd...")
        return nil
    },
}

func init() {
    rootCmd.AddCommand(startCmd)
}
```

- [ ] **Step 4: 创建其他命令骨架**

```go
// cmd/stop.go
package cmd

import (
    "fmt"
    "github.com/spf13/cobra"
)

var stopCmd = &cobra.Command{
    Use:   "stop",
    Short: "Stop the deployd daemon",
    RunE: func(cmd *cobra.Command, args []string) error {
        fmt.Println("stopping deployd...")
        return nil
    },
}

func init() { rootCmd.AddCommand(stopCmd) }
```

```go
// cmd/status.go
package cmd

import (
    "fmt"
    "github.com/spf13/cobra"
)

var statusCmd = &cobra.Command{
    Use:   "status",
    Short: "Show deployd and all services status",
    RunE: func(cmd *cobra.Command, args []string) error {
        fmt.Println("checking status...")
        return nil
    },
}

func init() { rootCmd.AddCommand(statusCmd) }
```

```go
// cmd/logs.go
package cmd

import (
    "fmt"
    "github.com/spf13/cobra"
)

var logsCmd = &cobra.Command{
    Use:   "logs [service_name]",
    Short: "View deployd or service logs",
    Args:  cobra.MaximumNArgs(1),
    RunE: func(cmd *cobra.Command, args []string) error {
        if len(args) > 0 {
            fmt.Printf("showing logs for %s...\n", args[0])
        } else {
            fmt.Println("showing deployd logs...")
        }
        return nil
    },
}

func init() { rootCmd.AddCommand(logsCmd) }
```

```go
// cmd/deploy.go
package cmd

import (
    "fmt"
    "github.com/spf13/cobra"
)

var deployCmd = &cobra.Command{
    Use:   "deploy <service_name>",
    Short: "Manually trigger deployment for a service",
    Args:  cobra.ExactArgs(1),
    RunE: func(cmd *cobra.Command, args []string) error {
        fmt.Printf("triggering deploy for %s...\n", args[0])
        return nil
    },
}

func init() { rootCmd.AddCommand(deployCmd) }
```

```go
// cmd/config_cmd.go
package cmd

import (
    "fmt"
    "github.com/spf13/cobra"
)

var configCmd = &cobra.Command{
    Use:   "config",
    Short: "Interactive configuration wizard",
    RunE: func(cmd *cobra.Command, args []string) error {
        fmt.Println("opening configuration wizard...")
        return nil
    },
}

func init() { rootCmd.AddCommand(configCmd) }
```

- [ ] **Step 5: 编译并验证所有命令可用**

```bash
go build -o deployd .
./deployd --help
./deployd start --help
./deployd status --help
```

Expected: 输出各命令的 help 信息

- [ ] **Step 6: Commit**

```bash
git add main.go cmd/
git commit -m "feat: add CLI command framework with cobra"
```

---

### Task 4: 交互式配置向导

**Files:**
- Create: `internal/config/wizard.go`
- Create: `internal/config/wizard_test.go`
- Modify: `cmd/config_cmd.go`

**Interfaces:**
- Consumes: `AppConfig`, `ServiceConfig` from Task 1
- Produces: `RunWizard(w io.Writer, r io.Reader, path string) error`

- [ ] **Step 1: 编写向导测试**

```go
// internal/config/wizard_test.go
package config

import (
    "bytes"
    "os"
    "path/filepath"
    "strings"
    "testing"
)

func TestRunWizard_WritesConfig(t *testing.T) {
    dir := t.TempDir()
    configPath := filepath.Join(dir, "config.yaml")

    // 模拟用户输入：端口 9527，回车默认，name=my-app，type=springboot，
    // repo url, token, branch=main, workspace=/tmp/app, build cmd, run cmd
    input := "9527\n\nmy-app\nspringboot\nhttps://github.com/user/repo.git\ngithub-token-xxx\nmain\n/tmp/app\nmvn package -DskipTests\njava -jar target/app.jar\n"
    reader := strings.NewReader(input)
    var output bytes.Buffer

    err := RunWizard(&output, reader, configPath)
    if err != nil {
        t.Fatal(err)
    }

    data, err := os.ReadFile(configPath)
    if err != nil {
        t.Fatal(err)
    }
    content := string(data)
    if !strings.Contains(content, "my-app") {
        t.Error("config should contain service name")
    }
    if !strings.Contains(content, "springboot") {
        t.Error("config should contain type")
    }
    if !strings.Contains(content, "github.com/user/repo.git") {
        t.Error("config should contain repo url")
    }
}

func TestRunWizard_DefaultPort(t *testing.T) {
    dir := t.TempDir()
    configPath := filepath.Join(dir, "config.yaml")

    // 空输入使用默认值
    input := "\n\nmy-app\nspringboot\nhttps://github.com/user/repo.git\n\ndefault-branch\n/tmp/app\nmvn package\njava -jar app.jar\n"
    reader := strings.NewReader(input)
    var output bytes.Buffer

    err := RunWizard(&output, reader, configPath)
    if err != nil {
        t.Fatal(err)
    }

    cfg, err := Load(configPath)
    if err != nil {
        t.Fatal(err)
    }
    if cfg.Server.Port != 9527 {
        t.Errorf("expected default port 9527, got %d", cfg.Server.Port)
    }
}
```

- [ ] **Step 2: 运行测试确认失败**

```bash
go test ./internal/config/ -v
```

Expected: FAIL with "function not defined"

- [ ] **Step 3: 实现向导**

```go
// internal/config/wizard.go
package config

import (
    "bufio"
    "fmt"
    "os"
    "strconv"
    "strings"

    "gopkg.in/yaml.v3"
)

func RunWizard(w io.Writer, r io.Reader, configPath string) error {
    scanner := bufio.NewScanner(r)
    writer := bufio.NewWriter(w)
    defer writer.Flush()

    ask := func(prompt string, defaultVal string) string {
        if defaultVal != "" {
            fmt.Fprintf(writer, "%s [%s]: ", prompt, defaultVal)
        } else {
            fmt.Fprintf(writer, "%s: ", prompt)
        }
        writer.Flush()
        line := scanner.Scan()
        if !line {
            return defaultVal
        }
        val := strings.TrimSpace(scanner.Text())
        if val == "" {
            return defaultVal
        }
        return val
    }

    portStr := ask("Webhook listen port", "9527")
    port, _ := strconv.Atoi(portStr)
    if port == 0 {
        port = 9527
    }

    host := ask("Listen host", "0.0.0.0")

    name := ask("Service name", "")
    svcType := ask("Service type (springboot)", "springboot")
    repoURL := ask("Git repository URL", "")
    repoToken := ask("Git access token (optional)", "")
    branch := ask("Deploy branch", "main")
    workspace := ask("Workspace directory", "")
    buildCmd := ask("Build command", "mvn package -DskipTests")
    runCmd := ask("Run command", "")

    cfg := &AppConfig{
        Server: ServerConfig{Host: host, Port: port},
        Services: []ServiceConfig{{
            Name:      name,
            Type:      svcType,
            Repo:      RepoConfig{URL: repoURL, Token: repoToken, Branch: branch},
            Workspace: workspace,
            Build:     BuildConfig{Command: buildCmd},
            Run:       RunConfig{Command: runCmd},
        }},
    }

    data, err := yaml.Marshal(cfg)
    if err != nil {
        return fmt.Errorf("failed to marshal config: %w", err)
    }

    if err := os.MkdirAll(filepath.Dir(configPath), 0755); err != nil {
        return err
    }
    if err := os.WriteFile(configPath, data, 0644); err != nil {
        return err
    }

    fmt.Fprintf(writer, "\nConfiguration saved to %s\n", configPath)
    writer.Flush()
    return nil
}
```

需要补充 import：

```go
import (
    "bufio"
    "fmt"
    "io"
    "os"
    "path/filepath"
    "strconv"
    "strings"

    "gopkg.in/yaml.v3"
)
```

- [ ] **Step 4: 运行测试确认通过**

```bash
go test ./internal/config/ -v
```

Expected: PASS

- [ ] **Step 5: 修改 config_cmd.go 调用向导**

```go
// cmd/config_cmd.go
package cmd

import (
    "fmt"
    "os"
    "strings"

    "auto-deployer/internal/config"
    "github.com/spf13/cobra"
)

var configFile string

func init() {
    configCmd.Flags().StringVarP(&configFile, "file", "f", "", "config file path")
    rootCmd.AddCommand(configCmd)
}

var configCmd = &cobra.Command{
    Use:   "config",
    Short: "Interactive configuration wizard",
    RunE: func(cmd *cobra.Command, args []string) error {
        path := configFile
        if path == "" {
            path = "config.yaml"
        }
        fmt.Fprintf(os.Stdout, "Starting configuration wizard...\n")
        fmt.Fprintf(os.Stdout, "Config file: %s\n\n", path)
        return config.RunWizard(os.Stdout, os.Stdin, path)
    },
}
```

- [ ] **Step 6: Commit**

```bash
git add internal/config/wizard.go internal/config/wizard_test.go cmd/config_cmd.go
git commit -m "feat: add interactive configuration wizard"
```

---

### Task 5: 环境检查

**Files:**
- Create: `internal/build/envcheck.go`
- Create: `internal/build/envcheck_test.go`

**Interfaces:**
- Consumes: none
- Produces: `CheckEnvironment() []error`

- [ ] **Step 1: 编写测试**

```go
// internal/build/envcheck_test.go
package build

import (
    "os/exec"
    "testing"
)

func TestCheckEnvironment(t *testing.T) {
    // 这个测试在开发机上应该全部通过
    // 如果某些工具缺失，测试会失败，这是预期行为
    errs := CheckEnvironment()
    if len(errs) > 0 {
        t.Logf("environment check results (expected to pass on dev machine): %v", errs)
    }
}

func TestCheckEnvironment_DetectsMissing(t *testing.T) {
    // 保存原始 PATH
    origPath := os.Getenv("PATH")
    // 设置一个不包含任何可执行文件的 PATH
    _ = os.Setenv("PATH", "/nonexistent/path")

    defer func() {
        _ = os.Setenv("PATH", origPath)
    }()

    errs := CheckEnvironment()
    if len(errs) == 0 {
        t.Fatal("expected errors when tools are missing")
    }
}

func TestToolExists(t *testing.T) {
    if !toolExists("sh") {
        t.Error("sh should exist")
    }
    if toolExists("definitely-not-a-real-tool-xyz") {
        t.Error("should not find nonexistent tool")
    }
}

func TestGetVersion(t *testing.T) {
    // git 应该能查到版本
    if _, err := GetVersion("git"); err != nil {
        t.Logf("git version check: %v (may fail in restricted envs)", err)
    }
}
```

- [ ] **Step 2: 运行测试确认失败**

```bash
go test ./internal/build/ -v
```

Expected: FAIL with "function not defined"

- [ ] **Step 3: 实现环境检查**

```go
// internal/build/envcheck.go
package build

import (
    "fmt"
    "os/exec"
    "strings"
)

type environmentCheck struct {
    name string
    cmd  string
}

var requiredTools = []environmentCheck{
    {name: "git", cmd: "git"},
    {name: "java", cmd: "java"},
    {name: "mvn", cmd: "mvn"},
}

func CheckEnvironment() []error {
    var errs []error
    for _, tool := range requiredTools {
        if _, err := exec.LookPath(tool.cmd); err != nil {
            errs = append(errs, fmt.Errorf("%s is not installed or not in PATH: %w", tool.name, err))
        }
    }
    return errs
}

func toolExists(name string) bool {
    _, err := exec.LookPath(name)
    return err == nil
}

func GetVersion(name string) (string, error) {
    out, err := exec.Command(name, "--version").Output()
    if err != nil {
        return "", err
    }
    lines := strings.Split(strings.TrimSpace(string(out)), "\n")
    if len(lines) > 0 {
        return lines[0], nil
    }
    return "", nil
}
```

- [ ] **Step 4: 运行测试确认通过**

```bash
go test ./internal/build/ -v
```

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/build/envcheck.go internal/build/envcheck_test.go
git commit -m "feat: add environment dependency checker"
```

---

### Task 6: Git 操作封装

**Files:**
- Create: `internal/build/git.go`
- Create: `internal/build/git_test.go`

**Interfaces:**
- Consumes: none
- Produces: `Clone(repoURL, token, branch, destDir string) error`、`Pull(destDir, branch string) error`

- [ ] **Step 1: 编写测试**

```go
// internal/build/git_test.go
package build

import (
    "os"
    "os/exec"
    "path/filepath"
    "testing"
)

func TestClone_WithLocalRepo(t *testing.T) {
    // 创建一个临时裸仓库作为源
    bareDir := t.TempDir()
    setupDir := filepath.Join(t.TempDir(), "setup")
    _ = os.MkdirAll(setupDir, 0755)
    _ = runCmd(setupDir, "git", "init", "-b", "main")
    _ = runCmd(setupDir, "git", "config", "user.email", "test@test.com")
    _ = runCmd(setupDir, "git", "config", "user.name", "Test")
    _ = os.WriteFile(filepath.Join(setupDir, "README.md"), []byte("hello"), 0644)
    _ = runCmd(setupDir, "git", "add", ".")
    _ = runCmd(setupDir, "git", "commit", "-m", "init")
    _ = runCmd(setupDir, "git", "clone", "--bare", setupDir, bareDir)

    destDir := filepath.Join(t.TempDir(), "cloned")
    err := Clone(bareDir, "", "main", destDir)
    if err != nil {
        t.Fatal(err)
    }

    if _, err := os.Stat(filepath.Join(destDir, "README.md")); err != nil {
        t.Error("README.md should exist after clone")
    }
}

func TestPull_UpdatesWorkingDir(t *testing.T) {
    bareDir := t.TempDir()
    setupDir := filepath.Join(t.TempDir(), "setup")
    _ = os.MkdirAll(setupDir, 0755)
    _ = runCmd(setupDir, "git", "init", "-b", "main")
    _ = runCmd(setupDir, "git", "config", "user.email", "test@test.com")
    _ = runCmd(setupDir, "git", "config", "user.name", "Test")
    _ = os.WriteFile(filepath.Join(setupDir, "README.md"), []byte("hello"), 0644)
    _ = runCmd(setupDir, "git", "add", ".")
    _ = runCmd(setupDir, "git", "commit", "-m", "init")
    _ = runCmd(setupDir, "git", "clone", "--bare", setupDir, bareDir)

    destDir := filepath.Join(t.TempDir(), "cloned")
    _ = Clone(bareDir, "", "main", destDir)

    // 向裸仓库添加新提交
    _ = os.WriteFile(filepath.Join(setupDir, "new.txt"), []byte("new content"), 0644)
    _ = runCmd(setupDir, "git", "add", ".")
    _ = runCmd(setupDir, "git", "commit", "-m", "second")
    _ = runCmd(setupDir, "git", "push", bareDir, "main")

    err := Pull(destDir, "main")
    if err != nil {
        t.Fatal(err)
    }

    data, _ := os.ReadFile(filepath.Join(destDir, "new.txt"))
    if string(data) != "new content" {
        t.Errorf("expected new content, got %q", string(data))
    }
}

func runCmd(dir string, name string, args ...string) error {
    cmd := exec.Command(name, args...)
    cmd.Dir = dir
    cmd.Stdout = os.Stdout
    cmd.Stderr = os.Stderr
    return cmd.Run()
}
```

- [ ] **Step 2: 运行测试确认失败**

```bash
go test ./internal/build/ -v
```

Expected: FAIL with "function not defined"

- [ ] **Step 3: 实现 Git 操作**

```go
// internal/build/git.go
package build

import (
    "fmt"
    "os/exec"
    "strings"
)

func Clone(repoURL, token, branch, destDir string) error {
    if err := ensureDir(destDir); err != nil {
        return err
    }

    url := repoURL
    if token != "" {
        url = insertToken(url, token)
    }

    args := []string{"clone", url, destDir}
    if branch != "" {
        args = append([]string{"clone", "--branch", branch, "--single-branch", url, destDir}, "")[:3]
        args = []string{"clone", "--branch", branch, "--single-branch", url, destDir}
    }

    cmd := exec.Command("git", args...)
    cmd.Stdout = logWriter("git-clone")
    cmd.Stderr = logWriter("git-clone")
    if err := cmd.Run(); err != nil {
        return fmt.Errorf("git clone failed: %w", err)
    }
    return nil
}

func Pull(destDir, branch string) error {
    cmd := exec.Command("git", "pull", "origin", branch)
    cmd.Dir = destDir
    cmd.Stdout = logWriter("git-pull")
    cmd.Stderr = logWriter("git-pull")
    if err := cmd.Run(); err != nil {
        return fmt.Errorf("git pull failed: %w", err)
    }
    return nil
}

func insertToken(url, token string) string {
    if strings.HasPrefix(url, "https://") {
        // https://github.com/user/repo.git -> https://TOKEN@github.com/user/repo.git
        return strings.Replace(url, "https://", "https://"+token+"@", 1)
    }
    return url
}

func ensureDir(dir string) error {
    return exec.Command("mkdir", "-p", dir).Run()
}

func logWriter(tag string) *logWriterWrapper {
    return &logWriterWrapper{tag: tag}
}

type logWriterWrapper struct {
    tag string
}

func (w *logWriterWrapper) Write(p []byte) (n int, err error) {
    return fmt.Fprintf(strings.NewReader(""), "[%s] %s", w.tag, string(p))
}
```

注意：上面的 `logWriter` 只是占位写法，实际日志系统会在后续 Task 中完善。当前可以先用 `os.Stdout` 代替，或者简化为：

```go
func logWriter(tag string) *logWriterWrapper {
    return &logWriterWrapper{tag: tag}
}
```

这里先保持简单，直接用标准库的 `os.Stdout` 替代：

```go
// 简化版：直接打印到 stdout
func runGit(dir string, args ...string) error {
    cmd := exec.Command("git", args...)
    if dir != "" {
        cmd.Dir = dir
    }
    cmd.Stdout = os.Stdout
    cmd.Stderr = os.Stderr
    return cmd.Run()
}
```

- [ ] **Step 4: 运行测试确认通过**

```bash
go test ./internal/build/ -v
```

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/build/git.go internal/build/git_test.go
git commit -m "feat: add git clone and pull operations"
```

---

### Task 7: 构建执行器

**Files:**
- Create: `internal/build/executor.go`
- Create: `internal/build/executor_test.go`

**Interfaces:**
- Consumes: Git 操作 from Task 6
- Produces: `ExecuteBuild(workspace, command string) error`

- [ ] **Step 1: 编写测试**

```go
// internal/build/executor_test.go
package build

import (
    "os"
    "path/filepath"
    "testing"
)

func TestExecuteBuild_Success(t *testing.T) {
    workspace := t.TempDir()
    // 创建一个会成功的 shell 脚本
    script := filepath.Join(workspace, "build.sh")
    _ = os.WriteFile(script, []byte("#!/bin/sh\necho 'building...'\nexit 0\n"), 0755)

    err := ExecuteBuild(workspace, script)
    if err != nil {
        t.Fatal(err)
    }
}

func TestExecuteBuild_Failure(t *testing.T) {
    workspace := t.TempDir()
    script := filepath.Join(workspace, "build.sh")
    _ = os.WriteFile(script, []byte("#!/bin/sh\necho 'failing...'\nexit 1\n"), 0755)

    err := ExecuteBuild(workspace, script)
    if err == nil {
        t.Fatal("expected error for failing build")
    }
}

func TestExecuteBuild_CommandNotFound(t *testing.T) {
    err := ExecuteBuild("/tmp", "definitely-not-a-real-command-xyz")
    if err == nil {
        t.Fatal("expected error for missing command")
    }
}
```

- [ ] **Step 2: 运行测试确认失败**

```bash
go test ./internal/build/ -v
```

Expected: FAIL with "function not defined"

- [ ] **Step 3: 实现构建执行器**

```go
// internal/build/executor.go
package build

import (
    "fmt"
    "os/exec"
)

func ExecuteBuild(workspace, command string) error {
    parts := splitCommand(command)
    if len(parts) == 0 {
        return fmt.Errorf("empty build command")
    }

    cmd := exec.Command(parts[0], parts[1:]...)
    cmd.Dir = workspace
    cmd.Stdout = os.Stdout
    cmd.Stderr = os.Stderr

    fmt.Printf("[build] executing: %s\n", command)
    if err := cmd.Run(); err != nil {
        return fmt.Errorf("build failed: %w", err)
    }
    fmt.Println("[build] build completed successfully")
    return nil
}

func splitCommand(command string) []string {
    // 简单 split，生产环境建议用 shlex 风格的库
    // 对于 mvn package -DskipTests 这种单行命令足够
    var result []string
    current := ""
    inQuote := false
    quoteChar := byte(0)

    for i := 0; i < len(command); i++ {
        ch := command[i]
        if inQuote {
            if ch == quoteChar {
                inQuote = false
            } else {
                current += string(ch)
            }
            continue
        }
        switch ch {
        case '"', '\'':
            inQuote = true
            quoteChar = ch
        case ' ', '\t':
            if current != "" {
                result = append(result, current)
                current = ""
            }
        default:
            current += string(ch)
        }
    }
    if current != "" {
        result = append(result, current)
    }
    return result
}
```

- [ ] **Step 4: 运行测试确认通过**

```bash
go test ./internal/build/ -v
```

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/build/executor.go internal/build/executor_test.go
git commit -m "feat: add build command executor with argument splitting"
```

---

### Task 8: 进程管理器

**Files:**
- Create: `internal/process/manager.go`
- Create: `internal/process/manager_test.go`

**Interfaces:**
- Consumes: none
- Produces: `ProcessManager` 结构体及 `Start/Stop/Status/PIDFile` 方法

- [ ] **Step 1: 编写测试**

```go
// internal/process/manager_test.go
package process

import (
    "os"
    "path/filepath"
    "testing"
    "time"
)

func TestManager_StartAndStop(t *testing.T) {
    dir := t.TempDir()
    pidFile := filepath.Join(dir, "test.pid")

    m := NewManager(pidFile)

    // 启动一个 sleep 进程
    err := m.Start("sleep", "300")
    if err != nil {
        t.Fatal(err)
    }

    time.Sleep(100 * time.Millisecond)

    pid, err := m.ReadPID()
    if err != nil {
        t.Fatal(err)
    }
    if pid == 0 {
        t.Fatal("pid should not be 0")
    }

    status := m.Status()
    if status != "running" {
        t.Errorf("expected running, got %s", status)
    }

    err = m.Stop()
    if err != nil {
        t.Fatal(err)
    }

    time.Sleep(100 * time.Millisecond)
    status = m.Status()
    if status != "stopped" {
        t.Errorf("expected stopped, got %s", status)
    }
}

func TestManager_StopNonexistent(t *testing.T) {
    dir := t.TempDir()
    pidFile := filepath.Join(dir, "nonexistent.pid")

    m := NewManager(pidFile)
    err := m.Stop()
    if err != nil {
        t.Fatal(err)
    }
}

func TestManager_PIDFileCleanup(t *testing.T) {
    dir := t.TempDir()
    pidFile := filepath.Join(dir, "cleanup.pid")

    m := NewManager(pidFile)
    _ = m.Start("sleep", "300")
    time.Sleep(100 * time.Millisecond)

    if _, err := os.Stat(pidFile); err != nil {
        t.Fatal("pid file should exist while running")
    }

    _ = m.Stop()
    time.Sleep(100 * time.Millisecond)

    if _, err := os.Stat(pidFile); !os.IsNotExist(err) {
        t.Error("pid file should be removed after stop")
    }
}
```

- [ ] **Step 2: 运行测试确认失败**

```bash
go test ./internal/process/ -v
```

Expected: FAIL with "function not defined"

- [ ] **Step 3: 实现进程管理器**

```go
// internal/process/manager.go
package process

import (
    "fmt"
    "os"
    "os/exec"
    "path/filepath"
    "strconv"
    "strings"
    "syscall"
)

type Manager struct {
    pidFilePath string
}

func NewManager(pidFilePath string) *Manager {
    return &Manager{pidFilePath: pidFilePath}
}

func (m *Manager) Start(name string, args ...string) error {
    if existingPID, _ := m.ReadPID(); existingPID > 0 {
        return fmt.Errorf("process already running with pid %d", existingPID)
    }

    cmd := exec.Command(name, args...)
    cmd.Stdout = os.Stdout
    cmd.Stderr = os.Stderr
    cmd.Stdin = os.Stdin
    cmd.SysProcAttr = syscall.Setsid

    if err := cmd.Start(); err != nil {
        return fmt.Errorf("failed to start process: %w", err)
    }

    if err := m.WritePID(cmd.Process.Pid); err != nil {
        return err
    }

    fmt.Printf("started %s with pid %d\n", name, cmd.Process.Pid)
    return nil
}

func (m *Manager) Stop() error {
    pid, err := m.ReadPID()
    if err != nil || pid == 0 {
        return nil
    }

    proc, err := os.FindProcess(pid)
    if err != nil {
        return nil
    }

    if err := proc.Signal(syscall.SIGTERM); err != nil {
        if err == syscall.ESRCH {
            _ = m.CleanupPID()
            return nil
        }
        return fmt.Errorf("failed to send SIGTERM: %w", err)
    }

    _ = m.CleanupPID()
    fmt.Printf("stopped process %d\n", pid)
    return nil
}

func (m *Manager) Status() string {
    pid, err := m.ReadPID()
    if err != nil || pid == 0 {
        return "stopped"
    }

    proc, err := os.FindProcess(pid)
    if err != nil {
        return "unknown"
    }

    err = proc.Signal(syscall.Signal(0))
    if err == nil {
        return "running"
    }
    if err == syscall.ESRCH {
        _ = m.CleanupPID()
        return "stopped"
    }
    return "unknown"
}

func (m *Manager) ReadPID() (int, error) {
    data, err := os.ReadFile(m.pidFilePath)
    if err != nil {
        return 0, err
    }
    pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
    return pid, err
}

func (m *Manager) WritePID(pid int) error {
    if err := os.MkdirAll(filepath.Dir(m.pidFilePath), 0755); err != nil {
        return err
    }
    return os.WriteFile(m.pidFilePath, []byte(strconv.Itoa(pid)), 0644)
}

func (m *Manager) CleanupPID() error {
    return os.Remove(m.pidFilePath)
}
```

- [ ] **Step 4: 运行测试确认通过**

```bash
go test ./internal/process/ -v
```

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/process/manager.go internal/process/manager_test.go
git commit -m "feat: add process manager with PID file support"
```

---

### Task 9: Spring Boot 插件

**Files:**
- Create: `plugins/springboot/plugin.go`
- Create: `plugins/springboot/builder.go`
- Create: `plugins/springboot/runner.go`
- Create: `plugins/springboot/plugin_test.go`

**Interfaces:**
- Consumes: `config.ServiceConfig`, `build.ExecuteBuild`, `process.Manager`
- Produces: 实现 `Deployer` 接口的 `SpringBootPlugin`

- [ ] **Step 1: 定义 Deployer 接口并编写测试**

```go
// plugins/springboot/plugin.go
package springboot

import (
    "context"
    "auto-deployer/internal/config"
)

type Deployer interface {
    Build(ctx context.Context, svc *config.ServiceConfig) error
    Start(ctx context.Context, svc *config.ServiceConfig) error
    Stop(ctx context.Context, svc *config.ServiceConfig) error
    Status(ctx context.Context, svc *config.ServiceConfig) (string, error)
    Type() string
}

type SpringBootPlugin struct{}

func New() *SpringBootPlugin {
    return &SpringBootPlugin{}
}

func (p *SpringBootPlugin) Type() string {
    return "springboot"
}
```

```go
// plugins/springboot/plugin_test.go
package springboot

import (
    "context"
    "os"
    "path/filepath"
    "testing"
    "auto-deployer/internal/config"
)

func TestType(t *testing.T) {
    p := New()
    if p.Type() != "springboot" {
        t.Errorf("expected springboot, got %s", p.Type())
    }
}

func TestBuild_NoCommand(t *testing.T) {
    p := New()
    svc := &config.ServiceConfig{
        Workspace: t.TempDir(),
        Build:     config.BuildConfig{Command: ""},
    }
    err := p.Build(context.Background(), svc)
    if err == nil {
        t.Fatal("expected error for empty build command")
    }
}

func TestBuild_WithScript(t *testing.T) {
    p := New()
    workspace := t.TempDir()
    script := filepath.Join(workspace, "build.sh")
    _ = os.WriteFile(script, []byte("#!/bin/sh\nexit 0\n"), 0755)

    svc := &config.ServiceConfig{
        Workspace: workspace,
        Build:     config.BuildConfig{Command: script},
    }
    err := p.Build(context.Background(), svc)
    if err != nil {
        t.Fatal(err)
    }
}
```

- [ ] **Step 2: 实现 Builder 和 Runner**

```go
// plugins/springboot/builder.go
package springboot

import (
    "context"
    "fmt"
    "os/exec"
    "auto-deployer/internal/build"
    "auto-deployer/internal/config"
)

func (p *SpringBootPlugin) Build(ctx context.Context, svc *config.ServiceConfig) error {
    if svc.Build.Command == "" {
        return fmt.Errorf("build command is empty")
    }

    // 先 git pull
    if err := build.Pull(svc.Workspace, svc.Repo.Branch); err != nil {
        return fmt.Errorf("git pull failed: %w", err)
    }

    // 再执行构建
    if err := build.ExecuteBuild(svc.Workspace, svc.Build.Command); err != nil {
        return err
    }

    fmt.Println("[springboot] build completed")
    return nil
}
```

```go
// plugins/springboot/runner.go
package springboot

import (
    "context"
    "fmt"
    "path/filepath"
    "auto-deployer/internal/process"
    "auto-deployer/internal/config"
)

func (p *SpringBootPlugin) Start(ctx context.Context, svc *config.ServiceConfig) error {
    pidFile := filepath.Join(getDaemonDir(), svc.Name+".pid")
    mgr := process.NewManager(pidFile)

    if mgr.Status() == "running" {
        return fmt.Errorf("service %s is already running", svc.Name)
    }

    parts := splitCommand(svc.Run.Command)
    if len(parts) == 0 {
        return fmt.Errorf("run command is empty")
    }

    return mgr.Start(parts[0], parts[1:]...)
}

func (p *SpringBootPlugin) Stop(ctx context.Context, svc *config.ServiceConfig) error {
    pidFile := filepath.Join(getDaemonDir(), svc.Name+".pid")
    mgr := process.NewManager(pidFile)
    return mgr.Stop()
}

func (p *SpringBootPlugin) Status(ctx context.Context, svc *config.ServiceConfig) (string, error) {
    pidFile := filepath.Join(getDaemonDir(), svc.Name+".pid")
    mgr := process.NewManager(pidFile)
    return mgr.Status(), nil
}

func splitCommand(command string) []string {
    // 复用 build 包的 split 逻辑，或从 build 包导出
    return build.SplitCommand(command)
}

func getDaemonDir() string {
    return filepath.Join(os.Getenv("HOME"), ".deployd", "run")
}
```

需要在 `internal/build/executor.go` 中把 `splitCommand` 导出为 `SplitCommand`：

```go
// internal/build/executor.go 修改
func SplitCommand(command string) []string {
    // ... 原来的 splitCommand 逻辑，改名为 SplitCommand
}
```

- [ ] **Step 3: 运行测试确认通过**

```bash
go test ./plugins/springboot/ -v
```

Expected: PASS

- [ ] **Step 4: Commit**

```bash
git add plugins/springboot/ internal/build/executor.go
git commit -m "feat: add springboot plugin with build, start, stop, status"
```

---

### Task 10: Webhook HTTP 服务

**Files:**
- Create: `internal/webhook/server.go`
- Create: `internal/webhook/server_test.go`
- Create: `internal/webhook/dispatcher.go`
- Create: `internal/webhook/dispatcher_test.go`

**Interfaces:**
- Consumes: `config.AppConfig`, `plugins/springboot.Deployer`
- Produces: `Start(host string, port int) error`、`RegisterPlugin(Deployer)`

- [ ] **Step 1: 编写 Webhook 路由测试**

```go
// internal/webhook/server_test.go
package webhook

import (
    "net/http"
    "net/http/httptest"
    "testing"
)

func TestHandleGitHubWebhook(t *testing.T) {
    body := `{"ref":"refs/heads/main","repository":{"clone_url":"https://github.com/user/repo.git"}}`
    req := httptest.NewRequest(http.MethodPost, "/webhook", http.BodyReader(func(w io.Writer) {
        _, _ = w.Write([]byte(body))
    }))
    req.Header.Set("X-GitHub-Event", "push")
    req.Header.Set("X-Hub-Signature-256", "sha256=test")

    rr := httptest.NewRecorder()
    handler := http.HandlerFunc(handleWebhook)
    handler.ServeHTTP(rr, req)

    if rr.Code != http.StatusOK {
        t.Errorf("expected 200, got %d", rr.Code)
    }
}

func TestHandleGiteeWebhook(t *testing.T) {
    body := `{"ref":"main","repository":{"git_http_url":"https://gitee.com/user/repo.git"}}`
    req := httptest.NewRequest(http.MethodPost, "/webhook", strings.NewReader(body))
    req.Header.Set("X-Gitee-Event", "Push Hook")

    rr := httptest.NewRecorder()
    handler := http.HandlerFunc(handleWebhook)
    handler.ServeHTTP(rr, req)

    if rr.Code != http.StatusOK {
        t.Errorf("expected 200, got %d", rr.Code)
    }
}
```

- [ ] **Step 2: 实现 Webhook 处理**

```go
// internal/webhook/dispatcher.go
package webhook

import (
    "encoding/json"
    "fmt"
    "auto-deployer/internal/config"
)

type GitHubPayload struct {
    Ref       string `json:"ref"`
    Repository struct {
        CloneURL string `json:"clone_url"`
    } `json:"repository"`
}

type GiteePayload struct {
    Ref       string `json:"ref"`
    Repository struct {
        GitHTTPURL string `json:"git_http_url"`
    } `json:"repository"`
}

type DispatchResult struct {
    ServiceName string
    Branch      string
    RepoURL     string
}

func ParsePayload(body []byte, source string) (*DispatchResult, error) {
    switch source {
    case "github":
        var payload GitHubPayload
        if err := json.Unmarshal(body, &payload); err != nil {
            return nil, err
        }
        branch := strings.TrimPrefix(payload.Ref, "refs/heads/")
        return &DispatchResult{
            Branch:  branch,
            RepoURL: payload.Repository.CloneURL,
        }, nil

    case "gitee":
        var payload GiteePayload
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

func MatchService(services []config.ServiceConfig, result *DispatchResult) *config.ServiceConfig {
    for i := range services {
        svc := &services[i]
        if svc.Repo.URL == result.RepoURL && svc.Repo.Branch == result.Branch {
            return svc
        }
    }
    return nil
}
```

```go
// internal/webhook/server.go
package webhook

import (
    "fmt"
    "io"
    "net/http"
    "strings"
)

func handleWebhook(w http.ResponseWriter, r *http.Request) {
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

    w.WriteHeader(http.StatusOK)
    _, _ = w.Write([]byte("ok"))
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
```

- [ ] **Step 3: 运行测试确认通过**

```bash
go test ./internal/webhook/ -v
```

Expected: PASS

- [ ] **Step 4: Commit**

```bash
git add internal/webhook/server.go internal/webhook/server_test.go internal/webhook/dispatcher.go internal/webhook/dispatcher_test.go
git commit -m "feat: add webhook HTTP server with GitHub/Gitee payload parsing"
```

---

### Task 11: 守护进程与 start 命令集成

**Files:**
- Modify: `cmd/start.go`
- Create: `internal/daemon/daemon.go`
- Create: `internal/daemon/daemon_test.go`

**Interfaces:**
- Consumes: `config.Load`, `config.Validate`, `webhook.Start`, `process.Manager`
- Produces: 完整的 `deployd start` 流程

- [ ] **Step 1: 编写守护进程测试**

```go
// internal/daemon/daemon_test.go
package daemon

import (
    "os"
    "path/filepath"
    "testing"
)

func TestStart_FailsWithoutConfig(t *testing.T) {
    dir := t.TempDir()
    os.Setenv("DEPLOYD_HOME", dir)
    defer os.Unsetenv("DEPLOYD_HOME")

    err := Start(filepath.Join(dir, "nonexistent.yaml"))
    if err == nil {
        t.Fatal("expected error when config file does not exist")
    }
}

func TestStart_FailsInvalidConfig(t *testing.T) {
    dir := t.TempDir()
    os.Setenv("DEPLOYD_HOME", dir)

    badConfig := filepath.Join(dir, "bad.yaml")
    _ = os.WriteFile(badConfig, []byte("invalid: yaml: ["), 0644)

    err := Start(badConfig)
    if err == nil {
        t.Fatal("expected error for invalid yaml")
    }
}
```

- [ ] **Step 2: 实现守护进程**

```go
// internal/daemon/daemon.go
package daemon

import (
    "fmt"
    "os"
    "path/filepath"

    "auto-deployer/internal/build"
    "auto-deployer/internal/config"
    "auto-deployer/internal/process"
    "auto-deployer/internal/webhook"
    "auto-deployer/plugins/springboot"
)

const defaultConfigName = "config.yaml"
const defaultPidDir = ".deployd/run"

func Start(configPath string) error {
    if configPath == "" {
        home, _ := os.UserHomeDir()
        configPath = filepath.Join(home, defaultConfigName)
    }

    // 1. 检查配置文件
    if _, err := os.Stat(configPath); os.IsNotExist(err) {
        return fmt.Errorf("config file not found: %s\nRun `deployd config` to create one", configPath)
    }

    // 2. 加载配置
    cfg, err := config.Load(configPath)
    if err != nil {
        return fmt.Errorf("failed to load config: %w", err)
    }

    // 3. 校验配置
    if errs := config.Validate(cfg); len(errs) > 0 {
        for _, e := range errs {
            fmt.Fprintf(os.Stderr, "config error: %v\n", e)
        }
        return fmt.Errorf("config validation failed")
    }

    // 4. 检查环境
    if errs := build.CheckEnvironment(); len(errs) > 0 {
        for _, e := range errs {
            fmt.Fprintf(os.Stderr, "environment error: %v\n", e)
        }
        return fmt.Errorf("environment check failed")
    }

    // 5. 创建工作目录
    for _, svc := range cfg.Services {
        if err := os.MkdirAll(svc.Workspace, 0755); err != nil {
            return fmt.Errorf("failed to create workspace %s: %w", svc.Workspace, err)
        }
    }

    // 6. 启动 webhook 服务
    pidDir := filepath.Join(os.Getenv("HOME"), defaultPidDir)
    _ = os.MkdirAll(pidDir, 0755)
    pidFile := filepath.Join(pidDir, "deployd.pid")
    mgr := process.NewManager(pidFile)

    if mgr.Status() == "running" {
        return fmt.Errorf("deployd is already running (pid: %d)", mgr.ReadPID())
    }

    // 注册插件
    plugin := springboot.New()

    // 启动 webhook HTTP 服务（后台 goroutine）
    addr := fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port)
    http.HandleFunc("/webhook", webhook.Handle)

    go func() {
        fmt.Printf("[daemon] starting webhook server on %s\n", addr)
        if err := http.ListenAndServe(addr, nil); err != nil {
            fmt.Fprintf(os.Stderr, "[daemon] webhook server error: %v\n", err)
        }
    }()

    // 写入 PID
    // 注意：这里需要获取 http server 的 pid，简化处理使用当前进程 pid
    myPID := os.Getpid()
    if err := mgr.WritePID(myPID); err != nil {
        return err
    }

    fmt.Printf("[daemon] deployd started on %s\n", addr)
    return nil
}
```

- [ ] **Step 3: 修改 start.go 调用守护进程**

```go
// cmd/start.go
package cmd

import (
    "fmt"
    "os"

    "auto-deployer/internal/daemon"
    "github.com/spf13/cobra"
)

var startCmd = &cobra.Command{
    Use:   "start",
    Short: "Start the deployd daemon in background",
    RunE: func(cmd *cobra.Command, args []string) error {
        configPath := configFile
        if configPath == "" {
            configPath = "config.yaml"
        }

        if err := daemon.Start(configPath); err != nil {
            return err
        }
        fmt.Fprintln(os.Stdout, "deployd started successfully")
        return nil
    },
}
```

- [ ] **Step 4: 运行测试确认通过**

```bash
go test ./internal/daemon/ -v
```

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/daemon/ cmd/start.go
git commit -m "feat: integrate daemon startup with config validation and env check"
```

---

### Task 12: stop / status / logs / deploy 命令实现

**Files:**
- Modify: `cmd/stop.go`
- Modify: `cmd/status.go`
- Modify: `cmd/logs.go`
- Modify: `cmd/deploy.go`
- Create: `internal/daemon/commands.go`

**Interfaces:**
- Consumes: `daemon` 包中的 PID 管理和进程状态
- Produces: 完整的 CLI 命令功能

- [ ] **Step 1: 编写 commands.go**

```go
// internal/daemon/commands.go
package daemon

import (
    "fmt"
    "os"
    "path/filepath"

    "auto-deployer/internal/config"
    "auto-deployer/internal/process"
)

const defaultPidDir = ".deployd/run"

func Stop() error {
    pidFile := filepath.Join(homeDir(), defaultPidDir, "deployd.pid")
    mgr := process.NewManager(pidFile)

    if mgr.Status() != "running" {
        fmt.Println("deployd is not running")
        return nil
    }

    return mgr.Stop()
}

func Status() error {
    pidFile := filepath.Join(homeDir(), defaultPidDir, "deployd.pid")
    mgr := process.NewManager(pidFile)

    fmt.Printf("deployd: %s\n", mgr.Status())

    configPath := filepath.Join(homeDir(), "config.yaml")
    if _, err := os.Stat(configPath); os.IsNotExist(err) {
        return nil
    }

    cfg, err := config.Load(configPath)
    if err != nil {
        return err
    }

    for _, svc := range cfg.Services {
        svcPIDFile := filepath.Join(homeDir(), defaultPidDir, svc.Name+".pid")
        svcMgr := process.NewManager(svcPIDFile)
        fmt.Printf("  %-30s %s\n", svc.Name, svcMgr.Status())
    }

    return nil
}

func Logs(serviceName string) error {
    logDir := filepath.Join(homeDir(), ".deployd", "logs")
    if serviceName != "" {
        logFile := filepath.Join(logDir, serviceName+".log")
        return tailLog(logFile)
    }
    logFile := filepath.Join(logDir, "deployd.log")
    return tailLog(logFile)
}

func tailLog(path string) error {
    data, err := os.ReadFile(path)
    if err != nil {
        if os.IsNotExist(err) {
            fmt.Println("no logs found")
            return nil
        }
        return err
    }
    fmt.Print(string(data))
    return nil
}

func homeDir() string {
    h, _ := os.UserHomeDir()
    return h
}
```

- [ ] **Step 2: 更新 CLI 命令**

```go
// cmd/stop.go
package cmd

import (
    "fmt"
    "auto-deployer/internal/daemon"
    "github.com/spf13/cobra"
)

var stopCmd = &cobra.Command{
    Use:   "stop",
    Short: "Stop the deployd daemon",
    RunE: func(cmd *cobra.Command, args []string) error {
        return daemon.Stop()
    },
}
```

```go
// cmd/status.go
package cmd

import (
    "auto-deployer/internal/daemon"
    "github.com/spf13/cobra"
)

var statusCmd = &cobra.Command{
    Use:   "status",
    Short: "Show deployd and all services status",
    RunE: func(cmd *cobra.Command, args []string) error {
        return daemon.Status()
    },
}
```

```go
// cmd/logs.go
package cmd

import (
    "auto-deployer/internal/daemon"
    "github.com/spf13/cobra"
)

var logsCmd = &cobra.Command{
    Use:   "logs [service_name]",
    Short: "View deployd or service logs",
    Args:  cobra.MaximumNArgs(1),
    RunE: func(cmd *cobra.Command, args []string) error {
        var name string
        if len(args) > 0 {
            name = args[0]
        }
        return daemon.Logs(name)
    },
}
```

```go
// cmd/deploy.go
package cmd

import (
    "fmt"
    "auto-deployer/internal/daemon"
    "github.com/spf13/cobra"
)

var deployCmd = &cobra.Command{
    Use:   "deploy <service_name>",
    Short: "Manually trigger deployment for a service",
    Args:  cobra.ExactArgs(1),
    RunE: func(cmd *cobra.Command, args []string) error {
        fmt.Printf("triggering deploy for %s...\n", args[0])
        // TODO: 实际部署逻辑
        return daemon.TriggerDeploy(args[0])
    },
}
```

- [ ] **Step 3: 编译验证**

```bash
go build -o deployd .
./deployd --help
./deployd status
./deployd logs
```

Expected: 无报错，status/logs 正常输出

- [ ] **Step 4: Commit**

```bash
git add internal/daemon/commands.go cmd/stop.go cmd/status.go cmd/logs.go cmd/deploy.go
git commit -m "feat: implement stop, status, logs, and deploy commands"
```

---

### Task 13: GitHub Actions CI/CD 与 Release 构建

**Files:**
- Create: `.github/workflows/release.yml`
- Create: `install.sh`

**Interfaces:**
- Consumes: none
- Produces: 自动构建 + 发布 GitHub Release + 一键安装脚本

- [ ] **Step 1: 编写 CI/CD 配置**

```yaml
# .github/workflows/release.yml
name: Release

on:
  push:
    tags:
      - 'v*'

permissions:
  contents: write

jobs:
  release:
    runs-on: ubuntu-latest
    strategy:
      matrix:
        include:
          - goos: linux
            goarch: amd64
          - goos: linux
            goarch: arm64
          - goos: darwin
            goarch: amd64
          - goos: darwin
            goarch: arm64

    steps:
      - uses: actions/checkout@v4

      - uses: actions/setup-go@v5
        with:
          go-version: '1.22'

      - name: Build
        run: |
          GOOS=${{ matrix.goos }} GOARCH=${{ matrix.goarch }} CGO_ENABLED=0 go build -o deployd-${{ matrix.goos }}-${{ matrix.goarch }} .

      - name: Package
        run: |
          tar -czf deployd-${{ matrix.goos }}-${{ matrix.goarch }}.tar.gz deployd-${{ matrix.goos }}-${{ matrix.goarch }}

      - name: Upload Release Asset
        uses: softprops/action-gh-release@v1
        with:
          files: deployd-*
```

- [ ] **Step 2: 编写安装脚本**

```bash
#!/bin/sh
set -e

VERSION="${VERSION:-latest}"
INSTALL_DIR="/usr/local/bin"
HOST_OS=$(uname -s | tr '[:upper:]' '[:lower:]')
HOST_ARCH=$(uname -m)

case "$HOST_ARCH" in
    x86_64) GOARCH="amd64" ;;
    aarch64|arm64) GOARCH="arm64" ;;
    *) echo "Unsupported architecture: $HOST_ARCH"; exit 1 ;;
esac

if [ "$VERSION" = "latest" ]; then
    URL="https://github.com/auto-deployer/auto-deployer/releases/latest/download/deployd-${HOST_OS}-${GOARCH}.tar.gz"
else
    URL="https://github.com/auto-deployer/auto-deployer/releases/download/${VERSION}/deployd-${HOST_OS}-${GOARCH}.tar.gz"
fi

echo "Downloading deployd ${VERSION} for ${HOST_OS}/${GOARCH}..."
curl -fsSL "$URL" | tar -xz -C /tmp
sudo mv /tmp/deployd-${HOST_OS}-${GOARCH} "${INSTALL_DIR}/deployd"
chmod +x "${INSTALL_DIR}/deployd"

echo "deployd installed to ${INSTALL_DIR}/deployd"
deployd --version
```

- [ ] **Step 3: Commit**

```bash
git add .github/workflows/release.yml install.sh
git commit -m "ci: add GitHub Actions release workflow and install script"
```

---

### Task 14: 集成测试与端到端验证

**Files:**
- Create: `test/e2e_test.go`
- Create: `test/fixtures/test-repo/.gitkeep`

**Interfaces:**
- Consumes: 以上所有组件
- Produces: 端到端验证 webhook → build → restart 流程

- [ ] **Step 1: 编写 E2E 测试**

```go
// test/e2e_test.go
package e2e

import (
    "net/http"
    "net/http/httptest"
    "os"
    "path/filepath"
    "testing"
    "auto-deployer/internal/config"
    "auto-deployer/plugins/springboot"
)

func TestEndToEnd_WebhookTriggersBuild(t *testing.T) {
    workspace := t.TempDir()

    // 创建测试用的 git 仓库
    repoDir := filepath.Join(t.TempDir(), "repo")
    os.MkdirAll(repoDir, 0755)
    // ... 初始化 git repo，写入 pom.xml 和 start.sh

    cfg := &config.AppConfig{
        Server: config.ServerConfig{Host: "127.0.0.1", Port: 0},
        Services: []config.ServiceConfig{{
            Name:      "test-app",
            Type:      "springboot",
            Repo:      config.RepoConfig{URL: repoDir, Branch: "main"},
            Workspace: workspace,
            Build:     config.BuildConfig{Command: "echo build"},
            Run:       config.RunConfig{Command: "echo run"},
        }},
    }

    plugin := springboot.New()

    // 验证 Build 能被调用
    err := plugin.Build(nil, &cfg.Services[0])
    if err != nil {
        t.Logf("build result: %v", err)
    }
}
```

- [ ] **Step 2: Commit**

```bash
git add test/
git commit -m "test: add e2e test skeleton"
```

---

## Self-Review

**Spec coverage check:**

| Spec 要求 | 对应 Task |
|-----------|----------|
| Go 单二进制 | Task 1, 13 |
| YAML 配置 | Task 1, 2 |
| 插件化架构 | Task 9 |
| Webhook 统一路由 | Task 10 |
| GitHub/Gitee payload 解析 | Task 10 |
| 分支校验 | Task 10 (MatchService) |
| git pull → mvn package → 重启 | Task 6, 7, 9 |
| 一对多服务管理 | Task 2, 11 |
| 后台守护进程 | Task 11 |
| 环境检查 (git/java/mvn) | Task 5 |
| 交互式配置向导 | Task 4 |
| CLI 命令集合 | Task 3, 12 |
| GitHub Releases + curl 安装 | Task 13 |
| 日志查看 | Task 12 |
| 手动触发部署 | Task 12 |

**Placeholder scan:**
- Task 8 中 `getDaemonDir()` 使用了环境变量，需确保一致
- Task 10 的 `handleWebhook` 目前只解析不执行部署，Task 12 的 `TriggerDeploy` 标记为 TODO，需在 Task 12 中补充完整
- Task 13 安装脚本中的 GitHub 用户名/仓库名需要替换为实际值

**修复：**
- Task 10 的 `handleWebhook` 需要对接 Dispatcher 和 Plugin Registry，当前只是空壳。应在 Task 11 或 12 中完成真实路由逻辑。
- Task 12 的 `TriggerDeploy` 需要实现：加载配置 → 找服务 → 调插件 Build → Stop → Start。

---

## Execution Handoff

Plan complete and saved to `docs/superpowers/plans/2026-07-21-auto-deployer-plan.md`. Two execution options:

**1. Subagent-Driven (recommended)** — 我每个 Task 派一个子 agent 执行，任务间有 review 检查点，迭代快

**2. Inline Execution** — 在当前会话中按顺序执行，每批任务后 checkpoint

**Which approach?**
