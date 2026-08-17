package static

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

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
	if New().Type() != "static" {
		t.Errorf("expected static")
	}
}

func TestStageCopiesArtifact(t *testing.T) {
	ws := t.TempDir()
	dest := t.TempDir()
	_ = os.MkdirAll(filepath.Join(ws, "dist"), 0755)
	_ = os.WriteFile(filepath.Join(ws, "dist", "index.html"), []byte("<h1>hi</h1>"), 0644)

	svc := mustService(t, "deploy:\n  artifact: \"dist/*\"\n  dest: "+dest+"\n")
	svc.Workspace = ws
	p := New()
	if err := p.Stage(context.Background(), svc); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dest, "index.html"))
	if err != nil {
		t.Fatalf("index.html not copied: %v", err)
	}
	if string(data) != "<h1>hi</h1>" {
		t.Errorf("unexpected content %q", data)
	}
}

func TestStageRequiresArtifactAndDest(t *testing.T) {
	p := New()
	svc := &config.ServiceConfig{Name: "web"}
	if err := p.Stage(context.Background(), svc); err == nil {
		t.Fatal("expected error when artifact/dest missing")
	}
}

func TestStageNoArtifactFound(t *testing.T) {
	p := New()
	svc := mustService(t, "deploy:\n  artifact: \"dist/*\"\n  dest: \"/tmp/x\"\n")
	svc.Workspace = t.TempDir()
	if err := p.Stage(context.Background(), svc); err == nil {
		t.Fatal("expected error when nothing matches the artifact glob")
	}
}

func TestStatus_HealthURL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()

	p := New()
	svc := mustService(t, "deploy:\n  health: "+srv.URL+"\n")
	st, err := p.Status(context.Background(), svc)
	if err != nil {
		t.Fatal(err)
	}
	if st != "running" {
		t.Errorf("expected running, got %s", st)
	}
}

func TestStatus_HealthDown(t *testing.T) {
	p := New()
	svc := mustService(t, "deploy:\n  health: \"http://127.0.0.1:1/health\"\n")
	st, _ := p.Status(context.Background(), svc)
	if st != "stopped" {
		t.Errorf("expected stopped, got %s", st)
	}
}

func TestStatus_NoHealthUnknown(t *testing.T) {
	p := New()
	p.SetOutput(io.Discard)
	svc := &config.ServiceConfig{Name: "web"}
	st, _ := p.Status(context.Background(), svc)
	if st != "unknown" {
		t.Errorf("expected unknown, got %s", st)
	}
}

// Static has no per-service process: the plugin must NOT satisfy the
// orchestrator's optional Start/Stop capabilities.
func TestNotStartableOrStoppable(t *testing.T) {
	var d interface{} = New()
	if _, ok := d.(interface {
		Start(context.Context, *config.ServiceConfig) error
	}); ok {
		t.Error("static plugin must not implement Start")
	}
	if _, ok := d.(interface {
		Stop(context.Context, *config.ServiceConfig) error
	}); ok {
		t.Error("static plugin must not implement Stop")
	}
}
