package build

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// Clone clones a git repository into destDir. If token is provided, it is inserted
// into HTTPS URLs for authentication.
func Clone(repoURL, token, branch, destDir string) error {
	if err := ensureDir(destDir); err != nil {
		return err
	}

	url := repoURL
	if token != "" {
		url = insertToken(url, token)
	}

	args := []string{"clone"}
	if branch != "" {
		args = append(args, "--branch", branch, "--single-branch")
	}
	args = append(args, url, destDir)

	cmd := exec.Command("git", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git clone failed: %w", err)
	}
	return nil
}

// Pull performs git pull origin <branch> in destDir.
func Pull(destDir, branch string) error {
	cmd := exec.Command("git", "pull", "origin", branch)
	cmd.Dir = destDir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git pull failed: %w", err)
	}
	return nil
}

func insertToken(url, token string) string {
	if strings.HasPrefix(url, "https://") {
		return strings.Replace(url, "https://", "https://"+token+"@", 1)
	}
	return url
}

func ensureDir(dir string) error {
	return exec.Command("mkdir", "-p", dir).Run()
}
