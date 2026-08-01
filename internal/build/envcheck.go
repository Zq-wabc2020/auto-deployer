package build

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type environmentCheck struct {
	name string
	cmd  string
}

var toolSearchPaths = []string{
	"",
	"/usr/local/bin",
	"/opt/maven/bin",
	"/opt/homebrew/bin",
	"/snap/bin",
}

var requiredTools = []environmentCheck{
	{name: "git", cmd: "git"},
	{name: "java", cmd: "java"},
	{name: "mvn", cmd: "mvn"},
}

// CheckEnvironment verifies that all required tools are installed and available on PATH,
// or at a common install location as fallback.
func CheckEnvironment() []error {
	var errs []error
	for _, tool := range requiredTools {
		found := false
		if _, err := exec.LookPath(tool.cmd); err == nil {
			found = true
		}
		if !found {
			for _, p := range toolSearchPaths {
				if p == "" {
					continue
				}
				path := filepath.Join(p, tool.cmd)
				if _, err := os.Stat(path); err == nil {
					found = true
					break
				}
			}
		}
		if !found {
			errs = append(errs, fmt.Errorf("%s is not installed or not in PATH: %w", tool.name, tool.cmd))
		}
	}
	return errs
}

// EnsureGitConfig initializes git user.email and user.name in the workspace directory.
// This ensures git operations (commit, etc.) in the workspace don't fail due to
// missing global git configuration. Uses safe defaults if not configured globally.
// Only sets config when a .git directory already exists (after clone).
func EnsureGitConfig(workspace string) error {
	gitDir := filepath.Join(workspace, ".git")
	if _, err := os.Stat(gitDir); os.IsNotExist(err) {
		return nil // .git not created yet, will be set after clone
	}

	for _, key := range []string{"user.email", "user.name"} {
		cmd := exec.Command("git", "-C", workspace, "config", "--local", key)
		if out, err := cmd.Output(); err == nil && len(out) > 0 {
			continue // already set
		}
		// Not set locally — try global first
		globalCmd := exec.Command("git", "config", "--global", "--get", key)
		globalOut, _ := globalCmd.Output()
		defaultVal := strings.TrimSpace(string(globalOut))

		// Fall back to safe defaults
		if defaultVal == "" {
			switch key {
			case "user.email":
				defaultVal = "deployd@auto-generated.local"
			case "user.name":
				defaultVal = "deployd"
			}
		}

		setCmd := exec.Command("git", "-C", workspace, "config", "--local", key, defaultVal)
		if err := setCmd.Run(); err != nil {
			return fmt.Errorf("failed to set git %s in %s: %w", key, workspace, err)
		}
	}
	return nil
}
