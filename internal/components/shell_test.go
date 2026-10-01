package components

import (
	"bytes"
	"context"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestShellBasic(t *testing.T) {
	var buf bytes.Buffer
	res, err := Get("shell")
	if err != nil {
		t.Fatal(err)
	}
	out, rerr := res.Run(context.Background(), Request{
		Params: map[string]any{"sh": "echo hello; echo err >&2"},
		Out:    &buf,
	})
	if rerr != nil {
		t.Fatalf("零退出码不应报错: %v", rerr)
	}
	if out["exit_code"] != "0" || out["stdout"] != "hello\n" || out["stderr"] != "err\n" {
		t.Fatalf("results = %#v", out)
	}
	if !strings.Contains(buf.String(), "hello") {
		t.Error("stdout 应同时写入 Out（日志流）")
	}
}

func TestShellMultiline(t *testing.T) {
	c, _ := Get("shell")
	// Jenkins 式多行块：逐行执行，最后一行退出码即整体退出码
	script := "A=1\nif [ $A -eq 1 ]; then\n  echo one\nfi\necho done"
	out, err := c.Run(context.Background(), Request{Params: map[string]any{"sh": script}, Out: &bytes.Buffer{}})
	if err != nil || out["stdout"] != "one\ndone\n" {
		t.Fatalf("out=%#v err=%v", out, err)
	}
}

func TestShellNonZeroExit(t *testing.T) {
	c, _ := Get("shell")
	out, err := c.Run(context.Background(), Request{
		Params: map[string]any{"sh": "echo before-fail; exit 3"},
		Out:    &bytes.Buffer{},
	})
	if err == nil {
		t.Fatal("非零退出码必须报错")
	}
	if out["exit_code"] != "3" {
		t.Fatalf("exit_code = %q, want 3", out["exit_code"])
	}
}

func TestShellCwdAndEnv(t *testing.T) {
	// macOS 上 /var 是 /private/var 的符号链接，sh 的 pwd 输出解析后的物理路径
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	c, _ := Get("shell")
	out, err := c.Run(context.Background(), Request{
		Params: map[string]any{
			"sh": "pwd; echo $MYVAR",
			"cwd": dir,
			"env": map[string]any{"MYVAR": "hello"},
		},
		Out: &bytes.Buffer{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if out["stdout"] != dir+"\nhello\n" {
		t.Fatalf("cwd/env 未生效: %q", out["stdout"])
	}
}

func TestShellStdoutTruncated(t *testing.T) {
	c, _ := Get("shell")
	// 超限输出在 shell 内部生成：1MB+ 字符串作 exec 参数会撞 OS ARG_MAX（macOS 1MB / Linux 单参数 128KB）
	out, err := c.Run(context.Background(), Request{
		Params: map[string]any{"sh": "head -c " + strconv.Itoa(1024*1024+10) + " /dev/zero | tr '\\0' 'x'"},
		Out:    &bytes.Buffer{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(out["stdout"]) != 1024*1024 {
		t.Fatalf("stdout 应截断到 1MB, got %d", len(out["stdout"]))
	}
}

func TestShellCtxCancelKillsGroup(t *testing.T) {
	c, _ := Get("shell")
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(100 * time.Millisecond)
		cancel()
	}()
	start := time.Now()
	_, err := c.Run(ctx, Request{
		Params: map[string]any{"sh": "sleep 30"},
		Out:    &bytes.Buffer{},
	})
	if err == nil || time.Since(start) > 5*time.Second {
		t.Fatalf("ctx 取消必须立即杀掉进程组: err=%v elapsed=%v", err, time.Since(start))
	}
}

func TestShellMissingSh(t *testing.T) {
	c, _ := Get("shell")
	if _, err := c.Run(context.Background(), Request{Params: map[string]any{}, Out: &bytes.Buffer{}}); err == nil {
		t.Fatal("缺 sh 必须报错")
	}
}
