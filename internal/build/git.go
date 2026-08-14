package build

import (
	"fmt"
	"io"
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

// Fetch clones or updates a specific branch.
//
// Fast path: if destDir is already a git repo whose origin matches, run
// `git fetch --force` + `checkout -f` + `reset --hard origin/<branch>` -- fast
// (reuses .git) and robust (reset --hard discards local modifications).
//
// Fallback (non-repo, mismatched origin, or fast-path failure): Jenkins-style
// clean state (rm .git, init, remote add, fetch, checkout), robust against
// force-push and corruption.
func Fetch(repoURL, keyFile, branch, destDir string, out io.Writer) error {
	if err := ensureDir(destDir); err != nil {
		return err
	}

	url := repoURL
	if !strings.HasPrefix(url, "git@") && !strings.HasPrefix(url, "ssh://") {
		url = HTTPSToSSH(url)
	}

	if isGitRepoWithOrigin(destDir, url) {
		if err := fastFetch(destDir, keyFile, branch, out); err == nil {
			return nil
		} else {
			fmt.Fprintf(out, "[git] fast fetch failed (%v), falling back to clean clone\n", err)
		}
	}
	return cleanFetch(destDir, url, keyFile, branch, out)
}

// isGitRepoWithOrigin reports whether destDir is a git repo whose origin URL
// matches url.
func isGitRepoWithOrigin(destDir, url string) bool {
	if _, err := os.Stat(filepath.Join(destDir, ".git")); err != nil {
		return false
	}
	out, err := exec.Command("git", "-C", destDir, "remote", "get-url", "origin").Output()
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(out)) == url
}

// fastFetch updates an existing repo: fetch --force, checkout -f, reset --hard.
func fastFetch(destDir, keyFile, branch string, out io.Writer) error {
	env := os.Environ()
	if keyFile != "" {
		env = append(env, "GIT_SSH_COMMAND="+SSHCommand(keyFile))
	}
	steps := [][]string{
		{"fetch", "--force", "--progress", "origin", branch},
		{"checkout", "-f", branch},
		{"reset", "--hard", "origin/" + branch},
	}
	for _, args := range steps {
		cmd := exec.Command("git", append([]string{"-C", destDir}, args...)...)
		cmd.Stdout = out
		cmd.Stderr = out
		cmd.Env = env
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("git %s failed: %w", strings.Join(args, " "), err)
		}
	}
	return nil
}

// cleanFetch does a Jenkins-style clean clone: remove .git, init, remote add,
// fetch, checkout. Robust against force-push and corrupted state.
func cleanFetch(destDir, url, keyFile, branch string, out io.Writer) error {
	// Remove .git for a clean state.
	if _, err := os.Stat(filepath.Join(destDir, ".git")); err == nil {
		if err := os.RemoveAll(filepath.Join(destDir, ".git")); err != nil {
			return fmt.Errorf("failed to remove .git: %w", err)
		}
	}

	// git init
	initCmd := exec.Command("git", "init", destDir)
	initCmd.Stdout = out
	initCmd.Stderr = out
	if err := initCmd.Run(); err != nil {
		return fmt.Errorf("git init failed: %w", err)
	}

	// git remote add origin <url>
	remoteCmd := exec.Command("git", "-C", destDir, "remote", "add", "origin", url)
	remoteCmd.Stdout = out
	remoteCmd.Stderr = out
	if keyFile != "" {
		remoteCmd.Env = append(os.Environ(), "GIT_SSH_COMMAND="+SSHCommand(keyFile))
	}
	if err := remoteCmd.Run(); err != nil {
		return fmt.Errorf("git remote add failed: %w", err)
	}

	// git fetch --force --progress origin <branch>
	fetchCmd := exec.Command("git", "-C", destDir, "fetch", "--force", "--progress", "origin", branch)
	fetchCmd.Stdout = out
	fetchCmd.Stderr = out
	if keyFile != "" {
		fetchCmd.Env = append(os.Environ(), "GIT_SSH_COMMAND="+SSHCommand(keyFile))
	}
	if err := fetchCmd.Run(); err != nil {
		return fmt.Errorf("git fetch failed: %w", err)
	}

	// git checkout -f <branch>
	checkoutCmd := exec.Command("git", "-C", destDir, "checkout", "-f", branch)
	checkoutCmd.Stdout = out
	checkoutCmd.Stderr = out
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

// GetLatestCommit returns the subject of the latest commit on the checked-out
// branch (the message filled in when committing), for inclusion in deployment
// notifications. Returns "" if unavailable.
func GetLatestCommit(workspace, branch string) string {
	cmd := exec.Command("git", "-C", workspace, "log", "-1", "--format=%s")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
