package build

import (
	"context"
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

	copied, err := CopyArtifact(context.Background(), ws, "target/*.jar", dest, os.Stdout)
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
	if _, err := CopyArtifact(context.Background(), ws, "target/*.jar", filepath.Join(t.TempDir(), "d"), os.Stdout); err == nil {
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

		copied, err := CopyArtifact(context.Background(), ws, pattern, dest, os.Stdout)
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

	copied, err := CopyArtifact(context.Background(), ws, "dist/*", dest, os.Stdout)
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

// 列表形式：两个具体文件（spec §9 的 hello1.txt + test.json 场景），平铺到 dest 根。
func TestCopyArtifact_ListTwoFiles(t *testing.T) {
	ws := t.TempDir()
	writeFile(t, filepath.Join(ws, "hello1.txt"), "hello")
	writeFile(t, filepath.Join(ws, "test.json"), `{"a":1}`)
	dest := filepath.Join(t.TempDir(), "deploy")

	copied, err := CopyArtifact(context.Background(), ws, []string{"hello1.txt", "test.json"}, dest, os.Stdout)
	if err != nil {
		t.Fatal(err)
	}
	if len(copied) != 2 {
		t.Fatalf("expected 2 copied names, got %v", copied)
	}
	if readFile(t, filepath.Join(dest, "hello1.txt")) != "hello" {
		t.Error("hello1.txt not copied")
	}
	if readFile(t, filepath.Join(dest, "test.json")) != `{"a":1}` {
		t.Error("test.json not copied")
	}
}

// 单字符串形式仍是合法输入（向后兼容；等价于单元素列表）。
func TestCopyArtifact_SingleStringUnchanged(t *testing.T) {
	ws := t.TempDir()
	writeFile(t, filepath.Join(ws, "target", "app.jar"), "jar")
	dest := filepath.Join(t.TempDir(), "deploy")

	copied, err := CopyArtifact(context.Background(), ws, "target/*.jar", dest, os.Stdout)
	if err != nil {
		t.Fatal(err)
	}
	if len(copied) != 1 || copied[0] != "app.jar" {
		t.Errorf("expected [app.jar], got %v", copied)
	}
}

// 已取消的 ctx：在 pattern 之间立即返回 ctx.Err()，不再拷贝后续文件。
func TestCopyArtifact_CancelsBetweenFiles(t *testing.T) {
	ws := t.TempDir()
	writeFile(t, filepath.Join(ws, "a.txt"), "a")
	writeFile(t, filepath.Join(ws, "b.txt"), "b")
	dest := filepath.Join(t.TempDir(), "deploy")

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // 进入 CopyArtifact 前就已取消：第一个 pattern 前就应退出

	copied, err := CopyArtifact(ctx, ws, []string{"a.txt", "b.txt"}, dest, os.Stdout)
	if err != context.Canceled {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
	if len(copied) != 0 {
		t.Errorf("expected no files copied, got %v", copied)
	}
	if _, err := os.Stat(filepath.Join(dest, "a.txt")); !os.IsNotExist(err) {
		t.Error("a.txt should not have been copied after cancel")
	}
}
