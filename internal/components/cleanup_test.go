package components

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
)

func makeWs(t *testing.T) string {
	t.Helper()
	ws := t.TempDir()
	for _, f := range []string{"a.txt", "b.log", ".git/config"} {
		p := filepath.Join(ws, f)
		_ = os.MkdirAll(filepath.Dir(p), 0755)
		if err := os.WriteFile(p, []byte("x"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	_ = os.MkdirAll(filepath.Join(ws, "node_modules/pkg"), 0755)
	return ws
}

func TestCleanupDefaultKeepsGit(t *testing.T) {
	ws := makeWs(t)
	c, _ := Get("cleanup")
	out, err := c.Run(context.Background(), Request{
		Params: map[string]any{"workspace": ws}, Out: &bytes.Buffer{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(ws, ".git")); err != nil {
		t.Fatal(".git 缺省必须保留")
	}
	if _, err := os.Stat(filepath.Join(ws, "a.txt")); !os.IsNotExist(err) {
		t.Fatal("a.txt 应被清理")
	}
	if _, err := os.Stat(filepath.Join(ws, "node_modules")); !os.IsNotExist(err) {
		t.Fatal("目录应被清理")
	}
	if out["deleted"] != "3" { // a.txt b.log node_modules 共 3 条；.git 保留不算
		t.Fatalf("deleted = %s, want 3", out["deleted"])
	}
}

func TestCleanupKeepGlob(t *testing.T) {
	ws := makeWs(t)
	c, _ := Get("cleanup")
	_, err := c.Run(context.Background(), Request{
		Params: map[string]any{"workspace": ws, "keep": []any{".git", "*.log"}},
		Out:    &bytes.Buffer{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(ws, "b.log")); err != nil {
		t.Fatal("*.log 应被保留")
	}
	if _, err := os.Stat(filepath.Join(ws, "a.txt")); !os.IsNotExist(err) {
		t.Fatal("a.txt 应被清理")
	}
}

func TestCleanupBadKeepPattern(t *testing.T) {
	ws := makeWs(t)
	c, _ := Get("cleanup")
	_, err := c.Run(context.Background(), Request{
		Params: map[string]any{"workspace": ws, "keep": []any{"["}},
		Out:    &bytes.Buffer{},
	})
	if err == nil {
		t.Fatal("畸形 keep 模式必须报错而非静默误删")
	}
	if _, err := os.Stat(filepath.Join(ws, "a.txt")); err != nil {
		t.Fatal("报错时不得删除任何条目：a.txt 应仍存在")
	}
}

func TestCleanupEmptyDir(t *testing.T) {
	c, _ := Get("cleanup")
	ws := t.TempDir() // 空目录（甚至不是 git 仓库）也不报错
	if _, err := c.Run(context.Background(), Request{
		Params: map[string]any{"workspace": ws}, Out: &bytes.Buffer{},
	}); err != nil {
		t.Fatal(err)
	}
}
