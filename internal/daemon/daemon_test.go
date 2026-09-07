package daemon

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "github.com/auto-deployer/auto-deployer/plugins" // 注册内置插件，registry.Get 可用
)

func TestStart_FailsWithoutConfig(t *testing.T) {
	dir := t.TempDir()
	os.Setenv("HOME", dir)
	defer os.Unsetenv("HOME")

	err := Start(filepath.Join(dir, "nonexistent.yaml"))
	if err == nil {
		t.Fatal("expected error when config file does not exist")
	}
}

func TestStart_FailsInvalidConfig(t *testing.T) {
	dir := t.TempDir()
	os.Setenv("HOME", dir)
	defer os.Unsetenv("HOME")

	badConfig := filepath.Join(dir, "bad.yaml")
	_ = os.WriteFile(badConfig, []byte("invalid: yaml: ["), 0644)

	err := Start(badConfig)
	if err == nil {
		t.Fatal("expected error for invalid yaml")
	}
}

// daemon.Status 对 static 服务不能直读 pid 文件（static 无进程，恒 stopped），
// 必须走 GetServiceStatusRich → 插件实时探测（health URL）。
func TestStatus_StaticServiceUsesHealthProbe(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	dir := t.TempDir()
	t.Setenv("HOME", dir)
	cfgYAML := fmt.Sprintf(`
services:
  - name: site
    type: static
    workspace: %s/ws
    deploy:
      artifact: dist/*
      dest: %s/html
      health: %s
      health_interval: 1s
`, dir, dir, srv.URL)
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(cfgYAML), 0644); err != nil {
		t.Fatal(err)
	}

	// 捕获 Status 的 stdout
	old := os.Stdout
	rp, wp, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = wp
	statusErr := Status("")
	wp.Close()
	os.Stdout = old
	out, _ := io.ReadAll(rp)

	if statusErr != nil {
		t.Fatalf("Status returned error: %v", statusErr)
	}
	var svcLine string
	for _, line := range strings.Split(string(out), "\n") {
		if strings.Contains(line, "site") {
			svcLine = line
			break
		}
	}
	if svcLine == "" {
		t.Fatalf("service line for %q not found in output:\n%s", "site", out)
	}
	if !strings.Contains(svcLine, "running") {
		t.Errorf("static service with healthy health URL should report running, got: %q", svcLine)
	}
	if strings.Contains(svcLine, "stopped") {
		t.Errorf("static service should not report stopped (pid-probe legacy), got: %q", svcLine)
	}
}
