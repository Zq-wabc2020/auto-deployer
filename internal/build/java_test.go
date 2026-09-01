package build

import (
	"os"
	"path/filepath"
	"testing"
)

func mkJvm(t *testing.T, root, name string) string {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Join(dir, "bin"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "bin", "java"), []byte("#!/bin/sh\n"), 0755); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestScanJvmDir_DebianNaming(t *testing.T) {
	root := t.TempDir()
	java8 := mkJvm(t, root, "java-8-openjdk-amd64")
	mkJvm(t, root, "java-17-openjdk-amd64")

	if got := scanJvmDir(root, "8"); got != java8 {
		t.Errorf("expected %s, got %s", java8, got)
	}
	if got := scanJvmDir(root, "17"); got == "" {
		t.Error("expected java-17 dir to match version 17")
	}
	if got := scanJvmDir(root, "11"); got != "" {
		t.Errorf("expected no match for version 11, got %s", got)
	}
}

func TestScanJvmDir_RHELStyleLegacyName(t *testing.T) {
	root := t.TempDir()
	java8 := mkJvm(t, root, "java-1.8.0-openjdk")

	if got := scanJvmDir(root, "8"); got != java8 {
		t.Errorf("java-1.8.0-openjdk is Java 8: expected %s, got %s", java8, got)
	}
}

func TestScanJvmDir_InvalidVersion(t *testing.T) {
	root := t.TempDir()
	mkJvm(t, root, "java-8-openjdk-amd64")
	if got := scanJvmDir(root, "abc"); got != "" {
		t.Errorf("expected empty for invalid version, got %s", got)
	}
	if got := scanJvmDir(root + "-missing", "8"); got != "" {
		t.Errorf("expected empty for missing root, got %s", got)
	}
}
