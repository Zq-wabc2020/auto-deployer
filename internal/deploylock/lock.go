// Package deploylock provides a per-service exclusive lock used to serialize
// deployments. The lock is implemented with flock(2) on a dedicated lock file
// (~/.deployd/run/<service>.deploy.lock) and is therefore effective across
// processes (daemon vs. forked manual deploy).
package deploylock

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
)

// Lock is an exclusive deploy lock held via flock on a per-service lock file.
type Lock struct {
	f *os.File
}

func lockPath(serviceName string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("get home dir: %w", err)
	}
	dir := filepath.Join(home, ".deployd", "run")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", fmt.Errorf("create lock dir: %w", err)
	}
	return filepath.Join(dir, serviceName+".deploy.lock"), nil
}

func open(serviceName string) (*Lock, error) {
	p, err := lockPath(serviceName)
	if err != nil {
		return nil, err
	}
	f, err := os.OpenFile(p, os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return nil, fmt.Errorf("open deploy lock %s: %w", p, err)
	}
	return &Lock{f: f}, nil
}

// Acquire blocks until the exclusive deploy lock for the service is acquired.
// Used by the webhook queue processor (waits its turn).
func Acquire(serviceName string) (*Lock, error) {
	l, err := open(serviceName)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(l.f.Fd()), syscall.LOCK_EX); err != nil {
		l.f.Close()
		return nil, fmt.Errorf("acquire deploy lock: %w", err)
	}
	return l, nil
}

// TryAcquire tries to acquire the exclusive deploy lock without blocking.
// Returns an error (EWOULDBLOCK) if the lock is already held. Used by manual
// deploys: fail fast with "deploy in progress" instead of waiting.
func TryAcquire(serviceName string) (*Lock, error) {
	l, err := open(serviceName)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(l.f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		l.f.Close()
		return nil, err
	}
	return l, nil
}

// IsHeld 报告该服务的部署锁是否被持有。用 TryAcquire 探测：拿不到=EWOULDBLOCK=被持有；
// 拿到则立即释放并返回 false。用于 status 显示 starting 时确认锁仍在、以及 cancel 命令判定在途。
func IsHeld(serviceName string) bool {
	l, err := TryAcquire(serviceName)
	if err != nil {
		return true // 被持有（或打开失败，保守视为忙）
	}
	l.Release()
	return false
}

// Release unlocks and closes the lock file, releasing the lock. Used by the
// process that holds the lock (webhook queue processor, foreground manual).
func (l *Lock) Release() {
	if l == nil || l.f == nil {
		return
	}
	_ = syscall.Flock(int(l.f.Fd()), syscall.LOCK_UN)
	_ = l.f.Close()
}

// Close closes the underlying file descriptor WITHOUT unlocking. Used by a
// parent process after passing the fd to a forked child via ExtraFiles: flock
// locks are associated with the open file description and shared across
// duplicated fds, so the parent must not unlock (that would release it for the
// child too). The child inherits the lock; it is released when the child exits
// and its inherited fd closes.
func (l *Lock) Close() {
	if l == nil || l.f == nil {
		return
	}
	_ = l.f.Close()
}

// File returns the underlying *os.File, for passing to a child via cmd.ExtraFiles.
func (l *Lock) File() *os.File {
	return l.f
}

// ReleaseInherited releases an inherited deploy lock. The fd number is read
// from the DEPLOYD_LOCK_FD env var (set by the fork parent).
//
// A forked manual deploy child inherits the lock fd from its parent via
// cmd.ExtraFiles. The deployed service ALSO inherits that fd (ExtraFiles clears
// FD_CLOEXEC), so the child cannot rely on its own process exit to release the
// lock -- the long-running service would keep holding it. The child must call
// this after Deploy to explicitly unlock and close the fd. (The service's
// inherited copy becomes a harmless unlocked open fd.)
func ReleaseInherited() error {
	fdStr := os.Getenv("DEPLOYD_LOCK_FD")
	fd, err := strconv.Atoi(fdStr)
	if err != nil || fd <= 0 {
		return fmt.Errorf("DEPLOYD_LOCK_FD invalid (%q)", fdStr)
	}
	if err := syscall.Flock(fd, syscall.LOCK_UN); err != nil {
		_ = syscall.Close(fd)
		return fmt.Errorf("unlock inherited deploy lock: %w", err)
	}
	return syscall.Close(fd)
}
