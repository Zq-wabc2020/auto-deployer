package components

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
)

func init() { Register("cleanup", Cleanup{}) }

// Cleanup 删除 workspace 下不在 keep glob 匹配内的一切（缺省只保 .git，
// 下轮 git fetch 才能走快路径）。用于 when: always 的收尾清理节点。
type Cleanup struct{}

func (Cleanup) Run(ctx context.Context, req Request) (Results, error) {
	ws := asString(req.Params, "workspace")
	if ws == "" {
		return nil, fmt.Errorf("cleanup 组件缺少 workspace 参数（引擎默认注入 ${system.workspace}）")
	}
	keep := asStringList(req.Params, "keep")
	if len(keep) == 0 {
		keep = []string{".git"}
	}
	entries, err := os.ReadDir(ws)
	if err != nil {
		if os.IsNotExist(err) {
			return Results{"deleted": "0"}, nil
		}
		return nil, err
	}
	deleted := 0
	for _, e := range entries {
		if matchAny(e.Name(), keep) {
			continue
		}
		if err := os.RemoveAll(filepath.Join(ws, e.Name())); err != nil {
			return nil, fmt.Errorf("清理 %s 失败: %w", e.Name(), err)
		}
		deleted++
	}
	return Results{"deleted": strconv.Itoa(deleted)}, nil
}

func matchAny(name string, patterns []string) bool {
	for _, p := range patterns {
		if ok, _ := filepath.Match(p, name); ok {
			return true
		}
	}
	return false
}
