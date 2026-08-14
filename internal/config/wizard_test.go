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

	// jvm service; build & run use defaults (empty input).
	input := "9527\n\nmy-app\njvm\nhttps://github.com/user/repo.git\nmain\n/tmp/app\n\n\nsmtp.qq.com\n465\nuser@qq.com\nauth-code\ntrue\nadmin@example.com\n"
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
	for _, want := range []string{"my-app", "jvm", "github.com/user/repo.git", "9527", "deploy:", "java -jar"} {
		if !strings.Contains(content, want) {
			t.Errorf("config should contain %q\n---\n%s", want, content)
		}
	}
}

func TestRunWizard_DefaultPort(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")

	// Empty inputs use defaults.
	input := "\n\nmy-app\njvm\nhttps://github.com/user/repo.git\ndefault-branch\n/tmp/app\n\n\n\n\n\n\ntrue\n"
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

	input := "8080\n127.0.0.1\nmy-app\njvm\nhttps://github.com/user/repo.git\ntest-branch\n/workspace\nmvn clean package\njava -jar app.jar\n\n465\nuser@qq.com\ntoken\nfalse\n"
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
