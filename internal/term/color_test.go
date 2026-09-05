// internal/term/color_test.go
package term

import (
	"os"
	"testing"
)

func TestColorizeKnownStatus(t *testing.T) {
	// 强制走 TTY 分支以验证上色（默认按 os.Stdout 判定）
	defaultIsTTY = func() bool { return true }
	defer func() { defaultIsTTY = isStdoutTTY }()
	for _, c := range []struct{ status, want string }{
		{"starting", "\033[34mstarting\033[0m"},   // 蓝
		{"running", "\033[32mrunning\033[0m"},     // 绿
		{"stopped", "\033[90mstopped\033[0m"},      // 灰
		{"start_failed", "\033[31mstart_failed\033[0m"}, // 红
		{"unknown", "\033[33munknown\033[0m"},      // 暗黄
	} {
		if got := Colorize(c.status); got != c.want {
			t.Errorf("Colorize(%q)=%q want %q", c.status, got, c.want)
		}
	}
}

func TestColorizeNonTTYStripsANSI(t *testing.T) {
	defaultIsTTY = func() bool { return false }
	defer func() { defaultIsTTY = isStdoutTTY }()
	if got := Colorize("running"); got != "running" {
		t.Errorf("non-TTY must return plain text, got %q", got)
	}
}

func TestIsTerminalRegularFile(t *testing.T) {
	f, _ := os.CreateTemp("", "notatty")
	defer os.Remove(f.Name())
	defer f.Close()
	if IsTerminal(f) {
		t.Error("regular file should not be detected as terminal")
	}
}
