package daemon

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// defaultStopTimeout is how long Stop waits after SIGTERM before escalating
// to SIGKILL.
const defaultStopTimeout = 10 * time.Second

// PIDFile 管理单个进程的 PID 文件（daemon 用：~/.deployd/run/deployd.pid）。
// 从旧 internal/process.Manager 精简移植：只管 daemon 自己的 pid，无进程组
// 语义（daemon 进程由 start 命令 setsid fork，不属于被管理的进程组）。
type PIDFile struct {
	path string
}

// NewPIDFile 创建一个 PIDFile。
func NewPIDFile(path string) *PIDFile {
	return &PIDFile{path: path}
}

// Status 返回 "running"/"stopped"/"unknown"：无 pid 文件或进程已死 → stopped，
// pid 文件可读但无法探测 → unknown。
func (p *PIDFile) Status() string {
	pid, err := p.Read()
	if err != nil || pid == 0 {
		return "stopped"
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return "unknown"
	}
	err = proc.Signal(syscall.Signal(0))
	if err == nil {
		return "running"
	}
	if err == syscall.ESRCH {
		_ = p.Cleanup()
		return "stopped"
	}
	return "unknown"
}

// Write 写入 pid 到文件（自动创建目录）。
func (p *PIDFile) Write(pid int) error {
	if err := os.MkdirAll(filepath.Dir(p.path), 0755); err != nil {
		return err
	}
	return os.WriteFile(p.path, []byte(strconv.Itoa(pid)), 0644)
}

// Read 读取 pid 文件中的 pid。
func (p *PIDFile) Read() (int, error) {
	data, err := os.ReadFile(p.path)
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(string(data)))
}

// Cleanup 删除 pid 文件。
func (p *PIDFile) Cleanup() error {
	return os.Remove(p.path)
}

// Stop 终止 pid 对应进程：SIGTERM → 等 10s → SIGKILL。进程已死则直接清理
// pid 文件，不报错。从旧 process.Manager.Stop 精简移植。
func (p *PIDFile) Stop() error {
	pid, err := p.Read()
	if err != nil || pid == 0 {
		return nil
	}

	if !signalPid(pid, syscall.SIGTERM) {
		// Already gone.
		_ = p.Cleanup()
		return nil
	}
	if waitPidGone(pid, defaultStopTimeout) {
		_ = p.Cleanup()
		return nil
	}

	// Graceful shutdown timed out -- escalate.
	_ = signalPid(pid, syscall.SIGKILL)
	if waitPidGone(pid, 3*time.Second) {
		_ = p.Cleanup()
		return nil
	}
	return fmt.Errorf("process %d did not exit after SIGKILL", pid)
}

// signalPid 先发信号给 pid 的进程组（旧系统启动的进程用 Setpgid，pgid==pid），
// 失败再回退到单进程；对不存在/无权限的 pid 返回 false。
func signalPid(pid int, sig syscall.Signal) bool {
	if err := syscall.Kill(-pid, sig); err == nil {
		return true
	}
	return syscall.Kill(pid, sig) == nil
}

// pidAlive 报告 pid（或它的进程组）是否仍存在。
func pidAlive(pid int) bool {
	if syscall.Kill(-pid, 0) == nil {
		return true
	}
	return syscall.Kill(pid, 0) == nil
}

// waitPidGone 每 100ms 探测一次直到进程消失或超时；进程消失返回 true。
func waitPidGone(pid int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for {
		if !pidAlive(pid) {
			return true
		}
		if !time.Now().Before(deadline) {
			return false
		}
		time.Sleep(100 * time.Millisecond)
	}
}
