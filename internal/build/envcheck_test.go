package build

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestCheckEnvironment(t *testing.T) {
	errs := CheckEnvironment()
	if len(errs) > 0 {
		t.Logf("environment check results (expected to pass on dev machine): %v", errs)
	}
}

func TestCheckEnvironment_DetectsMissing(t *testing.T) {
	origPath := ""
	for _, env := range os.Environ() {
		if strings.HasPrefix(env, "PATH=") {
			origPath = strings.TrimPrefix(env, "PATH=")
			break
		}
	}

	os.Setenv("PATH", "/nonexistent/path")
	defer func() {
		os.Setenv("PATH", origPath)
	}()

	errs := CheckEnvironment()
	if len(errs) == 0 {
		t.Fatal("expected errors when tools are missing")
	}
}

func TestEnsureGitConfig(t *testing.T) {
	workspace := t.TempDir()

	// Initialize a git repo in the workspace
	if err := os.MkdirAll(workspace, 0755); err != nil {
		t.Fatal(err)
	}
	if err := exec.Command("git", "-C", workspace, "init", "-b", "main").Run(); err != nil {
		t.Fatalf("failed to init git repo: %v", err)
	}

	// Ensure git config
	if err := EnsureGitConfig(workspace); err != nil {
		t.Fatalf("EnsureGitConfig failed: %v", err)
	}

	// Verify config was set
	for _, key := range []string{"user.email", "user.name"} {
		out, err := exec.Command("git", "-C", workspace, "config", "--local", key).Output()
		if err != nil {
			t.Errorf("failed to get git %s: %v", key, err)
			continue
		}
		if len(strings.TrimSpace(string(out))) == 0 {
			t.Errorf("git %s is empty", key)
		}
	}
}

func TestEnsureGitConfig_PreservesExisting(t *testing.T) {
	workspace := t.TempDir()

	// Initialize a git repo with existing config
	if err := exec.Command("git", "-C", workspace, "init", "-b", "main").Run(); err != nil {
		t.Fatalf("failed to init git repo: %v", err)
	}
	if err := exec.Command("git", "-C", workspace, "config", "--local", "user.email", "existing@test.com").Run(); err != nil {
		t.Fatalf("failed to set existing email: %v", err)
	}

	// Ensure git config — should not overwrite existing
	if err := EnsureGitConfig(workspace); err != nil {
		t.Fatalf("EnsureGitConfig failed: %v", err)
	}

	// Verify existing email is preserved
	out, err := exec.Command("git", "-C", workspace, "config", "--local", "user.email").Output()
	if err != nil {
		t.Fatalf("failed to get git user.email: %v", err)
	}
	if strings.TrimSpace(string(out)) != "existing@test.com" {
		t.Errorf("expected existing email preserved, got %q", strings.TrimSpace(string(out)))
	}
}
