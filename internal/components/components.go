// Package components 是流水线组件的注册表。每个组件实现一个 Run：
// 拿插值后的 params 执行，返回结果变量（裸名，只能在本节点 output: 里引用）
// 和 error（非 nil = 节点失败）。各组件文件 init() 自注册 type 名。
package components

import (
	"context"
	"fmt"
	"io"

	"github.com/auto-deployer/auto-deployer/internal/notify"
)

type Results map[string]string

type Request struct {
	Params map[string]any
	// System 是工作项系统参数快照（name/trigger/branch/commit/author/message/
	// result/failed_stage/error），email 组件的内置标准模板用。只读。
	System   map[string]string
	Out      io.Writer        // 节点日志流（引擎接好：日志文件 ± 终端）
	Notifier *notify.Notifier // email 组件用；nil = 未配置 SMTP/Resend
}

type Component interface {
	Run(ctx context.Context, req Request) (Results, error)
}

var registry = map[string]Component{}

func Register(name string, c Component) { registry[name] = c }

func Get(name string) (Component, error) {
	c, ok := registry[name]
	if !ok {
		return nil, fmt.Errorf("未知组件类型 %q (已注册: %v)", name, names())
	}
	return c, nil
}

func Known() map[string]bool {
	m := make(map[string]bool, len(registry))
	for k := range registry {
		m[k] = true
	}
	return m
}

func names() []string {
	var out []string
	for k := range registry {
		out = append(out, k)
	}
	return out
}

// asString 取 string 参数；缺失或类型不符返回 ""（组件自己决定是否必填）。
func asString(params map[string]any, key string) string {
	s, _ := params[key].(string)
	return s
}

// asStringList 取列表参数，兼容 YAML 单字符串与列表两种写法。
func asStringList(params map[string]any, key string) []string {
	switch v := params[key].(type) {
	case string:
		return []string{v}
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}
