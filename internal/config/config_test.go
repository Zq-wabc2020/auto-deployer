package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

func TestParseConfig(t *testing.T) {
	yamlContent := []byte(`
server:
  host: "0.0.0.0"
  port: 9527

webhook:
  secret: "my-secret-token"

pipelines:
  - name: "test-pipeline"
    workspace: "/opt/deployd/apps/test-pipeline"
    timeout: "45m"
    stages:
      - name: 拉取代码
        type: git
        params:
          url: "https://github.com/user/repo.git"
          branch: ["main"]
      - name: 构建
        type: shell
        params:
          sh: "mvn package -DskipTests"
`)

	var cfg AppConfig
	if err := yaml.Unmarshal(yamlContent, &cfg); err != nil {
		t.Fatalf("failed to unmarshal config: %v", err)
	}

	if cfg.Server.Host != "0.0.0.0" {
		t.Errorf("expected server host '0.0.0.0', got '%s'", cfg.Server.Host)
	}
	if cfg.Server.Port != 9527 {
		t.Errorf("expected server port 9527, got %d", cfg.Server.Port)
	}
	if cfg.Webhook.Secret != "my-secret-token" {
		t.Errorf("expected webhook secret 'my-secret-token', got '%s'", cfg.Webhook.Secret)
	}
	if len(cfg.Pipelines) != 1 {
		t.Fatalf("expected 1 pipeline, got %d", len(cfg.Pipelines))
	}

	p := cfg.Pipelines[0]
	if p.Name != "test-pipeline" {
		t.Errorf("expected pipeline name 'test-pipeline', got '%s'", p.Name)
	}
	if p.Workspace != "/opt/deployd/apps/test-pipeline" {
		t.Errorf("expected workspace '/opt/deployd/apps/test-pipeline', got '%s'", p.Workspace)
	}
	if p.Stages[0].Type != "git" {
		t.Errorf("expected first stage type 'git', got '%s'", p.Stages[0].Type)
	}
	if p.Stages[0].Params["url"] != "https://github.com/user/repo.git" {
		t.Errorf("expected git url, got '%v'", p.Stages[0].Params["url"])
	}
}

func TestParseMultiLineCommand(t *testing.T) {
	// shell 节点的 sh 接受多行标量，Params 原样保留（含换行）。
	cfg := loadStr(t, `
server:
  host: "localhost"
  port: 8080

pipelines:
  - name: "multiline-test"
    workspace: "/tmp/test"
    stages:
      - name: 构建
        type: shell
        params:
          sh: |
            echo "step one"
            echo "step two"
`)
	if len(cfg.Pipelines) != 1 {
		t.Fatalf("expected 1 pipeline, got %d", len(cfg.Pipelines))
	}

	buildCmd, _ := cfg.Pipelines[0].Stages[0].Params["sh"].(string)
	newlineCount := strings.Count(buildCmd, "\n")
	if newlineCount < 1 {
		t.Errorf("expected multi-line command with newlines, got %d in:\n%s", newlineCount, buildCmd)
	}
}

func TestLoadExampleConfig(t *testing.T) {
	// The repo's config.yaml.example must parse and validate against the
	// pipeline schema (catches drift between the template and the parser).
	path := filepath.Join("..", "..", "config.yaml.example")
	if _, err := os.Stat(path); err != nil {
		t.Skip("config.yaml.example not found at", path)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("failed to load config.yaml.example: %v", err)
	}
	if errs := Validate(cfg); len(errs) != 0 {
		t.Fatalf("config.yaml.example failed validation: %v", errs)
	}
	if len(cfg.Pipelines) == 0 {
		t.Fatal("expected at least one pipeline in config.yaml.example")
	}
}

func TestLoadFromFile(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")

	content := `
server:
  host: "0.0.0.0"
  port: 9527

webhook:
  secret: ""

pipelines: []
`
	if err := os.WriteFile(configPath, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write temp config file: %v", err)
	}

	cfg, err := Load(configPath)
	if err != nil {
		t.Fatalf("failed to load config: %v", err)
	}

	if cfg.Server.Port != 9527 {
		t.Errorf("expected port 9527, got %d", cfg.Server.Port)
	}
}

