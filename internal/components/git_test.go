package components

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// newBareRepo 建一个本地裸仓库并提交一个 commit，返回其路径。
func newBareRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	bare := filepath.Join(dir, "repo.git")
	seed := filepath.Join(dir, "seed")
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		cmd.Dir = seed
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	_ = exec.Command("git", "init", "--bare", bare).Run()
	_ = exec.Command("git", "clone", bare, seed).Run()
	run("config", "user.email", "t@t")
	run("config", "user.name", "t")
	_ = os.WriteFile(filepath.Join(seed, "f.txt"), []byte("v1"), 0644)
	run("add", ".")
	run("commit", "-m", "c1")
	run("push", "origin", "HEAD:refs/heads/main")
	return bare
}

func TestGitCloneAndChanged(t *testing.T) {
	bare := newBareRepo(t)
	ws := t.TempDir()
	c, _ := Get("git")
	out, err := c.Run(context.Background(), Request{
		Params: map[string]any{"url": bare, "branch": "main", "workspace": ws},
		Out:    &bytes.Buffer{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if out["changed"] != "true" || out["branch"] != "main" || out["commit"] == "" {
		t.Fatalf("首次克隆: %#v", out)
	}
	if _, err := os.Stat(filepath.Join(ws, "f.txt")); err != nil {
		t.Error("工作空间应有检出文件")
	}
	// 无新提交再跑：changed=false（fetch 快路径）
	out2, err := c.Run(context.Background(), Request{
		Params: map[string]any{"url": bare, "branch": "main", "workspace": ws},
		Out:    &bytes.Buffer{},
	})
	if err != nil || out2["changed"] != "false" {
		t.Fatalf("无新提交: %#v err=%v", out2, err)
	}
}

func TestGitMissingParams(t *testing.T) {
	c, _ := Get("git")
	if _, err := c.Run(context.Background(), Request{Params: map[string]any{}, Out: &bytes.Buffer{}}); err == nil {
		t.Fatal("缺 url/branch/workspace 必须报错")
	}
}

func TestGitBadURL(t *testing.T) {
	c, _ := Get("git")
	_, err := c.Run(context.Background(), Request{
		Params: map[string]any{"url": "/nonexistent/repo.git", "branch": "main", "workspace": t.TempDir()},
		Out:    &bytes.Buffer{},
	})
	if err == nil {
		t.Fatal("克隆失败必须报错")
	}
}
