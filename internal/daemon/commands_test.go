package daemon

import (
	"bytes"
	"strings"
	"testing"
)

// TestLastRunSection 验证 logs 的「最近一次 run 分节」截取逻辑：
// Printf 行带 "[<name>] <ts> " 前缀，run 开始头内容以 "=== 2" 开头（开始时间戳年份 2xxx），
// run 结束头以工作项名开头。应命中最后一次 run 开始头并包含其分节主体。
func TestLastRunSection(t *testing.T) {
	log := strings.Join([]string{
		"[app] 2026-10-01 22:50:00 === 2026-10-01 22:50:00 app [manual  ] ===",
		"[app] 2026-10-01 22:50:01 构建",
		"[app] 2026-10-01 22:50:05 === app success 耗时 5s ===",
		"[app] 2026-10-01 22:51:00 === 2026-10-01 22:51:00 app [webhook main abc1234] ===",
		"[app] 2026-10-01 22:51:01 部署",
		"[app] 2026-10-01 22:51:10 === app failed 耗时 10s ===",
	}, "\n")

	got := lastRunSection([]byte(log))
	// 应只包含第二次 run：以第二次 run 开始头开头
	if !bytes.HasPrefix(got, []byte("[app] 2026-10-01 22:51:00 === 2026-10-01 22:51:00 app [webhook")) {
		t.Fatalf("lastRunSection 未命中最后一次 run 开始头，got:\n%s", got)
	}
	if bytes.Contains(got, []byte("22:50:01 构建")) {
		t.Fatalf("lastRunSection 不应包含上一次 run 的内容，got:\n%s", got)
	}
	if !bytes.Contains(got, []byte("22:51:01 部署")) || !bytes.Contains(got, []byte("=== app failed 耗时 10s ===")) {
		t.Fatalf("lastRunSection 应包含最后一次 run 的主体与结束头，got:\n%s", got)
	}
}

// TestLastRunSectionOldFormat 无分节头的旧格式日志应原样返回整个文件。
func TestLastRunSectionOldFormat(t *testing.T) {
	log := "[app] 2026-10-01 22:50:00 deploying\n[app] 2026-10-01 22:50:01 done\n"
	got := lastRunSection([]byte(log))
	if string(got) != log {
		t.Fatalf("旧格式日志应原样返回，got:\n%s", got)
	}
}
