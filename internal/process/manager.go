package process

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// Manager manages the lifecycle of a background process using a PID file.
type Manager struct {
	pidFilePath string
}

// NewManager creates a new process manager for the given PID file path.
func NewManager(pidFilePath string) *Manager {
	return &Manager{pidFilePath: pidFilePath}
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

	fmt.Printf("started %s with pid %d\n", name, cmd.Process.Pid)
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
	fmt.Printf("stopped process %d\n", pid)
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
