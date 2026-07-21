package build

import (
	"os"
	"strings"
	"testing"
)

func TestCheckEnvironment(t *testing.T) {
	errs := CheckEnvironment()
	if len(errs) > 0 {
		t.Logf("environment check results (expected to pass on dev machine): %v", errs)
	}
}

func TestCheckEnvironment_DetectsMissing(t *testing.T) {
	origPath := ""
	for _, env := range os.Environ() {
		if strings.HasPrefix(env, "PATH=") {
			origPath = strings.TrimPrefix(env, "PATH=")
			break
		}
	}

	os.Setenv("PATH", "/nonexistent/path")
	defer func() {
		os.Setenv("PATH", origPath)
	}()

	errs := CheckEnvironment()
	if len(errs) == 0 {
		t.Fatal("expected errors when tools are missing")
	}
}
