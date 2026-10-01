// Package runstate 持久化每个工作项最近一次流水线执行的状态
// （~/.deployd/run/<name>.status），并提供取消 sentinel（<name>.cancel）。
//
// 只有执行进程调用 Save；唯一例外是 StaleRecover：状态为 running 但部署锁
// 未被持有说明执行进程已死，读取方（status）就地改写为 failed——锁空保证
// 不存在并发写方（沿用旧 servstate 的陈旧恢复原则）。
package runstate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/auto-deployer/auto-deployer/internal/deploylock"
)

type StageState struct {
	Name     string `json:"name"`
	State    string `json:"state"`              // success | skipped | failed
	Duration string `json:"duration,omitempty"` // 如 "3s"
}

type RunState struct {
	State         string       `json:"state"` // running | success | failed | cancelled
	Trigger       string       `json:"trigger"`
	PID           int          `json:"pid,omitempty"`
	StartedAt     time.Time    `json:"started_at"`
	FinishedAt    *time.Time   `json:"finished_at,omitempty"`
	Commit        string       `json:"commit,omitempty"`
	Branch        string       `json:"branch,omitempty"`
	FailedStage   string       `json:"failed_stage,omitempty"`
	FailureReason string       `json:"failure_reason,omitempty"`
	Stages        []StageState `json:"stages"`
}

func runDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".deployd", "run")
}

func Path(name string) string { return filepath.Join(runDir(), name+".status") }

func CancelPath(name string) string { return filepath.Join(runDir(), name+".cancel") }

func Save(name string, st *RunState) error {
	if err := os.MkdirAll(runDir(), 0755); err != nil {
		return err
	}
	data, _ := json.Marshal(st)
	return os.WriteFile(Path(name), data, 0644)
}

func Read(name string) (RunState, bool) {
	data, err := os.ReadFile(Path(name))
	if err != nil {
		return RunState{}, false
	}
	var st RunState
	if json.Unmarshal(data, &st) != nil {
		return RunState{}, false // 半写/损坏 → 视为未执行
	}
	return st, true
}

// StaleRecover 见包注释。锁探测失败（IsHeld 保守返回 true）时不改写。
func StaleRecover(name string) RunState {
	st, ok := Read(name)
	if !ok {
		return RunState{}
	}
	if st.State == "running" && !deploylock.IsHeld(name) {
		now := time.Now()
		st.State = "failed"
		st.FinishedAt = &now
		st.FailureReason = "执行进程中断"
		_ = Save(name, &st)
	}
	return st
}

func WriteCancel(name string) error {
	if err := os.MkdirAll(runDir(), 0755); err != nil {
		return err
	}
	return os.WriteFile(CancelPath(name), []byte{}, 0644)
}

func HasCancel(name string) bool {
	_, err := os.Stat(CancelPath(name))
	return err == nil
}

func ClearCancel(name string) error {
	if err := os.Remove(CancelPath(name)); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
