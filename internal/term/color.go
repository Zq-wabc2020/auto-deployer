// internal/term/color.go
package term

import (
	"os"
)

const (
	ansiReset = "\033[0m"
	blue      = "\033[34m"
	green     = "\033[32m"
	gray      = "\033[90m"
	red       = "\033[31m"
	yellow    = "\033[33m"
)

func colorFor(status string) string {
	switch status {
	case "running":
		return blue
	case "success":
		return green
	case "failed":
		return red
	case "cancelled", "unknown":
		return yellow
	default: // never / stopped / 其他
		return gray
	}
}

// defaultIsTTY 可被测试覆盖；默认探测 os.Stdout。
var defaultIsTTY = isStdoutTTY

func isStdoutTTY() bool { return IsTerminal(os.Stdout) }

// Colorize 按状态上色；stdout 非 TTY（管道/重定向）时返回纯文本。
func Colorize(status string) string {
	if !defaultIsTTY() {
		return status
	}
	return colorFor(status) + status + ansiReset
}

// IsTerminal 报告 f 是否为终端字符设备。仅用标准库，不引入 x/term。
func IsTerminal(f *os.File) bool {
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	return (fi.Mode() & os.ModeCharDevice) != 0
}
