package build

import (
	"fmt"
	"os/exec"
	"strings"
)

// ExecuteBuild runs the given shell command in the specified workspace directory.
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

	fmt.Printf("[build] executing: %s\n", command)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("build failed: %w", err)
	}
	fmt.Println("[build] build completed successfully")
	return nil
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
