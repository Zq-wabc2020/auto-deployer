package springboot

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/auto-deployer/auto-deployer/internal/config"
)

func TestType(t *testing.T) {
	p := New()
	if p.Type() != "springboot" {
		t.Errorf("expected springboot, got %s", p.Type())
	}
}

func TestBuild_NoCommand(t *testing.T) {
	p := New()
	svc := &config.ServiceConfig{
		Workspace: t.TempDir(),
		Build:     config.BuildConfig{Command: ""},
	}
	err := p.Build(context.Background(), svc)
	if err == nil {
		t.Fatal("expected error for empty build command")
	}
}

func TestBuild_WithScript(t *testing.T) {
	p := New()
	workspace := t.TempDir()

	// Initialize a git repo in workspace with an initial commit and origin remote
	_ = exec.Command("git", "-C", workspace, "init", "-b", "main").Run()
	_, _ = exec.Command("git", "-C", workspace, "config", "user.email", "test@test.com").CombinedOutput()
	_, _ = exec.Command("git", "-C", workspace, "config", "user.name", "Test").CombinedOutput()
	_ = os.WriteFile(filepath.Join(workspace, ".gitkeep"), []byte(""), 0644)
	_, _ = exec.Command("git", "-C", workspace, "add", ".").CombinedOutput()
	_, _ = exec.Command("git", "-C", workspace, "commit", "-m", "init").CombinedOutput()
	// Add itself as origin remote so pull has a target
	_, _ = exec.Command("git", "-C", workspace, "remote", "add", "origin", workspace).CombinedOutput()

	script := filepath.Join(workspace, "build.sh")
	_ = os.WriteFile(script, []byte("#!/bin/sh\necho 'building'\nexit 0\n"), 0755)
	_, _ = exec.Command("git", "-C", workspace, "add", "build.sh").CombinedOutput()
	_, _ = exec.Command("git", "-C", workspace, "commit", "-m", "add build script").CombinedOutput()

	svc := &config.ServiceConfig{
		Workspace: workspace,
		Build:     config.BuildConfig{Command: script},
		Repo:      config.RepoConfig{Branch: "main"},
	}
	buildErr := p.Build(context.Background(), svc)
	if buildErr != nil {
		t.Fatal(buildErr)
	}
}

func TestStart_NoCommand(t *testing.T) {
	p := New()
	svc := &config.ServiceConfig{
		Name:      "test-app",
		Workspace: t.TempDir(),
		Run:       config.RunConfig{Command: ""},
	}
	err := p.Start(context.Background(), svc)
	if err == nil {
		t.Fatal("expected error for empty run command")
	}
}

func TestStatus_ReturnsStoppedWhenNoPID(t *testing.T) {
	p := New()
	svc := &config.ServiceConfig{Name: "nonexistent"}
	status, err := p.Status(context.Background(), svc)
	if err != nil {
		t.Fatal(err)
	}
	if status != "stopped" {
		t.Errorf("expected stopped, got %s", status)
	}
}
