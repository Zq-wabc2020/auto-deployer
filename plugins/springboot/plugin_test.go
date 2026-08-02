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

	// Create a bare repo to act as remote
	bareDir := filepath.Join(workspace, "..", "bare-repo")
	_ = os.MkdirAll(bareDir, 0755)
	_ = exec.Command("git", "-C", bareDir, "init", "--bare").Run()

	// Create a working repo to push from
	workDir := filepath.Join(workspace, "..", "work-repo")
	_ = os.MkdirAll(workDir, 0755)
	_ = exec.Command("git", "-C", workDir, "init", "-b", "main").Run()
	_, _ = exec.Command("git", "-C", workDir, "config", "user.email", "test@test.com").CombinedOutput()
	_, _ = exec.Command("git", "-C", workDir, "config", "user.name", "Test").CombinedOutput()

	script := filepath.Join(workDir, "build.sh")
	_ = os.WriteFile(script, []byte("#!/bin/sh\necho 'building'\nexit 0\n"), 0755)
	_ = os.WriteFile(filepath.Join(workDir, ".gitkeep"), []byte(""), 0644)
	_ = exec.Command("git", "-C", workDir, "add", ".").Run()
	_ = exec.Command("git", "-C", workDir, "commit", "-m", "init").Run()
	_, _ = exec.Command("git", "-C", workDir, "remote", "add", "origin", bareDir).CombinedOutput()
	_, _ = exec.Command("git", "-C", workDir, "push", "-u", "origin", "main").CombinedOutput()

	svc := &config.ServiceConfig{
		Workspace: workspace,
		Build:     config.BuildConfig{Command: script},
		Repo:      config.RepoConfig{URL: bareDir, Branch: "main"},
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
