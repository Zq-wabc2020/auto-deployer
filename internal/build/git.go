package build

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Clone clones a git repository into destDir.
// If keyFile is provided, GIT_SSH_COMMAND is set for SSH authentication.
// The repoURL is automatically converted from HTTPS to SSH format if needed.
// The destDir must be empty or not exist; it will be created if needed.
func Clone(repoURL, keyFile, branch, destDir string) error {
	// Ensure destDir is empty
	if info, err := os.Stat(destDir); err == nil && info.IsDir() {
		entries, err := os.ReadDir(destDir)
		if err != nil {
			return fmt.Errorf("failed to read directory: %w", err)
		}
		if len(entries) > 0 {
			// Directory exists and is not empty, remove all contents
			for _, entry := range entries {
				path := filepath.Join(destDir, entry.Name())
				if err := os.RemoveAll(path); err != nil {
					return fmt.Errorf("failed to remove %s: %w", entry.Name(), err)
				}
			}
		}
	} else if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to stat directory: %w", err)
	}

	url := repoURL
	if !strings.HasPrefix(url, "git@") && !strings.HasPrefix(url, "ssh://") {
		url = HTTPSToSSH(url)
	}

	args := []string{"clone"}
	if branch != "" {
		args = append(args, "--branch", branch, "--single-branch")
	}
	args = append(args, url, destDir)

	cmd := exec.Command("git", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if keyFile != "" {
		cmd.Env = append(os.Environ(), "GIT_SSH_COMMAND="+SSHCommand(keyFile))
	}
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git clone failed: %w", err)
	}
	return nil
}

// Fetch clones or updates a specific branch using Jenkins-style approach:
// git init → git remote add → git fetch → git checkout -f
// This ensures a clean state every time, similar to Jenkins Git plugin.
func Fetch(repoURL, keyFile, branch, destDir string) error {
	if err := ensureDir(destDir); err != nil {
		return err
	}

	url := repoURL
	if !strings.HasPrefix(url, "git@") && !strings.HasPrefix(url, "ssh://") {
		url = HTTPSToSSH(url)
	}

	// Check if .git exists, if so, remove it for clean state
	gitDir := filepath.Join(destDir, ".git")
	if _, err := os.Stat(gitDir); err == nil {
		if err := os.RemoveAll(gitDir); err != nil {
			return fmt.Errorf("failed to remove .git: %w", err)
		}
	}

	// git init
	initCmd := exec.Command("git", "init", destDir)
	initCmd.Stdout = os.Stdout
	initCmd.Stderr = os.Stderr
	if err := initCmd.Run(); err != nil {
		return fmt.Errorf("git init failed: %w", err)
	}

	// git remote add origin <url>
	remoteCmd := exec.Command("git", "-C", destDir, "remote", "add", "origin", url)
	remoteCmd.Stdout = os.Stdout
	remoteCmd.Stderr = os.Stderr
	if keyFile != "" {
		remoteCmd.Env = append(os.Environ(), "GIT_SSH_COMMAND="+SSHCommand(keyFile))
	}
	if err := remoteCmd.Run(); err != nil {
		return fmt.Errorf("git remote add failed: %w", err)
	}

	// git fetch --force --progress origin <branch>
	fetchCmd := exec.Command("git", "-C", destDir, "fetch", "--force", "--progress", "origin", branch)
	fetchCmd.Stdout = os.Stdout
	fetchCmd.Stderr = os.Stderr
	if keyFile != "" {
		fetchCmd.Env = append(os.Environ(), "GIT_SSH_COMMAND="+SSHCommand(keyFile))
	}
	if err := fetchCmd.Run(); err != nil {
		return fmt.Errorf("git fetch failed: %w", err)
	}

	// git checkout -f <branch>
	checkoutCmd := exec.Command("git", "-C", destDir, "checkout", "-f", branch)
	checkoutCmd.Stdout = os.Stdout
	checkoutCmd.Stderr = os.Stderr
	if keyFile != "" {
		checkoutCmd.Env = append(os.Environ(), "GIT_SSH_COMMAND="+SSHCommand(keyFile))
	}
	if err := checkoutCmd.Run(); err != nil {
		return fmt.Errorf("git checkout failed: %w", err)
	}

	return nil
}

func ensureDir(dir string) error {
	return exec.Command("mkdir", "-p", dir).Run()
}

// GetLatestAuthorEmail executes git log -1 --format=%ae in the workspace directory.
// Returns empty string if the directory doesn't exist, isn't a git repo, or the command fails.
func GetLatestAuthorEmail(workspace, branch string) string {
	cmd := exec.Command("git", "-C", workspace, "log", "-1", "--format=%ae")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
