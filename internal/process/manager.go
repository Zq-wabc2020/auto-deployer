package process

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/auto-deployer/auto-deployer/internal/build"
)

// defaultStopTimeout is how long Stop waits after SIGTERM before escalating
// to SIGKILL. Spring Boot graceful shutdown can take seconds.
const defaultStopTimeout = 10 * time.Second

// Manager manages the lifecycle of a background process using a PID file.
type Manager struct {
	pidFilePath string
	out         io.Writer
	// stopTimeout overrides the SIGTERM->SIGKILL escalation delay (tests).
	stopTimeout time.Duration
}

// NewManager creates a new process manager for the given PID file path.
// Output of Start/Stop lifecycle messages defaults to os.Stdout; use SetOutput
// to redirect (e.g. to a service log file during deployments).
func NewManager(pidFilePath string) *Manager {
	return &Manager{pidFilePath: pidFilePath, out: os.Stdout}
}

// SetOutput redirects Start/Stop lifecycle messages to the given writer.
func (m *Manager) SetOutput(w io.Writer) {
	m.out = w
}

// SetStopTimeout overrides how long Stop waits after SIGTERM before SIGKILL.
func (m *Manager) SetStopTimeout(d time.Duration) {
	m.stopTimeout = d
}

// Start launches a command and records its PID.
func (m *Manager) Start(name string, args ...string) error {
	if existingPID, _ := m.ReadPID(); existingPID > 0 {
		return fmt.Errorf("process already running with pid %d", existingPID)
	}

	cmd := exec.Command(name, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start process: %w", err)
	}
	// Reap the child when it exits so it does not linger as a zombie (which
	// would make liveness checks report it forever).
	go func() { _ = cmd.Wait() }()

	if err := m.WritePID(cmd.Process.Pid); err != nil {
		return err
	}

	fmt.Fprintf(m.out, "started %s with pid %d\n", name, cmd.Process.Pid)
	return nil
}

// StartShell starts `sh -c command` in dir (empty = current dir) with extra
// env overrides applied, in its own process group, and records the real PID:
// for a single-command string the shell execs it, so the recorded PID is the
// server process itself and Stop can signal it directly. Shared by every
// PID-model plugin (jvm/node/python).
//
// NOTE: on shells that do NOT exec-replace (dash, the default /bin/sh on
// Ubuntu), the recorded PID is the sh wrapper and the real server is its
// child. Stop therefore signals the whole process GROUP, which covers both
// cases -- see Stop.
func (m *Manager) StartShell(dir, command string, envOverrides map[string]string, out io.Writer) error {
	if m.Status() == "running" {
		pid, _ := m.ReadPID()
		return fmt.Errorf("process already running with pid %d", pid)
	}

	cmd := exec.Command("sh", "-c", command)
	if dir != "" {
		cmd.Dir = dir
	}
	// App runtime stdout/stderr discarded (log approach A): the service log
	// holds deploy pipeline logs only; the app logs to its own sink.
	cmd.Stdout = nil
	cmd.Stderr = nil
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if len(envOverrides) > 0 {
		cmd.Env = build.MergeEnv(os.Environ(), envOverrides)
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start process: %w", err)
	}
	// Reap the wrapper when it exits (avoids zombies under the daemon).
	go func() { _ = cmd.Wait() }()

	if err := m.WritePID(cmd.Process.Pid); err != nil {
		return err
	}
	fmt.Fprintf(out, "started with pid %d\n", cmd.Process.Pid)
	return nil
}

// Stop terminates the managed process GROUP: SIGTERM, wait for exit, then
// SIGKILL. Killing the whole group is essential because the launcher is
// `sh -c <command>` and shells differ: bash (RHEL/ALinux, macOS) exec-replaces
// itself so the PID is the server, but dash (Ubuntu's /bin/sh) keeps the
// wrapper as parent -- signalling only the PID would orphan the real server,
// leaking one process per deploy. Waiting for exit prevents the new process
// from racing the old one's graceful shutdown (port conflicts).
func (m *Manager) Stop() error {
	pid, err := m.ReadPID()
	if err != nil || pid == 0 {
		return nil
	}

	timeout := m.stopTimeout
	if timeout <= 0 {
		timeout = defaultStopTimeout
	}

	if !signalPid(pid, syscall.SIGTERM) {
		// Already gone.
		_ = m.CleanupPID()
		return nil
	}
	if waitPidGone(pid, timeout) {
		_ = m.CleanupPID()
		fmt.Fprintf(m.out, "stopped process %d\n", pid)
		return nil
	}

	// Graceful shutdown timed out -- escalate.
	_ = signalPid(pid, syscall.SIGKILL)
	if waitPidGone(pid, 3*time.Second) {
		_ = m.CleanupPID()
		fmt.Fprintf(m.out, "stopped process %d (SIGKILL after timeout)\n", pid)
		return nil
	}
	return fmt.Errorf("process %d did not exit after SIGKILL", pid)
}

// Alive 报告被管理进程（及其进程组）是否仍存活。供就绪轮询快速失败用。
func (m *Manager) Alive() bool {
	pid, err := m.ReadPID()
	if err != nil || pid == 0 {
		return false
	}
	return pidAlive(pid)
}

// signalPid sends sig to the process group of pid (our launched processes use
// Setpgid, so pgid == pid), falling back to the bare pid for processes started
// without a dedicated group (e.g. by older deployd binaries).
func signalPid(pid int, sig syscall.Signal) bool {
	if err := syscall.Kill(-pid, sig); err == nil {
		return true
	}
	return syscall.Kill(pid, sig) == nil
}

// pidAlive reports whether the pid or its process group still exists. The
// group check catches the case where the sh wrapper died but its children
// (the real server) are still running.
func pidAlive(pid int) bool {
	if syscall.Kill(-pid, 0) == nil {
		return true
	}
	return syscall.Kill(pid, 0) == nil
}

// waitPidGone polls until the process (group) no longer exists or the timeout
// elapses. Returns true when it is gone.
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

// Status returns "running", "stopped", or "unknown".
func (m *Manager) Status() string {
	pid, err := m.ReadPID()
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
		_ = m.CleanupPID()
		return "stopped"
	}
	return "unknown"
}

// ReadPID reads the PID from the PID file.
func (m *Manager) ReadPID() (int, error) {
	data, err := os.ReadFile(m.pidFilePath)
	if err != nil {
		return 0, err
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	return pid, err
}

// WritePID writes a PID to the PID file.
func (m *Manager) WritePID(pid int) error {
	if err := os.MkdirAll(filepath.Dir(m.pidFilePath), 0755); err != nil {
		return err
	}
	return os.WriteFile(m.pidFilePath, []byte(strconv.Itoa(pid)), 0644)
}

// CleanupPID removes the PID file.
func (m *Manager) CleanupPID() error {
	return os.Remove(m.pidFilePath)
}
