package cmd

import "testing"

// TestCancelNoOpWhenIdle 验证未持锁时 runCancel 仅打印提示并返回 nil（无需取消）。
func TestCancelNoOpWhenIdle(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	// 不持锁 → 提示无需取消，返回 nil
	if err := runCancel("idle-svc"); err != nil {
		t.Fatalf("idle cancel should be a no-op, got %v", err)
	}
}
