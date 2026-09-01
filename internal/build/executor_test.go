package build

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestExecuteBuild_Success(t *testing.T) {
	workspace := t.TempDir()
	script := filepath.Join(workspace, "build.sh")
	_ = os.WriteFile(script, []byte("#!/bin/sh\necho 'building...'\nexit 0\n"), 0755)

	err := ExecuteBuild(context.Background(), workspace, script, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
}

func TestExecuteBuild_Failure(t *testing.T) {
	workspace := t.TempDir()
	script := filepath.Join(workspace, "build.sh")
	_ = os.WriteFile(script, []byte("#!/bin/sh\necho 'failing...'\nexit 1\n"), 0755)

	err := ExecuteBuild(context.Background(), workspace, script, io.Discard)
	if err == nil {
		t.Fatal("expected error for failing build")
	}
}

func TestExecuteBuild_CommandNotFound(t *testing.T) {
	err := ExecuteBuild(context.Background(), "/tmp", "definitely-not-a-real-command-xyz", io.Discard)
	if err == nil {
		t.Fatal("expected error for missing command")
	}
}

func TestExecuteBuild_EmptyCommand(t *testing.T) {
	err := ExecuteBuild(context.Background(), "/tmp", "", io.Discard)
	if err == nil {
		t.Fatal("expected error for empty command")
	}
}

func TestExecuteBuild_ShellSemantics(t *testing.T) {
	// sh -c must support && and redirection (the reason for the switch from
	// the old no-shell SplitCommand execution).
	err := ExecuteBuild(context.Background(), "/tmp", "true && echo ok > /dev/null", io.Discard)
	if err != nil {
		t.Fatalf("expected shell semantics to work, got: %v", err)
	}
}

func TestExecuteBuild_TimeoutKillsCommand(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	start := time.Now()
	err := ExecuteBuild(ctx, "/tmp", "sleep 5; sleep 5", io.Discard)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected timeout error")
	}
	if elapsed > 3*time.Second {
		t.Fatalf("command should be killed at ~300ms, took %s", elapsed)
	}
}

// The timeout must kill the WHOLE process group: with `sh -c "sleep 30"`,
// killing only sh would orphan the sleep child.
func TestExecuteBuild_TimeoutKillsWholeProcessGroup(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	err := ExecuteBuild(ctx, "/tmp", "sleep 30", io.Discard)
	if err == nil {
		t.Fatal("expected timeout error")
	}

	// Give the group kill a moment to land, then verify no sleep survives.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		out, _ := exec.Command("pgrep", "-f", "sleep 30").Output()
		if strings.TrimSpace(string(out)) == "" {
			return // clean
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Error("sleep 30 still running after timeout: process group not killed")
}
