package build

import (
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestExecuteBuild_Success(t *testing.T) {
	workspace := t.TempDir()
	script := filepath.Join(workspace, "build.sh")
	_ = os.WriteFile(script, []byte("#!/bin/sh\necho 'building...'\nexit 0\n"), 0755)

	err := ExecuteBuild(workspace, script, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
}

func TestExecuteBuild_Failure(t *testing.T) {
	workspace := t.TempDir()
	script := filepath.Join(workspace, "build.sh")
	_ = os.WriteFile(script, []byte("#!/bin/sh\necho 'failing...'\nexit 1\n"), 0755)

	err := ExecuteBuild(workspace, script, io.Discard)
	if err == nil {
		t.Fatal("expected error for failing build")
	}
}

func TestExecuteBuild_CommandNotFound(t *testing.T) {
	err := ExecuteBuild("/tmp", "definitely-not-a-real-command-xyz", io.Discard)
	if err == nil {
		t.Fatal("expected error for missing command")
	}
}

func TestExecuteBuild_EmptyCommand(t *testing.T) {
	err := ExecuteBuild("/tmp", "", io.Discard)
	if err == nil {
		t.Fatal("expected error for empty command")
	}
}

func TestExecuteBuild_ShellSemantics(t *testing.T) {
	// sh -c must support && and redirection (the reason for the switch from
	// the old no-shell SplitCommand execution).
	err := ExecuteBuild("/tmp", "true && echo ok > /dev/null", io.Discard)
	if err != nil {
		t.Fatalf("expected shell semantics to work, got: %v", err)
	}
}
