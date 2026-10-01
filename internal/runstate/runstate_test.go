package runstate

import (
	"testing"
	"time"
)

func TestSaveReadRoundTrip(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	now := time.Now()
	st := &RunState{
		State: "success", Trigger: "manual", PID: 42,
		StartedAt: now, FinishedAt: &now, Commit: "abc1234", Branch: "main",
		Stages: []StageState{{Name: "构建", State: "success", Duration: "3s"}},
	}
	if err := Save("app", st); err != nil {
		t.Fatal(err)
	}
	got, ok := Read("app")
	if !ok || got.State != "success" || got.PID != 42 || got.Stages[0].Name != "构建" {
		t.Fatalf("round trip mismatch: %+v ok=%v", got, ok)
	}
	if _, ok := Read("nonexistent"); ok {
		t.Fatal("不存在的状态文件应返回 false")
	}
}

func TestStaleRecover(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	// running 且锁未被持有（无并发写方）→ 就地改写 failed
	_ = Save("app", &RunState{State: "running", Trigger: "webhook", PID: 1, StartedAt: time.Now()})
	got := StaleRecover("app")
	if got.State != "failed" || got.FailureReason != "执行进程中断" {
		t.Fatalf("陈旧 running 应改写为 failed: %+v", got)
	}
	// 已结束的状态原样返回
	_ = Save("ok", &RunState{State: "success"})
	if got := StaleRecover("ok"); got.State != "success" {
		t.Fatalf("非 running 状态不应被改写: %+v", got)
	}
}

func TestCancelSentinel(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if HasCancel("app") {
		t.Fatal("初始无 sentinel")
	}
	if err := WriteCancel("app"); err != nil {
		t.Fatal(err)
	}
	if !HasCancel("app") {
		t.Fatal("写后应存在")
	}
	if err := ClearCancel("app"); err != nil || HasCancel("app") {
		t.Fatal("清除失败或仍存在")
	}
	// 幂等清除
	if err := ClearCancel("app"); err != nil {
		t.Fatal(err)
	}
}
