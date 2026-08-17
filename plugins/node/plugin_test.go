package node

import (
	"context"
	"os"
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
