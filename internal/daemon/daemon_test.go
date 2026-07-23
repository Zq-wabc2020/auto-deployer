package daemon

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStart_FailsWithoutConfig(t *testing.T) {
	dir := t.TempDir()
	os.Setenv("HOME", dir)
	defer os.Unsetenv("HOME")

	err := Start(filepath.Join(dir, "nonexistent.yaml"))
	if err == nil {
		t.Fatal("expected error when config file does not exist")
	}
}

func TestStart_FailsInvalidConfig(t *testing.T) {
	dir := t.TempDir()
	os.Setenv("HOME", dir)
	defer os.Unsetenv("HOME")

	badConfig := filepath.Join(dir, "bad.yaml")
	_ = os.WriteFile(badConfig, []byte("invalid: yaml: ["), 0644)

	err := Start(badConfig)
	if err == nil {
		t.Fatal("expected error for invalid yaml")
	}
}
