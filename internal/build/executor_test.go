package build

import (
	"os"
	"path/filepath"
	"testing"
)

func TestExecuteBuild_Success(t *testing.T) {
	workspace := t.TempDir()
	script := filepath.Join(workspace, "build.sh")
	_ = os.WriteFile(script, []byte("#!/bin/sh\necho 'building...'\nexit 0\n"), 0755)

	err := ExecuteBuild(workspace, script)
	if err != nil {
		t.Fatal(err)
	}
}

func TestExecuteBuild_Failure(t *testing.T) {
	workspace := t.TempDir()
	script := filepath.Join(workspace, "build.sh")
	_ = os.WriteFile(script, []byte("#!/bin/sh\necho 'failing...'\nexit 1\n"), 0755)

	err := ExecuteBuild(workspace, script)
	if err == nil {
		t.Fatal("expected error for failing build")
	}
}

func TestExecuteBuild_CommandNotFound(t *testing.T) {
	err := ExecuteBuild("/tmp", "definitely-not-a-real-command-xyz")
	if err == nil {
		t.Fatal("expected error for missing command")
	}
}

func TestExecuteBuild_EmptyCommand(t *testing.T) {
	err := ExecuteBuild("/tmp", "")
	if err == nil {
		t.Fatal("expected error for empty command")
	}
}

func TestSplitCommand_Simple(t *testing.T) {
	result := SplitCommand("mvn package -DskipTests")
	expected := []string{"mvn", "package", "-DskipTests"}
	if len(result) != len(expected) {
		t.Fatalf("expected %v, got %v", expected, result)
	}
	for i, v := range expected {
		if result[i] != v {
			t.Errorf("expected[%d]=%q, got %q", i, v, result[i])
		}
	}
}

func TestSplitCommand_Quoted(t *testing.T) {
	result := SplitCommand(`echo "hello world"`)
	expected := []string{"echo", "hello world"}
	if len(result) != len(expected) {
		t.Fatalf("expected %v, got %v", expected, result)
	}
}
