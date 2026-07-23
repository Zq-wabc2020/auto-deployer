package springboot

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"auto-deployer/internal/config"
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
	script := filepath.Join(workspace, "build.sh")
	_ = os.WriteFile(script, []byte("#!/bin/sh\necho 'building'\nexit 0\n"), 0755)

	svc := &config.ServiceConfig{
		Workspace: workspace,
		Build:     config.BuildConfig{Command: script},
	}
	err := p.Build(context.Background(), svc)
	if err != nil {
		t.Fatal(err)
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