func TestLoadFileNotFound(t *testing.T) {
	_, err := Load("/nonexistent/path/config.yaml")
	if err == nil {
		t.Fatal("expected error when loading nonexistent file, got nil")
	}
}

func TestLoadInvalidYAML(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "invalid.yaml")

	content := `
server:
  host: "0.0.0.0"
  port: not_a_number
pipelines:
  - name: test
    stages:
      - name: x
        type: [invalid yaml
`
	if err := os.WriteFile(configPath, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write temp config file: %v", err)
	}

	_, err := Load(configPath)
	if err == nil {
		t.Fatal("expected error when loading invalid YAML, got nil")
	}
}

func TestParseSMTPAndNotificationConfig(t *testing.T) {
	yamlContent := []byte(`
server:
  host: "0.0.0.0"
  port: 9527

smtp:
  host: "smtp.example.com"
  port: 587
  username: "deploy@example.com"
  token: "smtp-token-123"
  tls: true

notifications:
  to:
    - "team@example.com"
    - "ops@example.com"

pipelines: []
`)

	var cfg AppConfig
	if err := yaml.Unmarshal(yamlContent, &cfg); err != nil {
		t.Fatalf("failed to unmarshal config: %v", err)
	}

	if cfg.SMTP.Host != "smtp.example.com" {
		t.Errorf("expected SMTP host 'smtp.example.com', got '%s'", cfg.SMTP.Host)
	}
	if cfg.SMTP.Port != 587 {
		t.Errorf("expected SMTP port 587, got %d", cfg.SMTP.Port)
	}
	if cfg.SMTP.Username != "deploy@example.com" {
		t.Errorf("expected SMTP username 'deploy@example.com', got '%s'", cfg.SMTP.Username)
	}
	if cfg.SMTP.Token != "smtp-token-123" {
		t.Errorf("expected SMTP token 'smtp-token-123', got '%s'", cfg.SMTP.Token)
	}
	if !cfg.SMTP.TLS {
		t.Error("expected SMTP TLS to be true")
	}
	if len(cfg.Notifications.To) != 2 {
		t.Fatalf("expected 2 notification recipients, got %d", len(cfg.Notifications.To))
	}
	if cfg.Notifications.To[0] != "team@example.com" {
		t.Errorf("expected first recipient 'team@example.com', got '%s'", cfg.Notifications.To[0])
	}
	if cfg.Notifications.To[1] != "ops@example.com" {
		t.Errorf("expected second recipient 'ops@example.com', got '%s'", cfg.Notifications.To[1])
	}
}

func TestParseResendConfig(t *testing.T) {
	yamlContent := []byte(`
server:
  host: "0.0.0.0"
  port: 9527

resend:
  api_key: "re_test-key-123"
  from: "deployd <onboarding@example.com>"

notifications:
  to:
    - "admin@example.com"

pipelines: []
`)

	var cfg AppConfig
	if err := yaml.Unmarshal(yamlContent, &cfg); err != nil {
		t.Fatalf("failed to unmarshal config: %v", err)
	}

	if cfg.Resend.APIKey != "re_test-key-123" {
		t.Errorf("expected resend api_key 're_test-key-123', got '%s'", cfg.Resend.APIKey)
	}
	if cfg.Resend.From != "deployd <onboarding@example.com>" {
		t.Errorf("expected resend from 'deployd <onboarding@example.com>', got '%s'", cfg.Resend.From)
	}
}

