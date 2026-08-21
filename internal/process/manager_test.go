package process

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestManager_StartAndStop(t *testing.T) {
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "test.pid")

	m := NewManager(pidFile)

	err := m.Start("sleep", "300")
	if err != nil {
		t.Fatal(err)
	}

	time.Sleep(100 * time.Millisecond)

	pid, err := m.ReadPID()
	if err != nil {
		t.Fatal(err)
	}
	if pid == 0 {
		t.Fatal("pid should not be 0")
	}

	status := m.Status()
	if status != "running" {
		t.Errorf("expected running, got %s", status)
	}

	err = m.Stop()
	if err != nil {
		t.Fatal(err)
	}

	time.Sleep(100 * time.Millisecond)
	status = m.Status()
	if status != "stopped" {
		t.Errorf("expected stopped, got %s", status)
	}
}

func TestManager_StopNonexistent(t *testing.T) {
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "nonexistent.pid")

	m := NewManager(pidFile)
	err := m.Stop()
	if err != nil {
		t.Fatal(err)
	}
}

func TestManager_PIDFileCleanup(t *testing.T) {
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "cleanup.pid")

	m := NewManager(pidFile)
	_ = m.Start("sleep", "300")
	time.Sleep(100 * time.Millisecond)

	if _, err := os.Stat(pidFile); err != nil {
		t.Fatal("pid file should exist while running")
	}

	_ = m.Stop()
	time.Sleep(100 * time.Millisecond)

	if _, err := os.Stat(pidFile); !os.IsNotExist(err) {
		t.Error("pid file should be removed after stop")
	}
}

// childPids returns the direct children of pid (empty if pgrep unavailable).
func childPids(t *testing.T, pid int) []int {
	t.Helper()
	out, err := exec.Command("pgrep", "-P", strconv.Itoa(pid)).Output()
	if err != nil {
		return nil
	}
	var pids []int
	for _, line := range strings.Fields(string(out)) {
		if p, err := strconv.Atoi(line); err == nil {
			pids = append(pids, p)
		}
	}
	return pids
}

func pidGone(pid int) bool {
	return syscall.Kill(pid, 0) != nil
}

func waitForPidGone(t *testing.T, pid int, timeout time.Duration) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if pidGone(pid) {
			return true
		}
		time.Sleep(50 * time.Millisecond)
	}
	return pidGone(pid)
}

// TestStopKillsWholeProcessGroup reproduces the Ubuntu/dash situation: the
// shell does not exec-replace, so the recorded PID is the `sh` wrapper while
// the real worker is its child. Stop must kill the child too, or every deploy
// leaks one worker process.
func TestStopKillsWholeProcessGroup(t *testing.T) {
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "group.pid")
	m := NewManager(pidFile)
	m.SetOutput(io.Discard)

	// `sleep A; sleep B` forces sh to stay as the parent (it cannot exec a
	// command list), with the first sleep as its live child.
	if err := m.StartShell(dir, "sleep 30; sleep 30", nil, io.Discard); err != nil {
		t.Fatal(err)
	}
	pid, _ := m.ReadPID()
	// The sh wrapper forks its child asynchronously -- poll until it appears.
	var children []int
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		children = childPids(t, pid)
		if len(children) > 0 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if len(children) == 0 {
		t.Fatal("expected the sh wrapper to have a live child process")
	}

	if err := m.Stop(); err != nil {
		t.Fatal(err)
	}

	// The wrapper itself must be gone...
	if !waitForPidGone(t, pid, 3*time.Second) {
		t.Fatalf("wrapper pid %d still alive after Stop", pid)
	}
	// ...AND its child (the real worker) must not be orphaned.
	for _, c := range children {
		if !waitForPidGone(t, c, 3*time.Second) {
			t.Errorf("child pid %d orphaned after Stop (the duplicate-deploy leak)", c)
		}
	}
}

// TestStopEscalatesToSigKILL verifies that a process ignoring SIGTERM is
// killed hard after the stop timeout.
func TestStopEscalatesToSigKILL(t *testing.T) {
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "stubborn.pid")
	m := NewManager(pidFile)
	m.SetOutput(io.Discard)
	m.SetStopTimeout(500 * time.Millisecond)

	// sh ignores SIGTERM and keeps respawning sleeps.
	if err := m.StartShell(dir, `trap "" TERM; while true; do sleep 1; done`, nil, io.Discard); err != nil {
		t.Fatal(err)
	}
	pid, _ := m.ReadPID()

	start := time.Now()
	if err := m.Stop(); err != nil {
		t.Fatal(err)
	}
	if !waitForPidGone(t, pid, 3*time.Second) {
		t.Fatalf("pid %d still alive after Stop with SIGKILL escalation", pid)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("Stop took too long: %v", elapsed)
	}
}
