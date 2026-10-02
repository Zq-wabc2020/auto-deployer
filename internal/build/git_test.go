package build

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestPull_UpdatesWorkingDir(t *testing.T) {
	bareDir := t.TempDir()
	setupDir := t.TempDir()
	_ = runCmd(setupDir, "git", "init", "-b", "main")
	_ = runCmd(setupDir, "git", "config", "user.email", "test@test.com")
	_ = runCmd(setupDir, "git", "config", "user.name", "Test")
	_ = os.WriteFile(filepath.Join(setupDir, "README.md"), []byte("hello"), 0644)
	_ = runCmd(setupDir, "git", "add", ".")
	_ = runCmd(setupDir, "git", "commit", "-m", "init")
	_ = runCmd(setupDir, "git", "clone", "--bare", setupDir, bareDir)

	destDir := t.TempDir()
	if err := Fetch(context.Background(), bareDir, "", "main", destDir, io.Discard); err != nil {
		t.Fatal(err)
	}

	// Add new commit to bare repo
	_ = os.WriteFile(filepath.Join(setupDir, "new.txt"), []byte("new content"), 0644)
	_ = runCmd(setupDir, "git", "add", ".")
	_ = runCmd(setupDir, "git", "commit", "-m", "second")
	_ = runCmd(setupDir, "git", "push", bareDir, "main")

	// Use Fetch (Jenkins-style)
	err := Fetch(context.Background(), bareDir, "", "main", destDir, io.Discard)
	if err != nil {
		t.Fatal(err)
	}

	data, _ := os.ReadFile(filepath.Join(destDir, "new.txt"))
	if string(data) != "new content" {
		t.Errorf("expected new content, got %q", string(data))
	}
}

func TestFetch_FastPathDiscardsLocalMods(t *testing.T) {
	bareDir := t.TempDir()
	setupDir := t.TempDir()
	_ = runCmd(setupDir, "git", "init", "-b", "main")
	_ = runCmd(setupDir, "git", "config", "user.email", "test@test.com")
	_ = runCmd(setupDir, "git", "config", "user.name", "Test")
	_ = os.WriteFile(filepath.Join(setupDir, "file.txt"), []byte("original"), 0644)
	_ = runCmd(setupDir, "git", "add", ".")
	_ = runCmd(setupDir, "git", "commit", "-m", "init")
	_ = runCmd(setupDir, "git", "clone", "--bare", setupDir, bareDir)

	destDir := t.TempDir()
	if err := Fetch(context.Background(), bareDir, "", "main", destDir, io.Discard); err != nil {
		t.Fatal(err)
	}

	// Local modification in the working tree (uncommitted).
	_ = os.WriteFile(filepath.Join(destDir, "file.txt"), []byte("LOCALLY MODIFIED"), 0644)

	// Fast path (existing repo, matching origin) -> reset --hard must discard it.
	if err := Fetch(context.Background(), bareDir, "", "main", destDir, io.Discard); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(destDir, "file.txt"))
	if string(data) != "original" {
		t.Errorf("fast path should discard local mods via reset --hard, got %q", string(data))
	}
}

func runCmd(dir, name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
