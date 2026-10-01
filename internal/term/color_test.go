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
		{"running", "\033[34mrunning\033[0m"},     // 蓝
		{"success", "\033[32msuccess\033[0m"},     // 绿
		{"failed", "\033[31mfailed\033[0m"},       // 红
		{"cancelled", "\033[33mcancelled\033[0m"}, // 黄
		{"unknown", "\033[33munknown\033[0m"},     // 黄
		{"never", "\033[90mnever\033[0m"},         // 灰
		{"stopped", "\033[90mstopped\033[0m"},     // 灰（旧服务态，回退默认）
	} {
		if got := Colorize(c.status); got != c.want {
			t.Errorf("Colorize(%q)=%q want %q", c.status, got, c.want)
		}
	}
}

func TestColorizeNonTTYStripsANSI(t *testing.T) {
	defaultIsTTY = func() bool { return false }
	defer func() { defaultIsTTY = isStdoutTTY }()
	for _, status := range []string{"running", "success"} {
		if got := Colorize(status); got != status {
			t.Errorf("non-TTY must return plain text, got %q", got)
		}
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
