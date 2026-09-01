package node

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/auto-deployer/auto-deployer/internal/config"
	"gopkg.in/yaml.v3"
)

func mustService(t *testing.T, svcYAML string) *config.ServiceConfig {
	t.Helper()
	svc := &config.ServiceConfig{}
	if err := yaml.Unmarshal([]byte(svcYAML), svc); err != nil {
		t.Fatalf("failed to unmarshal service: %v", err)
	}
	return svc
}

func TestType(t *testing.T) {
	if New().Type() != "node" {
		t.Errorf("expected node")
	}
}

func TestDeployConfigParsing(t *testing.T) {
	svc := mustService(t, `
deploy:
  run: "node server.js"
  env:
    NODE_ENV: "production"
    PORT: "3000"
`)
	dc := New().deployConfig(svc)
	if dc.Run != "node server.js" {
		t.Errorf("Run = %q", dc.Run)
	}
	if dc.Env["NODE_ENV"] != "production" || dc.Env["PORT"] != "3000" {
		t.Errorf("Env = %v", dc.Env)
	}
}

func TestStartRequiresRun(t *testing.T) {
	p := New()
	svc := &config.ServiceConfig{Name: "no-run-test"}
	if err := p.Start(context.Background(), svc); err == nil {
		t.Fatal("expected error when deploy.run missing")
	}
}

func TestLifecycleStartStop(t *testing.T) {
	name := "node-lifecycle-test"
	pidFile := pidFileFor(name)
	_ = os.Remove(pidFile)
	t.Cleanup(func() { _ = os.Remove(pidFile) })

	p := New()
	ws := t.TempDir()
	svc := mustService(t, "deploy:\n  run: \"sleep 60\"\n")
	svc.Name = name
	svc.Workspace = ws

	// Start -> running with a live PID.
	if err := p.Start(context.Background(), svc); err != nil {
		t.Fatal(err)
	}
	st, _ := p.Status(context.Background(), svc)
	if st != "running" {
		t.Fatalf("expected running after Start, got %s", st)
	}
	pid, _ := os.ReadFile(pidFile)

	// Start again -> already running.
	if err := p.Start(context.Background(), svc); err == nil {
		t.Fatal("expected already-running error")
	}

	// Stop -> process gone.
	if err := p.Stop(context.Background(), svc); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)
	st, _ = p.Status(context.Background(), svc)
	if st != "stopped" {
		t.Fatalf("expected stopped after Stop, got %s (pid %s)", st, pid)
	}
}

// Stage with no artifact is a no-op: node is source-is-artifact by default
// (same as python), so the bundle stays in the workspace and runs in place.
func TestStage_NoArtifact_IsNoOp(t *testing.T) {
	p := New()
	ws := t.TempDir()
	_ = os.MkdirAll(filepath.Join(ws, ".output"), 0755)
	_ = os.WriteFile(filepath.Join(ws, ".output", "server.js"), []byte("x"), 0644)
	svc := mustService(t, `deploy:
  run: "node .output/server.js"
`)
	svc.Workspace = ws
	if err := p.Stage(context.Background(), svc); err != nil {
		t.Fatalf("Stage with no artifact should be no-op, got: %v", err)
	}
	if _, err := os.Stat(filepath.Join(ws, ".output", "server.js")); err != nil {
		t.Errorf("source bundle should remain in workspace: %v", err)
	}
}

// Stage copies artifact contents to dest, and Start then runs from dest
// (decoupling the running server from the workspace's next build).
func TestStage_ArtifactCopiedToDest_AndStartRunsFromDest(t *testing.T) {
	name := "node-artifact-test"
	pidFile := pidFileFor(name)
	_ = os.Remove(pidFile)
	t.Cleanup(func() { _ = os.Remove(pidFile) })

	p := New()
	ws := t.TempDir()
	dest := t.TempDir()
	_ = os.MkdirAll(filepath.Join(ws, ".output"), 0755)
	_ = os.WriteFile(filepath.Join(ws, ".output", "server.mjs"), []byte("x"), 0644)
	_ = os.MkdirAll(filepath.Join(ws, ".output", "sub"), 0755)
	_ = os.WriteFile(filepath.Join(ws, ".output", "sub", "dep.js"), []byte("y"), 0644)

	svc := mustService(t, "deploy:\n  artifact: \".output\"\n  dest: \""+dest+"\"\n  run: \"sleep 60\"\n")
	svc.Name = name
	svc.Workspace = ws

	if err := p.Stage(context.Background(), svc); err != nil {
		t.Fatalf("Stage: %v", err)
	}
	for _, rel := range []string{"server.mjs", filepath.Join("sub", "dep.js")} {
		if _, err := os.Stat(filepath.Join(dest, rel)); err != nil {
			t.Errorf("dest/%s should exist after Stage: %v", rel, err)
		}
	}

	if err := p.Start(context.Background(), svc); err != nil {
		t.Fatal(err)
	}
	if st, _ := p.Status(context.Background(), svc); st != "running" {
		t.Fatalf("expected running after Start, got %s", st)
	}
	// On Linux, confirm the process actually runs from dest (not workspace).
	if data, err := os.ReadFile(pidFile); err == nil {
		pid := strings.TrimSpace(string(data))
		if cwd, err := os.Readlink("/proc/" + pid + "/cwd"); err == nil && cwd != dest {
			t.Errorf("process cwd = %s, want %s", cwd, dest)
		}
	}
	if err := p.Stop(context.Background(), svc); err != nil {
		t.Fatal(err)
	}
}
