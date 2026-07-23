package build

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestClone_WithLocalRepo(t *testing.T) {
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
	err := Clone(bareDir, "", "main", destDir)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(destDir + "/README.md"); err != nil {
		t.Error("README.md should exist after clone")
	}
}

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
	_ = Clone(bareDir, "", "main", destDir)

	// Add new commit to bare repo
	_ = os.WriteFile(filepath.Join(setupDir, "new.txt"), []byte("new content"), 0644)
	_ = runCmd(setupDir, "git", "add", ".")
	_ = runCmd(setupDir, "git", "commit", "-m", "second")
	_ = runCmd(setupDir, "git", "push", bareDir, "main")

	err := Pull(destDir, "main")
	if err != nil {
		t.Fatal(err)
	}

	data, _ := os.ReadFile(filepath.Join(destDir, "new.txt"))
	if string(data) != "new content" {
		t.Errorf("expected new content, got %q", string(data))
	}
}

func runCmd(dir, name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