func TestDefaultConfig_CurrentDir(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("server:\n  port: 9527\n"), 0644)
	oldWd, _ := os.Getwd()
	_ = os.Chdir(dir)
	defer func() { _ = os.Chdir(oldWd) }()
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
	_ = os.MkdirAll(filepath.Join(dir, ".deployd"), 0755)
	_ = os.WriteFile(filepath.Join(dir, ".deployd", "config.yaml"), []byte("server:\n  port: 9527\n"), 0644)
	oldHome, _ := os.UserHomeDir()
	os.Setenv("HOME", dir)
	defer os.Setenv("HOME", oldHome)
	oldWd, _ := os.Getwd()
	_ = os.Chdir("/tmp")
	defer func() { _ = os.Chdir(oldWd) }()
	path := DefaultConfig()
	expected := filepath.Join(dir, ".deployd", "config.yaml")
	if path != expected {
		t.Errorf("expected %s, got %s", expected, path)
	}
}

func TestDefaultConfig_NotFound(t *testing.T) {
	dir := t.TempDir()
	oldHome, _ := os.UserHomeDir()
	os.Setenv("HOME", dir)
	defer os.Setenv("HOME", oldHome)
	oldWd, _ := os.Getwd()
	_ = os.Chdir(dir)
	defer func() { _ = os.Chdir(oldWd) }()
	path := DefaultConfig()
	if path != "" {
		t.Errorf("expected empty string when no config found, got %s", path)
	}
}

func TestDefaultConfig_RecordedPathFallback(t *testing.T) {
	home := t.TempDir()
	oldHome, _ := os.UserHomeDir()
	os.Setenv("HOME", home)
	defer os.Setenv("HOME", oldHome)
	oldWd, _ := os.Getwd()
	_ = os.Chdir("/tmp")
	defer func() { _ = os.Chdir(oldWd) }()

	// No config in cwd or ~/.deployd; the daemon-recorded path is used.
	cfgDir := t.TempDir()
	recorded := filepath.Join(cfgDir, "prod.yaml")
	_ = os.WriteFile(recorded, []byte("server:\n  port: 9527\n"), 0644)
	RecordConfigPath(recorded)

	if got := DefaultConfig(); got != recorded {
		t.Errorf("expected recorded path %s, got %s", recorded, got)
	}
}

func TestDefaultConfig_StaleRecordedPathIgnored(t *testing.T) {
	home := t.TempDir()
	oldHome, _ := os.UserHomeDir()
	os.Setenv("HOME", home)
	defer os.Setenv("HOME", oldHome)
	oldWd, _ := os.Getwd()
	_ = os.Chdir("/tmp")
	defer func() { _ = os.Chdir(oldWd) }()

	_ = os.MkdirAll(filepath.Join(home, ".deployd"), 0755)
	RecordConfigPath("/nonexistent/config.yaml")

	if got := DefaultConfig(); got != "" {
		t.Errorf("expected empty string for stale recorded path, got %s", got)
	}
}

// loadStr 写入临时文件后用 Load 加载，便于用完整 yaml 字符串驱动 Load 路径
// （含 warnUnknownFields/ExtractTrigger 等加载期逻辑）。
func loadStr(t *testing.T, yamlStr string) *AppConfig {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(yamlStr), 0644); err != nil {
		t.Fatalf("write temp config: %v", err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load temp config: %v", err)
	}
	return cfg
}

func TestPipelineTimeoutDuration(t *testing.T) {
	cases := []struct {
		timeout string
		want    time.Duration
		wantErr bool
	}{
		{"", 0, false},
		{"45m", 45 * time.Minute, false},
		{"90s", 90 * time.Second, false},
		{"1h30m", 90 * time.Minute, false},
		{"fast", 0, true},
	}
	for _, c := range cases {
		p := PipelineConfig{Timeout: c.timeout}
		got, err := p.TimeoutDuration()
		if c.wantErr {
			if err == nil {
				t.Errorf("timeout %q: expected error", c.timeout)
			}
			continue
		}
		if err != nil {
			t.Errorf("timeout %q: %v", c.timeout, err)
		}
		if got != c.want {
			t.Errorf("timeout %q: expected %s, got %s", c.timeout, c.want, got)
		}
	}
}
