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

services:
  - name: "test-service"
    type: "springboot"
    repo:
      url: "https://github.com/user/repo.git"
      branch: "main"
    workspace: "/opt/deployd/apps/test-service"
    build:
      command: "mvn package -DskipTests"
    run:
      command: "java -jar test-service.jar"
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
	if len(cfg.Services) != 1 {
		t.Fatalf("expected 1 service, got %d", len(cfg.Services))
	}

	svc := cfg.Services[0]
	if svc.Name != "test-service" {
		t.Errorf("expected service name 'test-service', got '%s'", svc.Name)
	}
	if svc.Type != "springboot" {
		t.Errorf("expected service type 'springboot', got '%s'", svc.Type)
	}
	if svc.Repo.URL != "https://github.com/user/repo.git" {
		t.Errorf("expected repo URL 'https://github.com/user/repo.git', got '%s'", svc.Repo.URL)
	}
	if svc.Repo.Branch != "main" {
		t.Errorf("expected repo branch 'main', got '%s'", svc.Repo.Branch)
	}
	if svc.Workspace != "/opt/deployd/apps/test-service" {
		t.Errorf("expected workspace '/opt/deployd/apps/test-service', got '%s'", svc.Workspace)
	}
	if svc.Build.Command.String() != "mvn package -DskipTests" {
		t.Errorf("expected build command 'mvn package -DskipTests', got '%s'", svc.Build.Command.String())
	}
}

func TestParseMultiLineCommand(t *testing.T) {
	// build.command accepts a multi-line scalar; Command preserves it verbatim.
	yamlContent := []byte(`
server:
  host: "localhost"
  port: 8080

services:
  - name: "multiline-test"
    type: "jvm"
    workspace: "/tmp/test"
    build:
      command: |
        echo "step one"
        echo "step two"
`)

	var cfg AppConfig
	if err := yaml.Unmarshal(yamlContent, &cfg); err != nil {
		t.Fatalf("failed to unmarshal config: %v", err)
	}

	if len(cfg.Services) != 1 {
		t.Fatalf("expected 1 service, got %d", len(cfg.Services))
	}

	buildCmd := cfg.Services[0].Build.Command.String()
	newlineCount := strings.Count(buildCmd, "\n")
	if newlineCount < 1 {
		t.Errorf("expected multi-line command with newlines, got %d in:\n%s", newlineCount, buildCmd)
	}
}

func TestDeployNodeDecoding(t *testing.T) {
	yamlContent := []byte(`
services:
  - name: "hello1"
    type: "jvm"
    deploy:
      run: "java -jar app.jar"
      artifact: "target/*.jar"
      dest: "/opt/dest"
`)
	var cfg AppConfig
	if err := yaml.Unmarshal(yamlContent, &cfg); err != nil {
		t.Fatal(err)
	}
	svc := cfg.Services[0]
	if svc.Deploy.Kind == 0 {
		t.Fatal("Deploy node is zero -- deploy: block was not captured")
	}
	var dc struct {
		Run      string `yaml:"run"`
		Artifact string `yaml:"artifact"`
		Dest     string `yaml:"dest"`
	}
	if err := svc.Deploy.Decode(&dc); err != nil {
		t.Fatalf("Decode failed: %v", err)
	}
	if dc.Run != "java -jar app.jar" {
		t.Errorf("Run = %q, want java -jar app.jar", dc.Run)
	}
	if dc.Artifact != "target/*.jar" {
		t.Errorf("Artifact = %q, want target/*.jar", dc.Artifact)
	}
	if dc.Dest != "/opt/dest" {
		t.Errorf("Dest = %q, want /opt/dest", dc.Dest)
	}
}

func TestDeployNodeAbsent(t *testing.T) {
	// No deploy: block -> zero node, no panic on presence check.
	yamlContent := []byte(`
services:
  - name: "hello1"
    type: "jvm"
    build:
      command: "mvn package"
`)
	var cfg AppConfig
	if err := yaml.Unmarshal(yamlContent, &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Services[0].Deploy.Kind != 0 {
		t.Errorf("expected zero Deploy node, got Kind=%d", cfg.Services[0].Deploy.Kind)
	}
}

func TestLoadExampleConfig(t *testing.T) {
	// The repo's config.yaml.example must parse and validate against the new
	// two-tier schema (catches drift between the template and the parser).
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
	if len(cfg.Services) == 0 {
		t.Fatal("expected at least one service in config.yaml.example")
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

services: []
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
services:
  - name: test
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

services: []
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

services: []
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

func TestServiceTimeoutDuration(t *testing.T) {
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
		s := ServiceConfig{Timeout: c.timeout}
		got, err := s.TimeoutDuration()
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
