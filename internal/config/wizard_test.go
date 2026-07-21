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

	input := "9527\n\nmy-app\nspringboot\nhttps://github.com/user/repo.git\ngithub-token-xxx\nmain\n/tmp/app\nmvn package -DskipTests\njava -jar target/my-app.jar\n"
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
	if !strings.Contains(content, "9527") {
		t.Error("config should contain port")
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

func TestRunWizard_CustomHost(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")

	input := "8080\n127.0.0.1\nmy-app\nspringboot\nhttps://github.com/user/repo.git\n\ntest-branch\n/workspace\nmvn clean package\njava -jar app.jar\n"
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
	if cfg.Server.Host != "127.0.0.1" {
		t.Errorf("expected host 127.0.0.1, got %s", cfg.Server.Host)
	}
	if cfg.Server.Port != 8080 {
		t.Errorf("expected port 8080, got %d", cfg.Server.Port)
	}
	if cfg.Services[0].Repo.Branch != "test-branch" {
		t.Errorf("expected branch test-branch, got %s", cfg.Services[0].Repo.Branch)
	}
}
