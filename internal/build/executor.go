package build

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// ExecuteBuild runs the given shell command in the specified workspace directory.
// It automatically sets JAVA_HOME if a .java-version file exists in the workspace.
func ExecuteBuild(workspace, command string) error {
	if command == "" {
		return fmt.Errorf("build command is empty")
	}

	parts := SplitCommand(command)
	if len(parts) == 0 {
		return fmt.Errorf("failed to parse build command: %q", command)
	}

	cmd := exec.Command(parts[0], parts[1:]...)
	cmd.Dir = workspace
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	// Auto-detect Java version from .java-version file
	if javaVersion := detectJavaVersion(workspace); javaVersion != "" {
		if javaHome := findJavaHome(javaVersion); javaHome != "" {
			cmd.Env = append(os.Environ(), "JAVA_HOME="+javaHome)
			cmd.Env = append(cmd.Env, "PATH="+javaHome+string(os.PathListSeparator)+os.Getenv("PATH"))
		}
	}

	fmt.Printf("[build] executing: %s\n", command)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("build failed: %w", err)
	}
	fmt.Println("[build] build completed successfully")
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

// SplitCommand splits a shell command string into arguments for exec.Command.
// It handles quoted strings (single and double quotes).
func SplitCommand(command string) []string {
	var result []string
	current := ""
	inQuote := false
	quoteChar := byte(0)

	for i := 0; i < len(command); i++ {
		ch := command[i]
		if inQuote {
			if ch == quoteChar {
				inQuote = false
			} else {
				current += string(ch)
			}
			continue
		}
		switch ch {
		case '"', '\'':
			inQuote = true
			quoteChar = ch
		case ' ', '\t':
			if current != "" {
				result = append(result, current)
				current = ""
			}
		default:
			current += string(ch)
		}
	}
	if current != "" {
		result = append(result, current)
	}
	return result
}
