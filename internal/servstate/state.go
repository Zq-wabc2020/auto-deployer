// internal/servstate/state.go
package servstate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// State 服务状态持久化结构。Status 仅取 "starting" | "start_failed"。
type State struct {
	Status string    `json:"status"`            // 仅 "starting" | "start_failed"
	Since  time.Time `json:"since"`             // 状态写入时间
	Stage  string    `json:"stage,omitempty"`   // starting 时的子阶段（build/fetch 等）
}

// runDir 返回状态文件目录 ~/.deployd/run。
func runDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".deployd", "run")
}

// Path 返回服务状态文件路径 ~/.deployd/run/<name>.state。
func Path(name string) string {
	return filepath.Join(runDir(), name+".state")
}

// CancelPath 返回取消 sentinel 文件路径 ~/.deployd/run/<name>.cancel。
func CancelPath(name string) string {
	return filepath.Join(runDir(), name+".cancel")
}

// write 序列化并写入状态文件。
func write(name string, s State) error {
	if err := os.MkdirAll(runDir(), 0755); err != nil {
		return err
	}
	data, _ := json.Marshal(s)
	return os.WriteFile(Path(name), data, 0644)
}

// WriteStarting 写入 starting 状态（含子阶段）。
func WriteStarting(name, stage string) error {
	return write(name, State{Status: "starting", Since: time.Now(), Stage: stage})
}

// WriteFailed 写入 start_failed 状态。
func WriteFailed(name string) error {
	return write(name, State{Status: "start_failed", Since: time.Now()})
}

// Clear 删除状态文件，文件不存在视为成功。
func Clear(name string) error {
	if err := os.Remove(Path(name)); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// Read 读取状态文件；文件不存在或解析失败均返回 (State{}, false)，
// 调用方应降级为实时探测。
func Read(name string) (State, bool) {
	data, err := os.ReadFile(Path(name))
	if err != nil {
		return State{}, false
	}
	var s State
	if err := json.Unmarshal(data, &s); err != nil {
		// 半写/损坏 → 降级为无状态（走实时探测）
		return State{}, false
	}
	return s, true
}

// WriteCancel 写入取消 sentinel 文件。
func WriteCancel(name string) error {
	if err := os.MkdirAll(runDir(), 0755); err != nil {
		return err
	}
	return os.WriteFile(CancelPath(name), []byte{}, 0644)
}

// ClearCancel 删除取消 sentinel 文件，文件不存在视为成功。
func ClearCancel(name string) error {
	if err := os.Remove(CancelPath(name)); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// HasCancel 判断取消 sentinel 是否存在。
func HasCancel(name string) bool {
	_, err := os.Stat(CancelPath(name))
	return err == nil
}
