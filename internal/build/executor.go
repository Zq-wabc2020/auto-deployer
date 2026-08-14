package build

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

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
			cmd.Env = append(os.Environ(), "JAVA_HOME="+javaHome)
			cmd.Env = append(cmd.Env, "PATH="+javaHome+string(os.PathListSeparator)+os.Getenv("PATH"))
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
