package daemon

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestPIDFileLifecycle(t *testing.T) {
	p := NewPIDFile(filepath.Join(t.TempDir(), "d.pid"))
	if p.Status() != "stopped" {
		t.Fatalf("无 pid 文件应为 stopped: %s", p.Status())
	}
	if err := p.Write(os.Getpid()); err != nil {
		t.Fatal(err)
	}
	if p.Status() != "running" {
		t.Fatalf("写当前进程 pid 应为 running: %s", p.Status())
	}
	// 停一个不存在的进程组：写入一个必然不存在的 pid 后 Stop 不应挂死
	_ = os.WriteFile(p.path, []byte("999999"), 0644)
	done := make(chan error, 1)
	go func() { done <- p.Stop() }()
	select {
	case <-done:
	case <-time.After(15 * time.Second):
		_ = syscall.Kill(os.Getpid(), syscall.SIGKILL)
		t.Fatal("Stop 对已死 pid 不应挂死")
	}
}
