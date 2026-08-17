package python

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
	if New().Type() != "python" {
		t.Errorf("expected python")
	}
}

// Config shape from design doc §13.1 (ai_qa_assistant mapping).
func TestDeployConfigParsing(t *testing.T) {
	svc := mustService(t, `
deploy:
  venv: ".venv"
  migrate: ".venv/bin/alembic upgrade head"
  run: ".venv/bin/uvicorn app.main:create_app --factory --host 0.0.0.0 --port 8000"
  env:
    DATABASE_URL: "postgres://localhost/app"
    ARQ_WORKER_MODE: "inline"
`)
	dc := New().deployConfig(svc)
	if dc.Venv != ".venv" {
		t.Errorf("Venv = %q", dc.Venv)
	}
	if dc.Migrate != ".venv/bin/alembic upgrade head" {
		t.Errorf("Migrate = %q", dc.Migrate)
	}
	if !strings.HasPrefix(dc.Run, ".venv/bin/uvicorn") {
		t.Errorf("Run = %q", dc.Run)
	}
	if dc.Env["ARQ_WORKER_MODE"] != "inline" {
		t.Errorf("Env = %v", dc.Env)
	}
}

func TestStartRequiresRun(t *testing.T) {
	p := New()
	svc := &config.ServiceConfig{Name: "no-run-py"}
	if err := p.Start(context.Background(), svc); err == nil {
		t.Fatal("expected error when deploy.run missing")
	}
}

func TestStageNoMigrateIsNoop(t *testing.T) {
	p := New()
	svc := mustService(t, "deploy:\n  run: \"uvicorn main:app\"\n")
	if err := p.Stage(context.Background(), svc); err != nil {
		t.Fatalf("expected no-op without migrate, got %v", err)
	}
}

func TestStageRunsMigrate(t *testing.T) {
	ws := t.TempDir()
	marker := filepath.Join(ws, "migrated")
	p := New()
	svc := mustService(t, "deploy:\n  migrate: \"touch "+marker+"\"\n")
	svc.Workspace = ws
	if err := p.Stage(context.Background(), svc); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Error("migrate command did not run in the workspace")
	}
}

func TestStageMigrateFailure(t *testing.T) {
	p := New()
	svc := mustService(t, "deploy:\n  migrate: \"exit 7\"\n")
	svc.Workspace = t.TempDir()
	if err := p.Stage(context.Background(), svc); err == nil {
		t.Fatal("expected migrate failure to fail Stage")
	}
}

func TestLifecycleStartStop(t *testing.T) {
	name := "py-lifecycle-test"
	pidFile := pidFileFor(name)
	_ = os.Remove(pidFile)
	t.Cleanup(func() { _ = os.Remove(pidFile) })

	p := New()
	ws := t.TempDir()
	svc := mustService(t, "deploy:\n  run: \"sleep 60\"\n")
	svc.Name = name
	svc.Workspace = ws

	if err := p.Start(context.Background(), svc); err != nil {
		t.Fatal(err)
	}
	if st, _ := p.Status(context.Background(), svc); st != "running" {
		t.Fatalf("expected running, got %s", st)
	}
	if err := p.Stop(context.Background(), svc); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)
	if st, _ := p.Status(context.Background(), svc); st != "stopped" {
		t.Fatalf("expected stopped, got %s", st)
	}
}
