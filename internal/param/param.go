// Package param 实现工作项执行期的四套参数（system/env/args/output）与统一插值器。
//
// 引用语法 ${前缀.键}；output 的键为 ${output.<节点名>.<键>}（节点名允许中文）。
// 未定义引用一律报错：静默置空会让 ${env.typo} 变成空命令跑起来，比报错危险得多。
package param

import (
	"fmt"
	"strings"
)

type Set struct {
	System  map[string]string            // 运行期系统参数（trigger/commit/branch/...）
	Env     map[string]string            // 工作项 env: 块（字面量）
	Args    map[string]string            // deployd exec --key=value；webhook 触发为空集
	Outputs map[string]map[string]string // output.<节点名>.<键>
}

func New() *Set {
	return &Set{
		System:  map[string]string{},
		Env:     map[string]string{},
		Args:    map[string]string{},
		Outputs: map[string]map[string]string{},
	}
}

// AddOutput 记录某节点 output: 映射出的一个命名参数。
func (s *Set) AddOutput(stage, key, value string) {
	if s.Outputs[stage] == nil {
		s.Outputs[stage] = map[string]string{}
	}
	s.Outputs[stage][key] = value
}

// Interpolate 展开 str 中的 ${...} 引用。extra 是组件结果变量的裸名表
// （如 ${stdout}），仅在本节点 output: 求值时传入，优先于四套参数。
func (s *Set) Interpolate(str string, extra map[string]string) (string, error) {
	var b strings.Builder
	for i := 0; i < len(str); {
		j := strings.Index(str[i:], "${")
		if j < 0 {
			b.WriteString(str[i:])
			break
		}
		b.WriteString(str[i : i+j])
		k := i + j + 2
		end := strings.Index(str[k:], "}")
		if end < 0 {
			return "", fmt.Errorf("参数引用缺少右花括号: %q", str)
		}
		ref := str[k : k+end]
		v, ok := s.lookup(ref, extra)
		if !ok {
			return "", fmt.Errorf("未定义的参数引用 ${%s}", ref)
		}
		b.WriteString(v)
		i = k + end + 1
	}
	return b.String(), nil
}

func (s *Set) lookup(ref string, extra map[string]string) (string, bool) {
	if v, ok := extra[ref]; ok {
		return v, true
	}
	prefix, rest, _ := strings.Cut(ref, ".")
	switch prefix {
	case "system":
		return s.System[rest], s.System[rest] != "" || keyExists(s.System, rest)
	case "env":
		return s.Env[rest], s.Env[rest] != "" || keyExists(s.Env, rest)
	case "args":
		return s.Args[rest], s.Args[rest] != "" || keyExists(s.Args, rest)
	case "output":
		stage, key, ok := strings.Cut(rest, ".")
		if !ok {
			return "", false
		}
		v, ok := s.Outputs[stage][key]
		return v, ok
	}
	return "", false
}

// keyExists 允许值为空串的已定义参数（lookup 的 map 双返回值在空串时歧义）。
func keyExists(m map[string]string, k string) bool {
	_, ok := m[k]
	return ok
}

// InterpolateParams 对 map 做 params 插值：string 值逐个插值，其他类型
// （列表/数字，如 git 的 branch: ["main"]）原样透传，由组件自己解析。
func (s *Set) InterpolateParams(m map[string]any) (map[string]any, error) {
	out := make(map[string]any, len(m))
	for k, v := range m {
		if str, ok := v.(string); ok {
			nv, err := s.Interpolate(str, nil)
			if err != nil {
				return nil, fmt.Errorf("params.%s: %w", k, err)
			}
			out[k] = nv
			continue
		}
		out[k] = v
	}
	return out, nil
}

// SkipTrue 判断 stage.skip：精确等于 "true" 才跳过（"yes"/"1" 一律不跳过，
// 不做模糊布尔解析）。空串 = 未配置 = 不跳过。引用未定义时报错。
func (s *Set) SkipTrue(raw string) (bool, error) {
	if raw == "" {
		return false, nil
	}
	if raw == "true" {
		return true, nil
	}
	v, err := s.Interpolate(raw, nil)
	if err != nil {
		return false, err
	}
	return v == "true", nil
}
