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

	"github.com/auto-deployer/auto-deployer/internal/build"
)

// Manager manages the lifecycle of a background process using a PID file.
type Manager struct {
	pidFilePath string
	out         io.Writer
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
	if err := m.WritePID(cmd.Process.Pid); err != nil {
		return err
	}
	fmt.Fprintf(out, "started with pid %d\n", cmd.Process.Pid)
	return nil
}

// Stop terminates the managed process by sending SIGTERM.
func (m *Manager) Stop() error {
	pid, err := m.ReadPID()
	if err != nil || pid == 0 {
		return nil
	}

	proc, err := os.FindProcess(pid)
	if err != nil {
		return nil
	}

	if err := proc.Signal(syscall.SIGTERM); err != nil {
		if err == syscall.ESRCH {
			_ = m.CleanupPID()
			return nil
		}
		return fmt.Errorf("failed to send SIGTERM: %w", err)
	}

	_ = m.CleanupPID()
	fmt.Fprintf(m.out, "stopped process %d\n", pid)
	return nil
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
