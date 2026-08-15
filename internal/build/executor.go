package build

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// MergeEnv returns a copy of env with the given key=value overrides applied.
// Existing entries for overridden keys are REPLACED (appending duplicates would
// leave the original first-match entry effective in most shells).
func MergeEnv(env []string, overrides map[string]string) []string {
	out := make([]string, 0, len(env)+len(overrides))
	for _, kv := range env {
		k := kv
		if i := strings.IndexByte(kv, '='); i >= 0 {
			k = kv[:i]
		}
		if _, replaced := overrides[k]; replaced {
			continue
		}
		out = append(out, kv)
	}
	for k, v := range overrides {
		out = append(out, k+"="+v)
	}
	return out
}

// ExecuteBuild runs the given command in the workspace directory via `sh -c`,
// so shell semantics (&&, |, >, $VAR, etc.) work. It sets JAVA_HOME if a
// .java-version file exists in the workspace. out receives the command output.
func ExecuteBuild(workspace, command string, out io.Writer) error {
	if command == "" {
		return fmt.Errorf("build command is empty")
	}

	cmd := exec.Command("sh", "-c", command)
	cmd.Dir = workspace
	cmd.Stdout = out
	cmd.Stderr = out

	// Auto-detect Java version from .java-version file
	if javaVersion := detectJavaVersion(workspace); javaVersion != "" {
		if javaHome := findJavaHome(javaVersion); javaHome != "" {
			cmd.Env = MergeEnv(os.Environ(), map[string]string{
				"JAVA_HOME": javaHome,
				"PATH":      filepath.Join(javaHome, "bin") + string(os.PathListSeparator) + os.Getenv("PATH"),
			})
		}
	}

	fmt.Fprintf(out, "[build] executing: %s\n", command)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("build failed: %w", err)
	}
	fmt.Fprintln(out, "[build] build completed successfully")
	return nil
}

// detectJavaVersion reads .java-version file from workspace.
func detectJavaVersion(workspace string) string {
	versionFile := filepath.Join(workspace, ".java-version")
	data, err := os.ReadFile(versionFile)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// findJavaHome finds the JDK home for a given version.
// Tries jenv first, then system Java locations.
func findJavaHome(version string) string {
	// Try jenv
	if jenvPath, err := exec.Command("jenv", "prefix", version).Output(); err == nil {
		return strings.TrimSpace(string(jenvPath))
	}
	// Try system java_home
	if out, err := exec.Command("/usr/libexec/java_home", "-v", version).Output(); err == nil {
		return strings.TrimSpace(string(out))
	}
	return ""
}
