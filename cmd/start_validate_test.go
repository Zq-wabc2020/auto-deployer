package cmd

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// start 在 fork 守护进程前应先校验配置：校验错误（如缺 deploy.health）
// 必须直接报给用户，而不是等 fork 子进程死后报 10s 就绪超时。
func TestValidateConfigFile_MissingHealth(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	cfg := `
services:
  - name: "old-svc"
    type: "node"
    repo: { url: "/tmp/some-repo.git", branch: "main" }
    workspace: "/tmp/ws-old"
    build: { command: "true" }
    deploy: { run: "python3 -m http.server 18090" }
`
	if err := os.WriteFile(cfgPath, []byte(cfg), 0644); err != nil {
		t.Fatal(err)
	}

	err := validateConfigFile(cfgPath)
	if err == nil {
		t.Fatal("expected validation error for missing deploy.health")
	}
	// 详细校验错误（含 health 提示）打到 stderr，返回值为汇总错误。
	old := os.Stderr
	rp, wp, _ := os.Pipe()
	os.Stderr = wp
	_ = validateConfigFile(cfgPath)
	wp.Close()
	os.Stderr = old
	out, _ := io.ReadAll(rp)
	if !strings.Contains(string(out), "health") {
		t.Errorf("stderr should mention health, got: %s", out)
	}
}

// 合法配置（含 health）应通过。
func TestValidateConfigFile_OK(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	cfg := `
services:
  - name: "ok-svc"
    type: "node"
    repo: { url: "/tmp/some-repo.git", branch: "main" }
    workspace: "/tmp/ws-ok"
    build: { command: "true" }
    deploy:
      run: "python3 -m http.server 18091"
      health: "http://localhost:18091/"
`
	if err := os.WriteFile(cfgPath, []byte(cfg), 0644); err != nil {
		t.Fatal(err)
	}
	if err := validateConfigFile(cfgPath); err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
}
