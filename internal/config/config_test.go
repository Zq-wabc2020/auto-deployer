package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

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
      token: "ghp_token123"
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
	if svc.Repo.Token != "ghp_token123" {
		t.Errorf("expected repo token 'ghp_token123', got '%s'", svc.Repo.Token)
	}
	if svc.Repo.Branch != "main" {
		t.Errorf("expected repo branch 'main', got '%s'", svc.Repo.Branch)
	}
	if svc.Workspace != "/opt/deployd/apps/test-service" {
		t.Errorf("expected workspace '/opt/deployd/apps/test-service', got '%s'", svc.Workspace)
	}
	if svc.Build.Command != "mvn package -DskipTests" {
		t.Errorf("expected build command 'mvn package -DskipTests', got '%s'", svc.Build.Command)
	}
	if svc.Run.Command != "java -jar test-service.jar" {
		t.Errorf("expected run command 'java -jar test-service.jar', got '%s'", svc.Run.Command)
	}
}

func TestParseMultiLineCommand(t *testing.T) {
	yamlContent := []byte(`
server:
  host: "localhost"
  port: 8080

services:
  - name: "multiline-test"
    type: "custom"
    workspace: "/tmp/test"
    build:
      command: "echo step1 && echo step2"
    run:
      command: |
        #!/bin/bash
        echo "Starting service..."
        java -jar app.jar
        echo "Service started"
`)

	var cfg AppConfig
	if err := yaml.Unmarshal(yamlContent, &cfg); err != nil {
		t.Fatalf("failed to unmarshal config: %v", err)
	}

	if len(cfg.Services) != 1 {
		t.Fatalf("expected 1 service, got %d", len(cfg.Services))
	}

	runCmd := cfg.Services[0].Run.Command
	newlineCount := strings.Count(runCmd, "\n")
	if newlineCount < 2 {
		t.Errorf("expected multi-line command with at least 2 newlines, got %d in:\n%s", newlineCount, runCmd)
	}

	if !strings.HasPrefix(runCmd, "#!/bin/bas") {
		t.Errorf("expected command to start with shebang, got: %q", runCmd)
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
