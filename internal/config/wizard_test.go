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

	// 端口/主机默认；构建命令用默认；部署命令用户填写；SMTP 与收件人填全。
	input := "9527\n\nmy-app\nhttps://github.com/user/repo.git\nmain\n/tmp/app\n\nsystemctl restart my-app\nsmtp.qq.com\n465\nuser@qq.com\nauth-code\ntrue\nadmin@example.com\n"
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
	for _, want := range []string{"pipelines:", "type: git", "when: always", "my-app", "github.com/user/repo.git", "systemctl restart my-app", "mvn package -DskipTests"} {
		if !strings.Contains(content, want) {
			t.Errorf("config should contain %q\n---\n%s", want, content)
		}
	}
}

func TestRunWizard_DefaultPort(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")

	// 空输入使用默认值。
	input := "\n\nmy-app\nhttps://github.com/user/repo.git\ndefault-branch\n/tmp/app\n\nsystemctl restart my-app\n\n\n\n\ntrue\n"
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

	input := "8080\n127.0.0.1\nmy-app\nhttps://github.com/user/repo.git\ntest-branch\n/workspace\nmvn clean package\njava -jar app.jar\nsmtp.qq.com\n465\nuser@qq.com\ntoken\nfalse\n"
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
	// 指定分支应流入 git 节点的 branch 列表（webhook 匹配用字面量）。
	p := cfg.Pipelines[0]
	if len(p.TriggerBranches) != 1 || p.TriggerBranches[0] != "test-branch" {
		t.Errorf("expected branch test-branch in git node, got %v", p.TriggerBranches)
	}
}
