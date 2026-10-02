package build

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Fetch clones or updates a specific branch.
//
// Fast path: if destDir is already a git repo whose origin matches, run
// `git fetch --force` + `checkout -f` + `reset --hard origin/<branch>` -- fast
// (reuses .git) and robust (reset --hard discards local modifications).
//
// Fallback (non-repo, mismatched origin, or fast-path failure): Jenkins-style
// clean state (rm .git, init, remote add, fetch, checkout), robust against
// force-push and corruption.
//
// Each git step is aborted when ctx is canceled or times out.
func Fetch(ctx context.Context, repoURL, keyFile, branch, destDir string, out io.Writer) error {
	if err := ensureDir(destDir); err != nil {
		return err
	}

	url := repoURL
	if !strings.HasPrefix(url, "git@") && !strings.HasPrefix(url, "ssh://") {
		url = HTTPSToSSH(url)
	}

	if isGitRepoWithOrigin(destDir, url) {
		if err := fastFetch(ctx, destDir, keyFile, branch, out); err == nil {
			return nil
		} else {
			fmt.Fprintf(out, "[git] fast fetch failed (%v), falling back to clean clone\n", err)
		}
	}
	return cleanFetch(ctx, destDir, url, keyFile, branch, out)
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
func fastFetch(ctx context.Context, destDir, keyFile, branch string, out io.Writer) error {
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
		if err := RunCommandCtx(ctx, cmd); err != nil {
			if ctx.Err() != nil {
				return fmt.Errorf("git %s aborted: %w", strings.Join(args, " "), ctx.Err())
			}
			return fmt.Errorf("git %s failed: %w", strings.Join(args, " "), err)
		}
	}
	return nil
}

// cleanFetch does a Jenkins-style clean clone: remove .git, init, remote add,
// fetch, checkout. Robust against force-push and corrupted state.
func cleanFetch(ctx context.Context, destDir, url, keyFile, branch string, out io.Writer) error {
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
	if err := RunCommandCtx(ctx, initCmd); err != nil {
		return fmt.Errorf("git init failed: %w", err)
	}

	// git remote add origin <url>
	remoteCmd := exec.Command("git", "-C", destDir, "remote", "add", "origin", url)
	remoteCmd.Stdout = out
	remoteCmd.Stderr = out
	if keyFile != "" {
		remoteCmd.Env = append(os.Environ(), "GIT_SSH_COMMAND="+SSHCommand(keyFile))
	}
	if err := RunCommandCtx(ctx, remoteCmd); err != nil {
		return fmt.Errorf("git remote add failed: %w", err)
	}

	// git fetch --force --progress origin <branch>
	fetchCmd := exec.Command("git", "-C", destDir, "fetch", "--force", "--progress", "origin", branch)
	fetchCmd.Stdout = out
	fetchCmd.Stderr = out
	if keyFile != "" {
		fetchCmd.Env = append(os.Environ(), "GIT_SSH_COMMAND="+SSHCommand(keyFile))
	}
	if err := RunCommandCtx(ctx, fetchCmd); err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("git fetch aborted: %w", ctx.Err())
		}
		return fmt.Errorf("git fetch failed: %w", err)
	}

	// git checkout -f <branch>
	checkoutCmd := exec.Command("git", "-C", destDir, "checkout", "-f", branch)
	checkoutCmd.Stdout = out
	checkoutCmd.Stderr = out
	if keyFile != "" {
		checkoutCmd.Env = append(os.Environ(), "GIT_SSH_COMMAND="+SSHCommand(keyFile))
	}
	if err := RunCommandCtx(ctx, checkoutCmd); err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("git checkout aborted: %w", ctx.Err())
		}
		return fmt.Errorf("git checkout failed: %w", err)
	}
	return nil
}

func ensureDir(dir string) error {
	return exec.Command("mkdir", "-p", dir).Run()
}
