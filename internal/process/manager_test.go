package process

import (
	"os"
	"path/filepath"
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
