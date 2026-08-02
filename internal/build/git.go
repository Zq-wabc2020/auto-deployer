package build

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// Clone clones a git repository into destDir.
// If keyFile is provided, GIT_SSH_COMMAND is set for SSH authentication.
// The repoURL is automatically converted from HTTPS to SSH format if needed.
func Clone(repoURL, keyFile, branch, destDir string) error {
	if err := ensureDir(destDir); err != nil {
		return err
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

// Pull performs git pull --force origin <branch> in destDir,
// resetting local changes and cleaning untracked files.
// If keyFile is provided, GIT_SSH_COMMAND is set for SSH authentication.
func Pull(destDir, branch, keyFile string) error {
	// Ensure we're on the correct branch (not detached HEAD)
	checkoutCmd := exec.Command("git", "checkout", branch)
	checkoutCmd.Dir = destDir
	checkoutCmd.Stdout = os.Stdout
	checkoutCmd.Stderr = os.Stderr
	if keyFile != "" {
		checkoutCmd.Env = append(os.Environ(), "GIT_SSH_COMMAND="+SSHCommand(keyFile))
	}
	if err := checkoutCmd.Run(); err != nil {
		// If branch doesn't exist locally, create it
		checkoutCmd = exec.Command("git", "checkout", "-b", branch, "origin/"+branch)
		checkoutCmd.Dir = destDir
		checkoutCmd.Stdout = os.Stdout
		checkoutCmd.Stderr = os.Stderr
		if keyFile != "" {
			checkoutCmd.Env = append(os.Environ(), "GIT_SSH_COMMAND="+SSHCommand(keyFile))
		}
		if err := checkoutCmd.Run(); err != nil {
			return fmt.Errorf("git checkout failed: %w", err)
		}
	}

	forceArgs := []string{"pull", "--force", "origin", branch}
	cmd := exec.Command("git", forceArgs...)
	cmd.Dir = destDir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if keyFile != "" {
		cmd.Env = append(os.Environ(), "GIT_SSH_COMMAND="+SSHCommand(keyFile))
	}
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git pull --force failed: %w", err)
	}
	// Reset local changes to match remote
	resetCmd := exec.Command("git", "reset", "--hard", "origin/"+branch)
	resetCmd.Dir = destDir
	resetCmd.Stdout = os.Stdout
	resetCmd.Stderr = os.Stderr
	if keyFile != "" {
		resetCmd.Env = append(os.Environ(), "GIT_SSH_COMMAND="+SSHCommand(keyFile))
	}
	if err := resetCmd.Run(); err != nil {
		return fmt.Errorf("git reset --hard failed: %w", err)
	}
	// Clean untracked files (but respect .gitignore)
	cleanCmd := exec.Command("git", "clean", "-fd")
	cleanCmd.Dir = destDir
	cleanCmd.Stdout = os.Stdout
	cleanCmd.Stderr = os.Stderr
	if keyFile != "" {
		cleanCmd.Env = append(os.Environ(), "GIT_SSH_COMMAND="+SSHCommand(keyFile))
	}
	if err := cleanCmd.Run(); err != nil {
		return fmt.Errorf("git clean failed: %w", err)
	}
	return nil
}

func ensureDir(dir string) error {
	return exec.Command("mkdir", "-p", dir).Run()
}
