package build

import (
	"os"
	"path/filepath"
	"testing"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestCopyArtifact_FlatFiles(t *testing.T) {
	ws := t.TempDir()
	writeFile(t, filepath.Join(ws, "target", "app-1.0.jar"), "jar")
	writeFile(t, filepath.Join(ws, "target", "app-1.0.jar.original.jar"), "source")
	dest := filepath.Join(t.TempDir(), "deploy")

	copied, err := CopyArtifact(ws, "target/*.jar", dest, os.Stdout)
	if err != nil {
		t.Fatal(err)
	}
	if len(copied) != 1 || copied[0] != "app-1.0.jar" {
		t.Errorf("expected [app-1.0.jar], got %v", copied)
	}
	if readFile(t, filepath.Join(dest, "app-1.0.jar")) != "jar" {
		t.Error("jar not copied")
	}
	if _, err := os.Stat(filepath.Join(dest, "app-1.0.jar.original.jar")); !os.IsNotExist(err) {
		t.Error("*.original.jar should be skipped")
	}
}

func TestCopyArtifact_NoMatch(t *testing.T) {
	ws := t.TempDir()
	if _, err := CopyArtifact(ws, "target/*.jar", filepath.Join(t.TempDir(), "d"), os.Stdout); err == nil {
		t.Fatal("expected error for no matching artifact")
	}
}

// A literal directory pattern (with or without trailing slash) copies the
// directory's CONTENTS into destDir.
func TestCopyArtifact_LiteralDirCopiesContents(t *testing.T) {
	for _, pattern := range []string{"dist", "dist/"} {
		ws := t.TempDir()
		writeFile(t, filepath.Join(ws, "dist", "index.html"), "html")
		writeFile(t, filepath.Join(ws, "dist", "js", "app.js"), "js")
		dest := filepath.Join(t.TempDir(), "html")

		copied, err := CopyArtifact(ws, pattern, dest, os.Stdout)
		if err != nil {
			t.Fatalf("pattern %q: %v", pattern, err)
		}
		if readFile(t, filepath.Join(dest, "index.html")) != "html" {
			t.Errorf("pattern %q: index.html not at dest root", pattern)
		}
		if readFile(t, filepath.Join(dest, "js", "app.js")) != "js" {
			t.Errorf("pattern %q: nested file not copied", pattern)
		}
		if len(copied) == 0 {
			t.Errorf("pattern %q: expected copied names, got none", pattern)
		}
	}
}

// A wildcard pattern that matches a directory keeps the directory's own name
// in dest and copies its subtree under it.
func TestCopyArtifact_WildcardDirKeepsName(t *testing.T) {
	ws := t.TempDir()
	writeFile(t, filepath.Join(ws, "dist", "index.html"), "html")
	writeFile(t, filepath.Join(ws, "dist", "js", "app.js"), "js")
	dest := filepath.Join(t.TempDir(), "html")

	copied, err := CopyArtifact(ws, "dist/*", dest, os.Stdout)
	if err != nil {
		t.Fatal(err)
	}
	if readFile(t, filepath.Join(dest, "index.html")) != "html" {
		t.Error("flat file not copied")
	}
	if readFile(t, filepath.Join(dest, "js", "app.js")) != "js" {
		t.Error("directory not copied as named subtree")
	}
	var found bool
	for _, n := range copied {
		if n == "js" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected 'js' in copied names, got %v", copied)
	}
}
