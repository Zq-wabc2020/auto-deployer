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
