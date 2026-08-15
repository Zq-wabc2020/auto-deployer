package docker

import (
	"context"
	"io"
	"os/exec"
	"strings"
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
	if New().Type() != "docker" {
		t.Errorf("expected docker")
	}
}

func TestDeployConfigParsing(t *testing.T) {
	svc := mustService(t, `
deploy:
  image: "app:latest"
  container: "my-app"
  ports: ["8000:8000", "9090:9090"]
  env:
    FOO: "bar"
    AAA: "bbb"
  volumes: ["/data:/data"]
  args: ["--memory=512m"]
`)
	dc := New().deployConfig(svc)
	if dc.Image != "app:latest" {
		t.Errorf("Image = %q", dc.Image)
	}
	if dc.Container != "my-app" {
		t.Errorf("Container = %q", dc.Container)
	}
	if len(dc.Ports) != 2 {
		t.Errorf("Ports = %v", dc.Ports)
	}
	if dc.Env["FOO"] != "bar" || dc.Env["AAA"] != "bbb" {
		t.Errorf("Env = %v", dc.Env)
	}
}

func TestContainerNameDefaultsToServiceName(t *testing.T) {
	svc := &config.ServiceConfig{Name: "svc1"}
	if n := New().containerName(svc); n != "svc1" {
		t.Errorf("containerName = %q, want svc1", n)
	}
	svc2 := mustService(t, "deploy:\n  container: \"box\"\n")
	if n := New().containerName(svc2); n != "box" {
		t.Errorf("containerName = %q, want box", n)
	}
}

func TestRunArgs(t *testing.T) {
	dc := dockerDeployConfig{
		Ports:   []string{"8000:8000"},
		Env:     map[string]string{"B": "2", "A": "1"},
		Volumes: []string{"/data:/data"},
		Args:    []string{"--memory=512m"},
	}
	got := strings.Join(runArgs(dc), " ")
	want := "-p 8000:8000 -e A=1 -e B=2 -v /data:/data --memory=512m"
	if got != want {
		t.Errorf("runArgs:\n got  %s\n want %s", got, want)
	}
}

func TestBuild_EmptyCommand(t *testing.T) {
	p := New()
	svc := &config.ServiceConfig{Workspace: t.TempDir()}
	if err := p.Build(context.Background(), svc); err == nil {
		t.Fatal("expected error for empty build command")
	}
}

func TestStart_RequiresImage(t *testing.T) {
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker not available")
	}
	p := New()
	p.SetOutput(io.Discard)
	svc := &config.ServiceConfig{Name: "t"}
	if err := p.Start(context.Background(), svc); err == nil {
		t.Fatal("expected error when deploy.image is missing")
	}
}
