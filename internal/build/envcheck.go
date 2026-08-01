package build

import (
	"fmt"
	"os/exec"
)

type environmentCheck struct {
	name string
	cmd  string
}

var requiredTools = []environmentCheck{
	{name: "git", cmd: "git"},
	{name: "java", cmd: "java"},
	{name: "mvn", cmd: "mvn"},
}

// CheckEnvironment verifies that all required tools are installed and available on PATH.
func CheckEnvironment() []error {
	var errs []error
	for _, tool := range requiredTools {
		if _, err := exec.LookPath(tool.cmd); err != nil {
			errs = append(errs, fmt.Errorf("%s is not installed or not in PATH: %w", tool.name, err))
		}
	}
	return errs
}

// EnsureGitConfig initializes git user.email and user.name in the workspace directory.
// This ensures git operations (commit, etc.) in the workspace don't fail due to
// missing global git configuration. Uses safe defaults if not configured globally.
func EnsureGitConfig(workspace string) error {
	// Check if already configured in this directory
	for _, key := range []string{"user.email", "user.name"} {
		cmd := exec.Command("git", "-C", workspace, "config", "--local", key)
		if out, err := cmd.Output(); err == nil && len(out) > 0 {
			continue // already set
		}
		// Not set locally — try global default
		defaultVal := ""
		switch key {
		case "user.email":
			defaultVal = "deployd@auto-generated.local"
		case "user.name":
			defaultVal = "deployd"
		}
		if defaultVal == "" {
			continue
		}
		if err := exec.Command("git", "-C", workspace, "config", "--local", key, defaultVal).Run(); err != nil {
			return fmt.Errorf("failed to set git %s in %s: %w", key, workspace, err)
		}
	}
	return nil
}
