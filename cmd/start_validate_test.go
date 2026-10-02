package cmd

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// start 在 fork 守护进程前应先校验配置：校验错误（如缺 workspace）
// 必须直接报给用户，而不是等 fork 子进程死后报 10s 就绪超时。
func TestValidateConfigFile_MissingWorkspace(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	cfg := `
server:
  port: 9527

pipelines:
  - name: "old-svc"
    stages:
      - name: 拉取代码
        type: git
        params:
          url: "/tmp/some-repo.git"
          branch: ["main"]
`
	if err := os.WriteFile(cfgPath, []byte(cfg), 0644); err != nil {
		t.Fatal(err)
	}

	err := validateConfigFile(cfgPath)
	if err == nil {
		t.Fatal("expected validation error for missing workspace")
	}
	// 详细校验错误（含 workspace 提示）打到 stderr，返回值为汇总错误。
	old := os.Stderr
	rp, wp, _ := os.Pipe()
	os.Stderr = wp
	_ = validateConfigFile(cfgPath)
	wp.Close()
	os.Stderr = old
	out, _ := io.ReadAll(rp)
	if !strings.Contains(string(out), "workspace") {
		t.Errorf("stderr should mention workspace, got: %s", out)
	}
}

// 合法配置（含 workspace/stages）应通过。
func TestValidateConfigFile_OK(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	cfg := `
server:
  port: 9527

pipelines:
  - name: "ok-svc"
    workspace: "/tmp/ws-ok"
    stages:
      - name: 构建
        type: shell
        params:
          sh: "true"
`
	if err := os.WriteFile(cfgPath, []byte(cfg), 0644); err != nil {
		t.Fatal(err)
	}
	if err := validateConfigFile(cfgPath); err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
}
