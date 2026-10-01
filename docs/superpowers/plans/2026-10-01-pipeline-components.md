# 工作项流水线 + 组件化重构 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 将 deployd 从「服务类型插件」重构为「工作项 = 任意节点流水线 + 四种组件（git/shell/email/cleanup）+ 四套参数」，deployd 不再管理服务进程。

**Architecture:** 新增 `internal/param`（参数插值）、`internal/components`（组件注册表 + 4 组件）、`internal/runstate`（执行状态文件）、`internal/pipeline`（执行引擎：fail-fast 主流程 + when 后置流程）。`internal/webhook`/`deployqueue`/`daemon` 切换到 pipelines；删除 `plugins`/`registry`/`process`/`servstate`/`deploy`，`internal/build` 缩为 git/ssh/exec 工具库（spec 写「删除 internal/build」，此处偏差：git Fetch/SSH key/RunCommandCtx 被组件复用，保留该包但删掉 artifact/java 部分）。

**Tech Stack:** Go（现有依赖 cobra、yaml.v3——**不新增任何依赖**）。测试用标准库 `testing`。

**Spec:** `docs/superpowers/specs/2026-10-01-pipeline-components-design.md`（执行者必读，参数/组件/when 语义以 spec 为准）

## Global Constraints

- 分支 `feat/pipeline-components`（已存在，基于 main@c60f92f），所有提交落在此分支
- 不新增任何第三方依赖；只用标准库 + cobra + yaml.v3
- 注释风格：中文、解释「为什么」；匹配现有代码风格（见 internal/deploylock/lock.go）
- 每个任务结束时 `go build ./... && go test ./...` 必须全绿
- 状态目录 `~/.deployd/run/`，日志 `~/.deployd/services/<name>.log`（沿用现有布局）
- 锁文件名沿用 `~/.deployd/run/<name>.deploy.lock`（deploylock 包不改）
- 组件名只有 4 个：`git`、`shell`、`email`、`cleanup`
- when 枚举：`""`（主流程）/ `failure` / `always`
- 参数引用语法：`${system.*}` `${env.*}` `${args.*}` `${output.<节点名>.<键>}`；未定义引用必须报错（禁止静默置空）
- 大结果截断上限 1MB（`stdout`/`stderr` 进参数集时）

## Review Focus

执行者注意：以下 5 类输入 spec 有语义但容易被实现歪，对应测试已分别钉在 owning task 里，改实现时不得删这些测试：

1. **未定义引用 `${env.typo}`** → 节点启动时报错失败，绝不静默当空串（Task 1 + Task 8）
2. **总预算耗尽/节点超时导致失败后，`when: always` 清理节点仍要执行**（后置走独立父 ctx + 10m 上限）（Task 8）
3. **取消（cancel/Ctrl+C）后：`always` 节点执行、`failure` 节点不执行、状态=cancelled 而非 failed**（Task 8）
4. **skip 求值：插值结果精确等于 `"true"` 才跳过**，`"yes"`/`"1"`/空 都不跳过（Task 1）
5. **stdout 超 1MB 截断**，防止构建日志撑爆参数集（Task 3）

---

### Task 1: internal/param — 四套参数集与插值器

**Files:**
- Create: `internal/param/param.go`
- Test: `internal/param/param_test.go`

**Interfaces:**
- Consumes: 无（最底层包）
- Produces（后续所有任务依赖，签名必须一字不差）:
  - `type Set struct { System, Env, Args map[string]string; Outputs map[string]map[string]string }`
  - `func New() *Set`
  - `func (s *Set) Interpolate(str string, extra map[string]string) (string, error)`
  - `func (s *Set) InterpolateParams(m map[string]any) (map[string]any, error)` — 只插值 string 值，其他类型原样透传（git 的 branch 列表）
  - `func (s *Set) AddOutput(stage, key, value string)`
  - `func (s *Set) SkipTrue(raw string) (bool, error)` — raw=="" → false 不跳过；=="true" → true；否则插值后精确 =="true" 才 true

- [ ] **Step 1: 写失败测试**

```go
package param

import "testing"

func newTestSet() *Set {
	s := New()
	s.System = map[string]string{"name": "app", "branch": "main"}
	s.Env = map[string]string{"skipBuild": "true", "nope": "yes"}
	s.Args = map[string]string{"env": "prod"}
	s.AddOutput("拉取代码", "v", "abc123")
	return s
}

func TestInterpolate(t *testing.T) {
	s := newTestSet()
	cases := []struct{ in, want string }{
		{"${system.name}", "app"},
		{"deploy ${system.name} to ${args.env}", "deploy app to prod"},
		{"${output.拉取代码.v}", "abc123"}, // 节点名允许中文
		{"no refs here", "no refs here"},
		{"", ""},
	}
	for _, c := range cases {
		got, err := s.Interpolate(c.in, nil)
		if err != nil || got != c.want {
			t.Errorf("Interpolate(%q) = %q, %v; want %q", c.in, got, err, c.want)
		}
	}
}

func TestInterpolateUndefinedRef(t *testing.T) {
	s := newTestSet()
	if _, err := s.Interpolate("${env.typo}", nil); err == nil {
		t.Fatal("未定义引用必须报错，不能静默置空")
	}
	if _, err := s.Interpolate("${output.不存在.x}", nil); err == nil {
		t.Fatal("未定义的 output 引用必须报错")
	}
	if _, err := s.Interpolate("${badprefix.x}", nil); err == nil {
		t.Fatal("未知前缀必须报错")
	}
}

func TestInterpolateExtra(t *testing.T) {
	s := newTestSet()
	// extra（组件结果变量）优先于四套参数；裸名只在 extra 里找
	got, err := s.Interpolate("v=${stdout}", map[string]string{"stdout": "hello"})
	if err != nil || got != "v=hello" {
		t.Errorf("got %q, %v", got, err)
	}
}

func TestInterpolateParams(t *testing.T) {
	s := newTestSet()
	in := map[string]any{"sh": "echo ${system.name}", "n": 42, "branch": []any{"main"}}
	out, err := s.InterpolateParams(in)
	if err != nil {
		t.Fatal(err)
	}
	if out["sh"] != "echo app" || out["n"] != 42 {
		t.Errorf("got %#v", out)
	}
	if _, ok := out["branch"].([]any); !ok {
		t.Error("非 string 值必须原样透传")
	}
}

func TestSkipTrue(t *testing.T) {
	s := newTestSet()
	cases := []struct {
		raw    string
		want   bool
		wantErr bool
	}{
		{"", false, false},
		{"true", true, false},           // 字面量
		{"${env.skipBuild}", true, false}, // 插值 = "true"
		{"${env.nope}", false, false},   // "yes" 不等于 "true" → 不跳过
		{"${env.typo}", false, true},    // 未定义引用 → 错误
	}
	for _, c := range cases {
		got, err := s.SkipTrue(c.raw)
		if (err != nil) != c.wantErr || got != c.want {
			t.Errorf("SkipTrue(%q) = %v, %v; want %v, err=%v", c.raw, got, err, c.want, c.wantErr)
		}
	}
}
```

- [ ] **Step 2: 运行确认失败**

Run: `go test ./internal/param/`
Expected: FAIL（package 不存在）

- [ ] **Step 3: 实现 param.go**

```go
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
```

- [ ] **Step 4: 运行测试通过**

Run: `go test ./internal/param/ -v`
Expected: PASS 全绿

- [ ] **Step 5: Commit**

```bash
git add internal/param/
git commit -m "feat(param): 四套参数集与统一插值器，未定义引用报错"
```

---

### Task 2: internal/runstate — 执行状态文件 + 取消 sentinel

**Files:**
- Create: `internal/runstate/runstate.go`
- Test: `internal/runstate/runstate_test.go`

**Interfaces:**
- Consumes: `internal/deploylock`（IsHeld 判陈旧，现有包不改）
- Produces（Task 8 引擎、Task 10 status 依赖）:
  - `type StageState struct { Name, State string; Duration string }` — State: `success|skipped|failed`
  - `type RunState struct { State, Trigger string; PID int; StartedAt time.Time; FinishedAt *time.Time; Commit, Branch, FailedStage, FailureReason string; Stages []StageState }` — State: `running|success|failed|cancelled`，全部 json tag 小写下划线
  - `func Path(name string) string` — `~/.deployd/run/<name>.status`
  - `func Save(name string, st *RunState) error`
  - `func Read(name string) (RunState, bool)` — 不存在/损坏 → `(RunState{}, false)`
  - `func StaleRecover(name string) RunState` — running 且锁未持有 → 就地改写 `failed`（FailureReason="执行进程中断"）并返回改写后状态；否则原样返回读到的（没读过返回零值）
  - `func WriteCancel(name string) error` / `func HasCancel(name string) bool` / `func ClearCancel(name string) error` — 与 servstate 现有实现相同语义

注意：测试必须用 `t.Setenv("HOME", t.TempDir())` 隔离 `~/.deployd`（servstate 测试的既有坑）。

- [ ] **Step 1: 写失败测试**

```go
package runstate

import (
	"os"
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
```

- [ ] **Step 2: 运行确认失败**

Run: `go test ./internal/runstate/`
Expected: FAIL（package 不存在）

- [ ] **Step 3: 实现 runstate.go**

照抄 `internal/servstate/state.go` 的文件布局惯例（runDir/Path/write 风格），结构换成：

```go
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
```

- [ ] **Step 4: 运行测试通过**

Run: `go test ./internal/runstate/ -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/runstate/
git commit -m "feat(runstate): 工作项执行状态文件 + 陈旧恢复 + 取消 sentinel"
```

---

### Task 3: components 注册表 + shell 组件

**Files:**
- Create: `internal/components/components.go`（注册表 + Request/Results + asString/asStringList 工具）
- Create: `internal/components/shell.go`
- Test: `internal/components/shell_test.go`

**Interfaces:**
- Consumes: `internal/build`（RunCommandCtx、MergeEnv——现有函数，不改）；`internal/notify`（本任务只引用类型）
- Produces（Task 4-6 组件、Task 7 校验、Task 8 引擎依赖）:
  - `type Results map[string]string`
  - `type Request struct { Params map[string]any; Out io.Writer; Notifier *notify.Notifier }` — Out 是节点日志流（引擎已接好文件/终端）；Notifier 仅 email 组件用，nil = 未配置
  - `type Component interface { Run(ctx context.Context, req Request) (Results, error) }`
  - `func Register(name string, c Component)` — 各组件文件 `init()` 自注册（沿用 plugins 模式）
  - `func Get(name string) (Component, error)`
  - `func Known() map[string]bool`
  - `func asString(params map[string]any, key string) string` — 缺失/非 string 返回 ""
  - `func asStringList(params map[string]any, key string) []string` — 接受单字符串或 []any
  - shell 组件：type 名 `"shell"`；params：`sh`（必填，单行或多行）、`cwd`（引擎已填默认 workspace）、`env`（map[string]any 额外环境变量）；Results：`exit_code`/`stdout`/`stderr`（stdout/stderr 截断 1MB）；非零退出码 = error

- [ ] **Step 1: 写失败测试**

```go
package components

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestShellBasic(t *testing.T) {
	var buf bytes.Buffer
	res, err := Get("shell")
	if err != nil {
		t.Fatal(err)
	}
	out, rerr := res.Run(context.Background(), Request{
		Params: map[string]any{"sh": "echo hello; echo err >&2"},
		Out:    &buf,
	})
	if rerr != nil {
		t.Fatalf("零退出码不应报错: %v", rerr)
	}
	if out["exit_code"] != "0" || out["stdout"] != "hello\n" || out["stderr"] != "err\n" {
		t.Fatalf("results = %#v", out)
	}
	if !strings.Contains(buf.String(), "hello") {
		t.Error("stdout 应同时写入 Out（日志流）")
	}
}

func TestShellMultiline(t *testing.T) {
	c, _ := Get("shell")
	// Jenkins 式多行块：逐行执行，最后一行退出码即整体退出码
	script := "A=1\nif [ $A -eq 1 ]; then\n  echo one\nfi\necho done"
	out, err := c.Run(context.Background(), Request{Params: map[string]any{"sh": script}, Out: &bytes.Buffer{}})
	if err != nil || out["stdout"] != "one\ndone\n" {
		t.Fatalf("out=%#v err=%v", out, err)
	}
}

func TestShellNonZeroExit(t *testing.T) {
	c, _ := Get("shell")
	out, err := c.Run(context.Background(), Request{
		Params: map[string]any{"sh": "echo before-fail; exit 3"},
		Out:    &bytes.Buffer{},
	})
	if err == nil {
		t.Fatal("非零退出码必须报错")
	}
	if out["exit_code"] != "3" {
		t.Fatalf("exit_code = %q, want 3", out["exit_code"])
	}
}

func TestShellCwdAndEnv(t *testing.T) {
	dir := t.TempDir()
	c, _ := Get("shell")
	out, err := c.Run(context.Background(), Request{
		Params: map[string]any{
			"sh": "pwd; echo $MYVAR",
			"cwd": dir,
			"env": map[string]any{"MYVAR": "hello"},
		},
		Out: &bytes.Buffer{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if out["stdout"] != dir+"\nhello\n" {
		t.Fatalf("cwd/env 未生效: %q", out["stdout"])
	}
}

func TestShellStdoutTruncated(t *testing.T) {
	c, _ := Get("shell")
	out, err := c.Run(context.Background(), Request{
		Params: map[string]any{"sh": strings.Repeat("x", 1024*1024+10)},
		Out:    &bytes.Buffer{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(out["stdout"]) != 1024*1024 {
		t.Fatalf("stdout 应截断到 1MB, got %d", len(out["stdout"]))
	}
}

func TestShellCtxCancelKillsGroup(t *testing.T) {
	c, _ := Get("shell")
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(100 * time.Millisecond)
		cancel()
	}()
	start := time.Now()
	_, err := c.Run(ctx, Request{
		Params: map[string]any{"sh": "sleep 30"},
		Out:    &bytes.Buffer{},
	})
	if err == nil || time.Since(start) > 5*time.Second {
		t.Fatalf("ctx 取消必须立即杀掉进程组: err=%v elapsed=%v", err, time.Since(start))
	}
}

func TestShellMissingSh(t *testing.T) {
	c, _ := Get("shell")
	if _, err := c.Run(context.Background(), Request{Params: map[string]any{}, Out: &bytes.Buffer{}}); err == nil {
		t.Fatal("缺 sh 必须报错")
	}
}
```

（测试文件 import `time`。）

- [ ] **Step 2: 运行确认失败**

Run: `go test ./internal/components/`
Expected: FAIL

- [ ] **Step 3: 实现 components.go + shell.go**

`components.go`：

```go
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
	Params   map[string]any
	Out      io.Writer       // 节点日志流（引擎接好：日志文件 ± 终端）
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
```

`shell.go`：

```go
package components

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"

	"github.com/auto-deployer/auto-deployer/internal/build"
)

// stdoutLimit 结果变量里 stdout/stderr 的截断上限：完整输出已在 Out（日志）里，
// 参数集只需要可引用的片段，防构建日志撑爆内存。
const stdoutLimit = 1024 * 1024

func init() { Register("shell", Shell{}) }

// Shell 在 deployd 本机执行 shell 命令（要操作远程机器请自己写 ssh user@host '...'）。
// sh 支持单行与多行 YAML 块（Jenkins sh ''' 的对应物）。
type Shell struct{}

func (Shell) Run(ctx context.Context, req Request) (Results, error) {
	sh := asString(req.Params, "sh")
	if sh == "" {
		return nil, fmt.Errorf("shell 组件缺少必填参数 sh")
	}

	var stdout, stderr bytes.Buffer
	cmd := exec.Command("sh", "-c", sh)
	if cwd := asString(req.Params, "cwd"); cwd != "" {
		cmd.Dir = cwd
	}
	// 边执行边写日志（Out），同时在内存留一份做结果变量
	cmd.Stdout = io.MultiWriter(&stdout, req.Out)
	cmd.Stderr = io.MultiWriter(&stderr, req.Out)
	if envParam, ok := req.Params["env"].(map[string]any); ok && len(envParam) > 0 {
		overrides := make(map[string]string, len(envParam))
		for k, v := range envParam {
			overrides[k] = fmt.Sprint(v)
		}
		cmd.Env = build.MergeEnv(os.Environ(), overrides)
	}

	err := build.RunCommandCtx(ctx, cmd) // Setpgid：ctx 取消杀整个进程组

	code := 0
	if cmd.ProcessState != nil {
		code = cmd.ProcessState.ExitCode() // 信号致死 = -1
	}
	res := Results{
		"exit_code": strconv.Itoa(code),
		"stdout":    truncate(stdout.String(), stdoutLimit),
		"stderr":    truncate(stderr.String(), stdoutLimit),
	}
	if err != nil {
		if ctx.Err() != nil {
			return res, fmt.Errorf("shell 被中止（超时或取消）: %w", ctx.Err())
		}
		return res, fmt.Errorf("exit_code=%d", code)
	}
	return res, nil
}

func truncate(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	return s[:limit]
}
```

- [ ] **Step 4: 运行测试通过**

Run: `go test ./internal/components/ -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/components/
git commit -m "feat(components): 组件注册表 + shell 组件（多行/退出码/进程组取消/1MB 截断）"
```

---

### Task 4: git 组件

**Files:**
- Create: `internal/components/git.go`
- Test: `internal/components/git_test.go`

**Interfaces:**
- Consumes: `internal/build` 的 `Fetch(ctx, repoURL, keyFile, branch, destDir, out)`、`EnsureSSHKey()`（均现有）；Task 3 的 Register/Request/Results/asStringList
- Produces:
  - type 名 `"git"`；params：`url`（必填）、`branch`（引擎已把列表解析成具体分支字符串，见 Task 8 applyDefaults）、`workspace`（引擎已填默认 `${system.workspace}`）
  - Results：`commit`（`git log -1 --format=%H`）、`changed`（"true"/"false"，fetch 前后 HEAD 是否变化；新克隆恒 "true"）、`branch`（实际 checkout 的分支名）

测试用本地裸仓库（`git init --bare` + `git clone` + commit + push），不碰网络。注意 git 组件走 `build.Fetch`，其内部会把非 `git@`/`ssh://` 且含 `://` 的 URL 转 SSH；本地 file:// 路径不含 `://` 时不转换、但 keyFile 逻辑照跑——测试直接给本地 bare 仓库路径即可。

- [ ] **Step 1: 写失败测试**

```go
package components

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// newBareRepo 建一个本地裸仓库并提交一个 commit，返回其路径。
func newBareRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	bare := filepath.Join(dir, "repo.git")
	seed := filepath.Join(dir, "seed")
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		cmd.Dir = seed
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	_ = exec.Command("git", "init", "--bare", bare).Run()
	_ = exec.Command("git", "clone", bare, seed).Run()
	run("config", "user.email", "t@t")
	run("config", "user.name", "t")
	_ = os.WriteFile(filepath.Join(seed, "f.txt"), []byte("v1"), 0644)
	run("add", ".")
	run("commit", "-m", "c1")
	run("push", "origin", "HEAD:refs/heads/main")
	return bare
}

func TestGitCloneAndChanged(t *testing.T) {
	bare := newBareRepo(t)
	ws := t.TempDir()
	c, _ := Get("git")
	out, err := c.Run(context.Background(), Request{
		Params: map[string]any{"url": bare, "branch": "main", "workspace": ws},
		Out:    &bytes.Buffer{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if out["changed"] != "true" || out["branch"] != "main" || out["commit"] == "" {
		t.Fatalf("首次克隆: %#v", out)
	}
	if _, err := os.Stat(filepath.Join(ws, "f.txt")); err != nil {
		t.Error("工作空间应有检出文件")
	}
	// 无新提交再跑：changed=false（fetch 快路径）
	out2, err := c.Run(context.Background(), Request{
		Params: map[string]any{"url": bare, "branch": "main", "workspace": ws},
		Out:    &bytes.Buffer{},
	})
	if err != nil || out2["changed"] != "false" {
		t.Fatalf("无新提交: %#v err=%v", out2, err)
	}
}

func TestGitMissingParams(t *testing.T) {
	c, _ := Get("git")
	if _, err := c.Run(context.Background(), Request{Params: map[string]any{}, Out: &bytes.Buffer{}}); err == nil {
		t.Fatal("缺 url/branch/workspace 必须报错")
	}
}

func TestGitBadURL(t *testing.T) {
	c, _ := Get("git")
	_, err := c.Run(context.Background(), Request{
		Params: map[string]any{"url": "/nonexistent/repo.git", "branch": "main", "workspace": t.TempDir()},
		Out:    &bytes.Buffer{},
	})
	if err == nil {
		t.Fatal("克隆失败必须报错")
	}
}
```

- [ ] **Step 2: 运行确认失败**

Run: `go test ./internal/components/ -run TestGit`
Expected: FAIL

- [ ] **Step 3: 实现 git.go**

```go
package components

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"

	"github.com/auto-deployer/auto-deployer/internal/build"
)

func init() { Register("git", Git{}) }

// Git 拉取代码到 workspace：快路径（fetch --force + checkout -f + reset --hard）
// 失败自动回退 Jenkins 式干净克隆（复用 build.Fetch 的全部语义，含 HTTPS→SSH）。
type Git struct{}

func (Git) Run(ctx context.Context, req Request) (Results, error) {
	url := asString(req.Params, "url")
	branch := asString(req.Params, "branch")
	ws := asString(req.Params, "workspace")
	if url == "" || branch == "" || ws == "" {
		return nil, fmt.Errorf("git 组件需要 url/branch/workspace 参数")
	}

	// SSH key 只需存在（已有则秒回）；新机器上自动生成并提示用户配公钥
	keyFile, _, _, err := build.EnsureSSHKey()
	if err != nil {
		return nil, fmt.Errorf("确保 SSH 密钥: %w", err)
	}

	before := headOf(ws)
	if err := build.Fetch(ctx, url, keyFile, branch, ws, req.Out); err != nil {
		return Results{}, err
	}
	after := headOf(ws)

	changed := "true"
	if before != "" && before == after {
		changed = "false"
	}
	return Results{
		"commit":  after,
		"changed": changed,
		"branch":  branch,
	}, nil
}

// headOf 返回 workspace 当前 HEAD（非 git 仓库返回空串）。
func headOf(ws string) string {
	out, err := exec.Command("git", "-C", ws, "log", "-1", "--format=%H").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
```

注意删掉 `bytes` import 若未用（headOf 用 `exec.Output`）。`build.GetLatestAuthorEmail`/`GetLatestCommit` 旧函数本组件不需要——webhook 作者来自 payload，不要调用它们。

- [ ] **Step 4: 运行测试通过**

Run: `go test ./internal/components/ -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/components/git.go internal/components/git_test.go
git commit -m "feat(components): git 组件，fetch 快路径+干净克隆回退，changed 检测"
```

---

### Task 5: notify.Send 改造 + email 组件

**Files:**
- Modify: `internal/notify/email.go`（Send 增加 to 参数；New 去掉 to；删 n.to 字段）
- Modify: `internal/notify/email_test.go`、`internal/notify/smtp_auth_test.go`（适配新签名）
- Create: `internal/components/email.go`
- Test: `internal/components/email_test.go`

**Interfaces:**
- Consumes: Task 3 注册表
- Produces:
  - `func New(smtpHost string, smtpPort int, smtpUsername, smtpToken string, smtpTLS bool, resendToken, resendFrom string) *notify.Notifier`（去掉 to 尾参）
  - `func (n *Notifier) Send(to []string, subject, body string) error`（发送前用 to；空 to = 不发送直接返回 nil？**否**：空 to 返回错误「收件人为空」）
  - email 组件：type 名 `"email"`；params：`subject`（必填）、`body`（必填）、`to`（逗号分隔；引擎已填默认 = 全局 notifications.to + system.pushers）；Results：失败时 `error` 键 + error 返回；Notifier 为 nil → 报错「未配置 SMTP/Resend」

- [ ] **Step 1: 改 notify 签名（含适配现有测试）**

`email.go` 修改点（保持 sendSMTP/sendResend 主体不动）：

```go
// New creates a Notifier.
// If resend.APIKey is set, it uses Resend API; otherwise falls back to SMTP.
// 收件人不再固化在构造期：Send 按次传入（email 组件的 to 是参数）。
func New(smtpHost string, smtpPort int, smtpUsername, smtpToken string, smtpTLS bool,
	resendToken, resendFrom string) *Notifier {
	provider := "smtp"
	if resendToken != "" {
		provider = "resend"
	}
	return &Notifier{
		provider:    provider,
		smtpHost:    smtpHost,
		smtpPort:    smtpPort,
		username:    smtpUsername,
		token:       smtpToken,
		tls:         smtpTLS,
		resendToken: resendToken,
		resendFrom:  resendFrom,
	}
}

// Send emails the given subject and HTML body to the given recipients.
func (n *Notifier) Send(to []string, subject, body string) error {
	if len(to) == 0 {
		return fmt.Errorf("收件人列表为空")
	}
	switch n.provider {
	case "resend":
		return n.sendResend(to, subject, body)
	default:
		return n.sendSMTP(to, subject, body)
	}
}
```

`sendSMTP`/`sendResend`/`buildMessage` 内所有 `n.to` 换成参数 `to`；`NotifyDeployResult` 及 `DeployNotice` 整个删除（旧编排专用，新引擎不用）。同步修 `email_test.go`/`smtp_auth_test.go`：`New(...)` 去掉最后的 to 参数，`Send(...)` 改为 `Send([]string{"a@b.c"}, ...)`。此时 `go build ./...` 会因 orchestrator/commands 还调用旧 API 报错——这两个文件在 Task 9/11 才删，**临时就地修编译**：`internal/deploy/orchestrator.go` 的 `buildNotifier` 调用处与 `internal/daemon/commands.go` 的 `buildNotifier`/`TriggerDeploy` 改成 `New(...)` 无 to + `Send(recipients, ...)`（TriggerDeploy 本来就是 TODO 占位，直接删掉 TriggerDeploy 函数与 buildNotifier，cmd 引用处留到 Task 10 处理——若编译报错，先在 cmd 里注释掉调用并留 `// TODO(Task 10)`）。

- [ ] **Step 2: 运行 notify 测试**

Run: `go test ./internal/notify/ -v && go build ./...`
Expected: PASS + 编译通过

- [ ] **Step 3: 写 email 组件失败测试**

```go
package components

import (
	"bytes"
	"context"
	"testing"

	"github.com/auto-deployer/auto-deployer/internal/notify"
)

type fakeMailer struct{ gotTo []string; gotSubject, gotBody string }

func TestEmail(t *testing.T) {
	c, _ := Get("email")
	// nil Notifier → 明确报错
	if _, err := c.Run(context.Background(), Request{
		Params: map[string]any{"subject": "s", "body": "b"}, Out: &bytes.Buffer{},
	}); err == nil {
		t.Fatal("未配置邮件服务必须报错")
	}
	// 缺 subject/body → 报错
	if _, err := c.Run(context.Background(), Request{
		Params: map[string]any{"body": "b"}, Out: &bytes.Buffer{}, Notifier: notify.New("", 0, "", "", false, "", ""),
	}); err == nil {
		t.Fatal("缺 subject 必须报错")
	}
}

func TestEmailSends(t *testing.T) {
	// 用真实 Notifier 但指向不存在的服务器会失败；这里只验证参数解析路径：
	// 构造非法收件人触发 Send 报错，同时验证 to 解析（逗号分隔）。
	c, _ := Get("email")
	_, err := c.Run(context.Background(), Request{
		Params:   map[string]any{"subject": "s", "body": "b", "to": "a@x.com, b@y.com"},
		Out:      &bytes.Buffer{},
		Notifier: notify.New("127.0.0.1", 1, "", "", false, "", ""), // 端口 1 必连不上
	})
	if err == nil {
		t.Fatal("连接失败必须返回错误（证明 to 已传入 Send）")
	}
}
```

- [ ] **Step 4: 实现 email.go**

```go
package components

import (
	"context"
	"fmt"
	"strings"
)

func init() { Register("email", Email{}) }

// Email 发送通知邮件。SMTP/Resend 凭据来自全局配置（引擎注入 Notifier），
// 组件只负责 subject/body/to（全部支持参数插值，由引擎完成）。
type Email struct{}

func (Email) Run(ctx context.Context, req Request) (Results, error) {
	if req.Notifier == nil {
		return nil, fmt.Errorf("未配置 SMTP/Resend，email 组件不可用")
	}
	subject := asString(req.Params, "subject")
	body := asString(req.Params, "body")
	toRaw := asString(req.Params, "to")
	if subject == "" || body == "" {
		return nil, fmt.Errorf("email 组件缺少必填参数 subject/body")
	}
	var to []string
	for _, addr := range strings.Split(toRaw, ",") {
		if addr = strings.TrimSpace(addr); addr != "" {
			to = append(to, addr)
		}
	}
	if len(to) == 0 {
		return nil, fmt.Errorf("email 组件收件人为空（to 参数或全局 notifications.to 未配置）")
	}
	if err := req.Notifier.Send(to, subject, body); err != nil {
		return Results{"error": err.Error()}, fmt.Errorf("发送邮件失败: %w", err)
	}
	return Results{}, nil
}
```

- [ ] **Step 5: 运行全部组件测试 + Commit**

Run: `go test ./internal/components/ ./internal/notify/ -v`
Expected: PASS

```bash
git add internal/notify/ internal/components/email.go internal/components/email_test.go internal/deploy/orchestrator.go internal/daemon/commands.go
git commit -m "feat(components): email 组件；notify.Send 按次传收件人"
```

---

### Task 6: cleanup 组件

**Files:**
- Create: `internal/components/cleanup.go`
- Test: `internal/components/cleanup_test.go`

**Interfaces:**
- Consumes: Task 3 注册表
- Produces: type 名 `"cleanup"`；params：`keep`（glob 列表，缺省 `[".git"]`——保 .git 是为了下轮 fetch 快路径）；Results：`deleted`（清理条目数）。删除 workspace 下所有不在 keep 匹配内的条目。

- [ ] **Step 1: 写失败测试**

```go
package components

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
)

func makeWs(t *testing.T) string {
	t.Helper()
	ws := t.TempDir()
	for _, f := range []string{"a.txt", "b.log", ".git/config"} {
		p := filepath.Join(ws, f)
		_ = os.MkdirAll(filepath.Dir(p), 0755)
		if err := os.WriteFile(p, []byte("x"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	_ = os.MkdirAll(filepath.Join(ws, "node_modules/pkg"), 0755)
	return ws
}

func TestCleanupDefaultKeepsGit(t *testing.T) {
	ws := makeWs(t)
	c, _ := Get("cleanup")
	out, err := c.Run(context.Background(), Request{
		Params: map[string]any{"workspace": ws}, Out: &bytes.Buffer{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(ws, ".git")); err != nil {
		t.Fatal(".git 缺省必须保留")
	}
	if _, err := os.Stat(filepath.Join(ws, "a.txt")); !os.IsNotExist(err) {
		t.Fatal("a.txt 应被清理")
	}
	if _, err := os.Stat(filepath.Join(ws, "node_modules")); !os.IsNotExist(err) {
		t.Fatal("目录应被清理")
	}
	if out["deleted"] != "4" { // a.txt b.log node_modules + .git 不算 → 3；见下
		t.Logf("deleted=%s（按实现条目数，断言见下）", out["deleted"])
	}
}

func TestCleanupKeepGlob(t *testing.T) {
	ws := makeWs(t)
	c, _ := Get("cleanup")
	_, err := c.Run(context.Background(), Request{
		Params: map[string]any{"workspace": ws, "keep": []any{".git", "*.log"}},
		Out:    &bytes.Buffer{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(ws, "b.log")); err != nil {
		t.Fatal("*.log 应被保留")
	}
	if _, err := os.Stat(filepath.Join(ws, "a.txt")); !os.IsNotExist(err) {
		t.Fatal("a.txt 应被清理")
	}
}

func TestCleanupEmptyDir(t *testing.T) {
	c, _ := Get("cleanup")
	ws := t.TempDir() // 空目录（甚至不是 git 仓库）也不报错
	if _, err := c.Run(context.Background(), Request{
		Params: map[string]any{"workspace": ws}, Out: &bytes.Buffer{},
	}); err != nil {
		t.Fatal(err)
	}
}
```

（TestCleanupDefaultKeepsGit 中 deleted 的精确断言：a.txt、b.log、node_modules 共 3 条，把 Logf 改为 `if out["deleted"] != "3" { t.Fatalf(...) }`。）

- [ ] **Step 2: 运行确认失败**

Run: `go test ./internal/components/ -run TestCleanup`
Expected: FAIL

- [ ] **Step 3: 实现 cleanup.go**

```go
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
```

- [ ] **Step 4: 运行测试通过**

Run: `go test ./internal/components/ -v`
Expected: PASS 全部（shell/git/email/cleanup）

- [ ] **Step 5: Commit**

```bash
git add internal/components/cleanup.go internal/components/cleanup_test.go
git commit -m "feat(components): cleanup 组件，keep glob 缺省保 .git"
```

---

### Task 7: config — PipelineConfig/StageConfig + 校验 + 触发仓库提取

**Files:**
- Modify: `internal/config/config.go`（新增 pipeline 类型；AppConfig 增加 Pipelines 字段——**Services 字段本任务保留**，Task 11 才删）
- Modify: `internal/config/validate.go`（新增 pipeline 校验，保留 services 旧校验）
- Modify: `internal/config/config_test.go`、`internal/config/validate_test.go`（新增用例）
- Modify: `cmd/start_validate_test.go` 若引用了受影响逻辑（跑一遍确认即可）

**Interfaces:**
- Consumes: `internal/components.Known()`（校验 type 名；components 不 import config，无环）
- Produces（Task 8/9/10 依赖）:
  - `type StageConfig struct { Name string; Type string; Params map[string]any; Output map[string]string; Skip string; When string; Timeout string }`（yaml tag 同名小写）
  - `func (s StageConfig) TimeoutDuration() (time.Duration, error)`
  - `type PipelineConfig struct { Name, Workspace, Timeout string; Env map[string]string; Stages []StageConfig }` + 加载期提取的 `TriggerURL string` / `TriggerBranches []string`（yaml:"-"）
  - `func (p PipelineConfig) TimeoutDuration() (time.Duration, error)`
  - `AppConfig.Pipelines []PipelineConfig \`yaml:"pipelines"\``
  - `func FindPipeline(cfg *AppConfig, name string) *PipelineConfig` — 找不到返回 nil
  - `func (p *PipelineConfig) ExtractTrigger() error` — 从第一个 type=="git" 的 stage 取 url/branch 写入 TriggerURL/TriggerBranches；url/branch 含 `${` → error（webhook 匹配需要字面量）。Load 在 unmarshal 后对每个 pipeline 调用并聚合报错。

- [ ] **Step 1: 写失败测试（validate_test.go 追加）**

```go
func TestValidatePipelines(t *testing.T) {
	gitStage := func(name string, params map[string]any) StageConfig {
		return StageConfig{Name: name, Type: "git", Params: params}
	}
	base := func(stages ...StageConfig) *AppConfig {
		return &AppConfig{Pipelines: []PipelineConfig{{
			Name: "app", Workspace: "/tmp/ws",
			Stages: append([]StageConfig{gitStage("拉取", map[string]any{
				"url": "https://github.com/u/r.git", "branch": []any{"main"},
			})}, stages...)}}}
	}

	if errs := Validate(base()); len(errs) != 0 {
		t.Errorf("合法配置不应报错: %v", errs)
	}

	// stage 重名
	cfg := base(StageConfig{Name: "拉取", Type: "shell", Params: map[string]any{"sh": "ls"}})
	if errs := Validate(cfg); len(errs) == 0 {
		t.Error("stage 重名必须报错")
	}

	// 未知组件类型
	cfg = base(StageConfig{Name: "构建", Type: "nope", Params: map[string]any{}})
	if errs := Validate(cfg); len(errs) == 0 {
		t.Error("未知 type 必须报错")
	}

	// when 枚举
	cfg = base(StageConfig{Name: "通知", Type: "email", When: "sometimes", Params: map[string]any{"subject": "s", "body": "b"}})
	if errs := Validate(cfg); len(errs) == 0 {
		t.Error("非法 when 必须报错")
	}

	// 缺 name/workspace
	cfg = base()
	cfg.Pipelines[0].Name = ""
	if errs := Validate(cfg); len(errs) == 0 {
		t.Error("缺 name 必须报错")
	}
	cfg = base()
	cfg.Pipelines[0].Stages = nil
	if errs := Validate(cfg); len(errs) == 0 {
		t.Error("无 stages 必须报错")
	}
}

func TestValidatePipelineWorkspaceUnique(t *testing.T) {
	cfg := &AppConfig{Pipelines: []PipelineConfig{
		{Name: "a", Workspace: "/tmp/ws", Stages: []StageConfig{{Name: "s", Type: "shell", Params: map[string]any{"sh": "true"}}}},
		{Name: "b", Workspace: "/tmp/ws", Stages: []StageConfig{{Name: "s", Type: "shell", Params: map[string]any{"sh": "true"}}}},
	}}
	if errs := Validate(cfg); len(errs) == 0 {
		t.Error("workspace 共享必须报错（独立锁看不见彼此，会互相清场）")
	}
}

func TestExtractTrigger(t *testing.T) {
	p := &PipelineConfig{Stages: []StageConfig{
		{Name: "拉取", Type: "git", Params: map[string]any{
			"url": "https://github.com/u/r.git", "branch": []any{"main", "release/*"}}},
		{Name: "构建", Type: "shell", Params: map[string]any{"sh": "make"}},
	}}
	if err := p.ExtractTrigger(); err != nil {
		t.Fatal(err)
	}
	if p.TriggerURL != "https://github.com/u/r.git" || len(p.TriggerBranches) != 2 {
		t.Fatalf("trigger 提取错误: %+v", p)
	}
	// 引用必须拒绝（webhook 匹配需要字面量）
	bad := &PipelineConfig{Stages: []StageConfig{
		{Name: "拉取", Type: "git", Params: map[string]any{"url": "${env.url}", "branch": "main"}}}}
	if err := bad.ExtractTrigger(); err == nil {
		t.Fatal("url 写引用必须报错")
	}
	// 无 git 节点：合法（仅手动 exec），TriggerURL 为空
	nogit := &PipelineConfig{Stages: []StageConfig{
		{Name: "构建", Type: "shell", Params: map[string]any{"sh": "make"}}}}
	if err := nogit.ExtractTrigger(); err != nil {
		t.Fatal(err)
	}
	if nogit.TriggerURL != "" {
		t.Fatal("无 git 节点不应有 TriggerURL")
	}
}

func TestLoadPipelinesYaml(t *testing.T) {
	yml := `
pipelines:
  - name: app
    workspace: /tmp/ws
    timeout: 45m
    env:
      JAVA_OPTS: -Xmx512m
    stages:
      - name: 拉取代码
        type: git
        params:
          url: "https://github.com/u/r.git"
          branch: ["main"]
      - name: 构建
        type: shell
        params:
          sh: |
            mvn package
        output:
          v: "${stdout}"
        skip: "${env.skipBuild}"
        when: failure
        timeout: 10m
`
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(yml), 0644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	p := cfg.Pipelines[0]
	if p.Name != "app" || len(p.Stages) != 2 || p.Env["JAVA_OPTS"] != "-Xmx512m" {
		t.Fatalf("解析错误: %+v", p)
	}
	if p.TriggerURL != "https://github.com/u/r.git" || p.TriggerBranches[0] != "main" {
		t.Fatalf("trigger 未提取: %+v", p)
	}
	st := p.Stages[1]
	if st.Params["sh"] != "mvn package\n" || st.Output["v"] != "${stdout}" || st.When != "failure" || st.Skip != "${env.skipBuild}" {
		t.Fatalf("stage 解析错误: %+v", st)
	}
	if d, err := st.TimeoutDuration(); err != nil || d != 10*time.Minute {
		t.Fatalf("stage timeout: %v %v", d, err)
	}
}
```

（import 补 `time`、`os`、`path/filepath`。）

- [ ] **Step 2: 运行确认失败**

Run: `go test ./internal/config/ -run 'Pipeline|Trigger|LoadPipelines'`
Expected: FAIL（类型未定义）

- [ ] **Step 3: 实现类型与校验**

`config.go` 追加（放在 ServiceConfig 之后）：

```go
// StageConfig 是流水线的一个节点：以某组件类型执行，params 是组件入参
// （string 值在执行期做 ${...} 插值，非 string 值如 git 的 branch 列表原样透传）。
// When: ""=主流程 / "failure"=主流程失败时执行 / "always"=无论成败执行。
type StageConfig struct {
	Name    string            `yaml:"name"`
	Type    string            `yaml:"type"`
	Params  map[string]any    `yaml:"params"`
	Output  map[string]string `yaml:"output"`
	Skip    string            `yaml:"skip"`
	When    string            `yaml:"when"`
	Timeout string            `yaml:"timeout"`
}

func (s StageConfig) TimeoutDuration() (time.Duration, error) {
	if s.Timeout == "" {
		return 0, nil
	}
	d, err := time.ParseDuration(s.Timeout)
	if err != nil {
		return 0, fmt.Errorf("invalid stage timeout %q: %w", s.Timeout, err)
	}
	return d, nil
}

// PipelineConfig 是一个工作项：任意数量节点组成的流水线模板。
// TriggerURL/TriggerBranches 在加载期从第一个 git 节点提取，供 webhook 匹配；
// 无 git 节点的工作项只能手动 exec。
type PipelineConfig struct {
	Name      string            `yaml:"name"`
	Workspace string            `yaml:"workspace"`
	Timeout   string            `yaml:"timeout"`
	Env       map[string]string `yaml:"env"`
	Stages    []StageConfig     `yaml:"stages"`

	TriggerURL      string   `yaml:"-"`
	TriggerBranches []string `yaml:"-"`
}

func (p PipelineConfig) TimeoutDuration() (time.Duration, error) {
	if p.Timeout == "" {
		return 0, nil
	}
	d, err := time.ParseDuration(p.Timeout)
	if err != nil {
		return 0, fmt.Errorf("invalid timeout %q: %w", p.Timeout, err)
	}
	return d, nil
}

// ExtractTrigger 从第一个 type==git 的节点提取 webhook 匹配用的 url/branch。
// 两项必须是字面量：匹配发生在配置加载期，那时还没有可插值的参数集。
func (p *PipelineConfig) ExtractTrigger() error {
	for _, st := range p.Stages {
		if st.Type != "git" {
			continue
		}
		url, _ := st.Params["url"].(string)
		if strings.Contains(url, "${") {
			return fmt.Errorf("pipeline %q: git 节点的 url 必须是字面量（webhook 匹配用），当前 %q", p.Name, url)
		}
		p.TriggerURL = url
		for _, b := range asAnyList(st.Params["branch"]) {
			if strings.Contains(b, "${") {
				return fmt.Errorf("pipeline %q: git 节点的 branch 必须是字面量，当前 %q", p.Name, b)
			}
			p.TriggerBranches = append(p.TriggerBranches, b)
		}
		return nil
	}
	return nil
}

func asAnyList(v any) []string {
	switch t := v.(type) {
	case string:
		return []string{t}
	case []any:
		out := make([]string, 0, len(t))
		for _, item := range t {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

// FindPipeline 按名查找工作项；找不到返回 nil。
func FindPipeline(cfg *AppConfig, name string) *PipelineConfig {
	for i := range cfg.Pipelines {
		if cfg.Pipelines[i].Name == name {
			return &cfg.Pipelines[i]
		}
	}
	return nil
}
```

AppConfig 增加字段 `Pipelines []PipelineConfig \`yaml:"pipelines"\``；`Load` 在 `parseCommonDeployFields(&cfg)` 之后追加：

```go
	for i := range cfg.Pipelines {
		if err := cfg.Pipelines[i].ExtractTrigger(); err != nil {
			return nil, err
		}
	}
```

`validate.go` 的 `Validate` 追加（保留旧 services 校验不动）：

```go
	known := components.Known()
	seenPipelineName := map[string]bool{}
	seenWorkspace := map[string]string{} // 复用旧 services 的同款表？分开：新的单独一张
	seenWs := map[string]string{}
	for i, p := range cfg.Pipelines {
		prefix := fmt.Sprintf("pipelines[%d]", i)
		if p.Name == "" {
			errs = append(errs, fmt.Errorf("%s: name is required", prefix))
		}
		if seenPipelineName[p.Name] {
			errs = append(errs, fmt.Errorf("%s: 工作项名 %q 重复", prefix, p.Name))
		}
		seenPipelineName[p.Name] = true
		if p.Workspace == "" {
			errs = append(errs, fmt.Errorf("%s: workspace is required", prefix))
		} else if prev, dup := seenWs[p.Workspace]; dup {
			// workspace 共享 = 两个独立锁看不见彼此的执行，会互相清场
			errs = append(errs, fmt.Errorf("%s: workspace %q 已被工作项 %q 使用", prefix, p.Workspace, prev))
		} else {
			seenWs[p.Workspace] = p.Name
		}
		if _, err := p.TimeoutDuration(); err != nil {
			errs = append(errs, fmt.Errorf("%s: %v (e.g. \"45m\")", prefix, err))
		}
		if len(p.Stages) == 0 {
			errs = append(errs, fmt.Errorf("%s: 至少需要一个 stage", prefix))
		}
		seenStage := map[string]bool{}
		for j, st := range p.Stages {
			sprefix := fmt.Sprintf("%s.stages[%d]", prefix, j)
			if st.Name == "" {
				errs = append(errs, fmt.Errorf("%s: name is required", sprefix))
			}
			if seenStage[st.Name] {
				errs = append(errs, fmt.Errorf("%s: stage 名 %q 重复（output 引用的命名空间）", sprefix, st.Name))
			}
			seenStage[st.Name] = true
			if !known[st.Type] {
				errs = append(errs, fmt.Errorf("%s: 未知组件类型 %q (支持: git/shell/email/cleanup)", sprefix, st.Type))
			}
			if st.When != "" && st.When != "failure" && st.When != "always" {
				errs = append(errs, fmt.Errorf("%s: when 只能是 failure/always，当前 %q", sprefix, st.When))
			}
			if _, err := st.TimeoutDuration(); err != nil {
				errs = append(errs, fmt.Errorf("%s: %v", sprefix, err))
			}
		}
	}
```

（import `github.com/auto-deployer/auto-deployer/internal/components`。**注意**：这会让 config 依赖 components；components 只依赖 build/notify，无环，可安全 import。）

- [ ] **Step 4: 运行测试通过 + 全量构建**

Run: `go test ./internal/config/ -v && go build ./... && go test ./...`
Expected: PASS（旧的 services 校验用例也仍绿）

- [ ] **Step 5: Commit**

```bash
git add internal/config/
git commit -m "feat(config): PipelineConfig/StageConfig 类型 + 校验 + webhook 触发仓库提取"
```

---

### Task 8: internal/pipeline — 执行引擎

**Files:**
- Create: `internal/pipeline/pipeline.go`
- Test: `internal/pipeline/pipeline_test.go`

**Interfaces:**
- Consumes: `internal/param`、`internal/components`、`internal/runstate`、`internal/logger`、`internal/deploylock`、`internal/config`、`internal/notify`（Task 1-7 全部产物）
- Produces（Task 9 webhook、Task 10 cmd 依赖）:
  - `type Options struct { Trigger string; Args map[string]string; Pushers []string; Branch, Commit, Author, Message string; Console io.Writer; Cfg *config.AppConfig }` — Trigger: "manual"/"webhook"；Console nil = 仅写日志文件（daemon 队列路径）
  - `type StageResult struct { Name, State, Duration string }`
  - `type Result struct { Status string; FailedStage string; Error string; Stages []StageResult }` — Status: `success|failed|cancelled`
  - `func Run(ctx context.Context, pl *config.PipelineConfig, opts Options) *Result`

**执行算法（照此实现）：**

1. `log := logger.GetServiceLogger(pl.Name)`；`out io.Writer = log`，Console 非 nil 时 `out = io.MultiWriter(log, opts.Console)`；`say(format, ...)` = log.Printf + Console 输出（工具行双写，原始子进程输出只走 out）
2. 写 runstate running（PID=os.Getpid()）、清残留 cancel sentinel
3. 日志 run 头：`=== <时间> <name> [trigger branch commit] ===`
4. 组装参数集：System{name,trigger,branch,commit,author,message,pushers(逗号 join),workspace} + Env + Args；branch = opts.Branch 非空则用之，否则第一个 TriggerBranches（手动 exec）
5. `mainCtx = WithTimeout(ctx, 总预算)`（默认 30m）；起 `watchCancel` goroutine（500ms 轮询 `runstate.HasCancel` → cancel，复用旧 deploy.watchCancel 的写法）+ SIGINT/SIGTERM→cancel goroutine
6. **主流程**：按列表顺序跑 `When==""` 的节点。每个节点：SkipTrue → 跳过记 skipped；节点 timeout>0 则 `WithTimeout(mainCtx, d)` 子 ctx；`InterpolateParams` → `applyDefaults` → `components.Get(type).Run` → 成功则求值 Output map（`Interpolate(raw, results)` 裸名）并 AddOutput；失败记 failed + FailedStage + break
7. **applyDefaults**（引擎注入组件缺省值，插值后）：`shell` 无 `cwd` → 设 workspace；`git` 无 `workspace` → 设 workspace、`branch` 若是列表 → 换成具体 branch（system.branch 命中列表则用命中的，否则第一个元素）；`cleanup` 无 `workspace` → 设 workspace
8. 结果判定：`mainCtx.Err()==context.Canceled` → cancelled；主流程有错 → failed；否则 success
9. **后置流程**：`postCtx = WithTimeout(ctx, 10m)`（**父 ctx 而非 mainCtx**——总预算耗尽/节点超时后清理仍要跑；10m 是后置节点自身的硬上限）。设 `system.result`/`system.failed_stage`。跑 `when==always`（恒）与 `when==failure`（仅 failed；**cancelled 不跑 failure**）的节点，按列表顺序。后置节点失败只 say 记日志，不影响最终状态、不递归
10. runstate Save 终态（FinishedAt=now），返回 Result

测试技巧：注册测试专用组件 `components.Register("test", ...)` 制造成功/失败/超时/取消/记录调用序列。**引擎测试里 `t.Setenv("HOME", t.TempDir())`**（runstate/锁文件隔离）。

- [ ] **Step 1: 写失败测试（覆盖 Review Focus 1/2/3）**

```go
package pipeline

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/auto-deployer/auto-deployer/internal/components"
	"github.com/auto-deployer/auto-deployer/internal/config"
)

// scriptComp 按 params["script"] 里的动作序列执行，记录调用顺序。
type scriptComp struct {
	mu      sync.Mutex
	calls   []string
	sleepFor time.Duration // >0 时睡这么久（测超时/取消）
}

func (s *scriptComp) Run(ctx context.Context, req components.Request) (components.Results, error) {
	script, _ := req.Params["script"].(string)
	s.mu.Lock()
	s.calls = append(s.calls, script)
	s.mu.Unlock()
	if s.sleepFor > 0 {
		select {
		case <-time.After(s.sleepFor):
		case <-ctx.Done():
			return components.Results{}, ctx.Err()
		}
	}
	if script == "fail" {
		return components.Results{}, fmt.Errorf("boom")
	}
	return components.Results{"stdout": "out:" + script}, nil
}

func (s *scriptComp) seq() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string{}, s.calls...)
}

var testComp *scriptComp

func TestMain(m *testing.M) {
	testComp = &scriptComp{}
	components.Register("test", testComp)
	os.Exit(m.Run())
}

func stage(name, when, script string) config.StageConfig {
	return config.StageConfig{
		Name: name, Type: "test", When: when,
		Params: map[string]any{"script": script},
	}
}

func newPipeline(stages ...config.StageConfig) *config.PipelineConfig {
	return &config.PipelineConfig{Name: "t", Workspace: "/tmp/ws-t", Stages: stages}
}

func TestMainFlowFailFast(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	testComp.calls = nil
	pl := newPipeline(stage("a", "", "ok"), stage("b", "", "fail"), stage("c", "", "ok"),
		stage("notify-f", "failure", "notify-fail"), stage("clean", "always", "cleanup"),
		stage("notify-s", "", "notify-ok"))
	res := Run(context.Background(), pl, Options{Trigger: "manual"})
	if res.Status != "failed" || res.FailedStage != "b" {
		t.Fatalf("res=%+v", res)
	}
	want := []string{"a", "b", "notify-fail", "cleanup"} // c 被跳过；notify-s 是主流程也不跑
	if fmt.Sprint(testComp.seq()) != fmt.Sprint(want) {
		t.Fatalf("执行序列 = %v, want %v", testComp.seq(), want)
	}
}

func TestMainFlowSuccess(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	testComp.calls = nil
	pl := newPipeline(stage("a", "", "ok"), stage("ok2", "", "ok"),
		stage("notify-f", "failure", "notify-fail"), stage("clean", "always", "cleanup"))
	res := Run(context.Background(), pl, Options{Trigger: "manual"})
	if res.Status != "success" {
		t.Fatalf("res=%+v", res)
	}
	want := []string{"a", "ok2", "cleanup"} // 成功：failure 节点不跑，always 跑
	if fmt.Sprint(testComp.seq()) != fmt.Sprint(want) {
		t.Fatalf("执行序列 = %v, want %v", testComp.seq(), want)
	}
}

func TestSkip(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	testComp.calls = nil
	skipped := stage("a", "", "ok")
	skipped.Skip = "${env.skipme}"
	pl := newPipeline(skipped, stage("b", "", "ok"))
	pl.Env = map[string]string{"skipme": "true"}
	res := Run(context.Background(), pl, Options{Trigger: "manual"})
	if res.Status != "success" || len(res.Stages) != 2 || res.Stages[0].State != "skipped" {
		t.Fatalf("res=%+v stages=%+v", res, res.Stages)
	}
	if fmt.Sprint(testComp.seq()) != "[b]" {
		t.Fatalf("被 skip 的节点不应执行: %v", testComp.seq())
	}
}

func TestUndefinedRefFailsStage(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	testComp.calls = nil
	s := stage("a", "", "ok")
	s.Params = map[string]any{"script": "${env.typo}"} // 未定义引用
	pl := newPipeline(s, stage("clean", "always", "cleanup"))
	res := Run(context.Background(), pl, Options{Trigger: "manual"})
	if res.Status != "failed" || res.FailedStage != "a" {
		t.Fatalf("未定义引用必须让节点失败: %+v", res)
	}
}

func TestOutputParams(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	testComp.calls = nil
	first := stage("a", "", "v1")
	first.Output = map[string]string{"val": "${stdout}"}
	second := stage("b", "", "use")
	second.Params = map[string]any{"script": "got:${output.a.val}"}
	pl := newPipeline(first, second)
	res := Run(context.Background(), pl, Options{Trigger: "manual"})
	if res.Status != "success" {
		t.Fatalf("res=%+v", res)
	}
	// scriptComp 拿到的 script 应是 "got:out:v1"（output 已插值）——通过调用记录断言
	if got := testComp.seq()[1]; got != "got:out:v1" {
		t.Fatalf("output 引用未生效: %q", got)
	}
}

func TestTotalTimeoutStillRunsAlways(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	testComp.calls = nil
	testComp.sleepFor = 5 * time.Second
	defer func() { testComp.sleepFor = 0 }()
	pl := newPipeline(stage("slow", "", "x"), stage("clean", "always", "cleanup"))
	pl.Timeout = "300ms"
	start := time.Now()
	res := Run(context.Background(), pl, Options{Trigger: "manual"})
	if res.Status != "failed" || time.Since(start) > 3*time.Second {
		t.Fatalf("总预算耗尽应判 failed: %+v elapsed=%v", res, time.Since(start))
	}
	if fmt.Sprint(testComp.seq()) != "[x, cleanup]" {
		t.Fatalf("总预算耗尽后 always 清理仍要执行: %v", testComp.seq())
	}
}

func TestStageTimeout(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	testComp.calls = nil
	testComp.sleepFor = 5 * time.Second
	defer func() { testComp.sleepFor = 0 }()
	s := stage("slow", "", "x")
	s.Timeout = "200ms"
	pl := newPipeline(s, stage("clean", "always", "cleanup"))
	res := Run(context.Background(), pl, Options{Trigger: "manual"})
	if res.Status != "failed" || res.FailedStage != "slow" {
		t.Fatalf("节点超时应判 failed: %+v", res)
	}
	if fmt.Sprint(testComp.seq()) != "[x, cleanup]" {
		t.Fatalf("节点超时后 always 仍要执行: %v", testComp.seq())
	}
}

func TestCancelRunsAlwaysNotFailure(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	testComp.calls = nil
	testComp.sleepFor = 2 * time.Second
	defer func() { testComp.sleepFor = 0 }()
	pl := newPipeline(stage("slow", "", "x"),
		stage("notify-f", "failure", "nf"), stage("clean", "always", "cleanup"))
	// 起跑后 300ms 写取消 sentinel
	go func() {
		time.Sleep(300 * time.Millisecond)
		if err := writeCancelForTest("t"); err != nil {
			t.Error(err)
		}
	}()
	res := Run(context.Background(), pl, Options{Trigger: "manual"})
	if res.Status != "cancelled" {
		t.Fatalf("取消应判 cancelled 而非 failed: %+v", res)
	}
	want := []string{"x", "cleanup"} // failure 节点不跑，always 跑
	if fmt.Sprint(testComp.seq()) != fmt.Sprint(want) {
		t.Fatalf("取消后执行序列 = %v, want %v", testComp.seq(), want)
	}
}
```

（`writeCancelForTest` 直接调 `runstate.WriteCancel("t")`，包内可见即可。）

- [ ] **Step 2: 运行确认失败**

Run: `go test ./internal/pipeline/`
Expected: FAIL

- [ ] **Step 3: 实现 pipeline.go**

```go
// Package pipeline 是工作项执行引擎：按列表顺序跑主流程节点（fail-fast），
// 结束后按 when 条件跑后置节点（failure/always）。
//
// 超时层级：总预算 ctx（默认 30m）罩主流程；节点 timeout 是其子 ctx。
// 后置节点走「父 ctx + 10m 硬上限」——总预算耗尽/节点超时后清理仍要执行。
package pipeline

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/auto-deployer/auto-deployer/internal/components"
	"github.com/auto-deployer/auto-deployer/internal/config"
	"github.com/auto-deployer/auto-deployer/internal/logger"
	"github.com/auto-deployer/auto-deployer/internal/notify"
	"github.com/auto-deployer/auto-deployer/internal/param"
	"github.com/auto-deployer/auto-deployer/internal/runstate"
)

type Options struct {
	Trigger string // manual | webhook
	Args    map[string]string
	Pushers []string // webhook 队列合并进来的作者（含最新任务）
	Branch  string   // webhook 命中分支；手动为空（取 TriggerBranches 第一个）
	Commit  string
	Author  string
	Message string
	Console io.Writer // exec 前台终端；nil = 只写日志文件
	Cfg     *config.AppConfig
}

type StageResult struct {
	Name     string
	State    string // success | skipped | failed
	Duration string
}

type Result struct {
	Status      string // success | failed | cancelled
	FailedStage string
	Error       string
	Stages      []StageResult
}

const (
	defaultTimeout  = 30 * time.Minute
	postBudget      = 10 * time.Minute // ponytail: 后置节点硬上限；不够时按节点 timeout 细化
	cancelPollEvery = 500 * time.Millisecond
)

func Run(ctx context.Context, pl *config.PipelineConfig, opts Options) *Result {
	log := logger.GetServiceLogger(pl.Name)
	out := io.Writer(log)
	if opts.Console != nil {
		out = io.MultiWriter(log, opts.Console)
	}
	say := func(format string, a ...any) {
		log.Printf(format, a...)
		if opts.Console != nil {
			fmt.Fprintf(opts.Console, format+"\n", a...)
		}
	}

	branch := opts.Branch
	if branch == "" && len(pl.TriggerBranches) > 0 {
		branch = pl.TriggerBranches[0]
	}

	st := &runstate.RunState{
		State: "running", Trigger: opts.Trigger, PID: os.Getpid(),
		StartedAt: time.Now(), Branch: branch, Commit: opts.Commit,
	}
	_ = runstate.ClearCancel(pl.Name)
	_ = runstate.Save(pl.Name, st)

	say("=== %s %s [%s %s %s] ===", time.Now().Format("2006-01-02 15:04:05"), pl.Name, opts.Trigger, branch, opts.Commit)

	ps := param.New()
	for k, v := range pl.Env {
		ps.Env[k] = v
	}
	for k, v := range opts.Args {
		ps.Args[k] = v
	}
	ps.System = map[string]string{
		"name": pl.Name, "trigger": opts.Trigger, "branch": branch,
		"commit": opts.Commit, "author": opts.Author, "message": opts.Message,
		"pushers": strings.Join(opts.Pushers, ","), "workspace": pl.Workspace,
	}

	total, err := pl.TimeoutDuration()
	if err != nil || total <= 0 {
		total = defaultTimeout
	}
	mainCtx, cancelAll := context.WithTimeout(ctx, total)
	defer cancelAll()
	go watchCancel(mainCtx, pl.Name, cancelAll, log)
	stopSig := make(chan os.Signal, 1)
	signal.Notify(stopSig, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(stopSig)
	go func() { select { case <-stopSig: cancelAll(); case <-mainCtx.Done(): } }()

	notifier := buildNotifier(opts.Cfg)
	res := &Result{}
	mainErr := runFlow(mainCtx, pl, ps, st, res, opts, out, say, notifier, false, "")

	switch {
	case mainCtx.Err() == context.Canceled:
		res.Status = "cancelled"
	case mainErr != nil:
		res.Status = "failed"
	default:
		res.Status = "success"
	}

	// 后置流程：走父 ctx——总预算耗尽后清理仍要执行（Review Focus #2）
	ps.System["result"] = res.Status
	ps.System["failed_stage"] = res.FailedStage
	postCtx, cancelPost := context.WithTimeout(ctx, postBudget)
	defer cancelPost()
	_ = runFlow(postCtx, pl, ps, st, res, opts, out, say, notifier, true, res.Status)

	now := time.Now()
	st.State = res.Status
	st.FailedStage = res.FailedStage
	st.FailureReason = res.Error
	st.FinishedAt = &now
	_ = runstate.Save(pl.Name, st)
	say("=== %s %s 耗时 %s ===", pl.Name, res.Status, time.Since(st.StartedAt).Round(time.Second))
	return res
}

// runFlow 跑一轮节点。isPost=false 只跑 when=="" 的主流程（fail-fast）；
// isPost=true 按 outcome 跑 when==always / failure 的后置节点（失败只记日志）。
func runFlow(ctx context.Context, pl *config.PipelineConfig, ps *param.Set,
	st *runstate.RunState, res *Result, opts Options, out io.Writer, say func(string, ...any),
	notifier *notify.Notifier, isPost bool, outcome string) error {

	for _, stage := range pl.Stages {
		if !isPost && stage.When != "" {
			continue // 主流程只跑 when 缺省节点
		}
		if isPost {
			if stage.When == "always" {
				// 恒跑
			} else if stage.When == "failure" && outcome == "failed" {
				// 失败时跑（cancelled ≠ failed）
			} else {
				continue
			}
		}

		skip, err := ps.SkipTrue(stage.Skip)
		if err != nil {
			return failStage(stage, res, st, fmt.Sprintf("skip 求值失败: %v", err))
		}
		if skip {
			record(st, res, stage.Name, "skipped", "")
			say("--- [%s] skipped ---", stage.Name)
			continue
		}

		stageCtx := ctx
		if d, err := stage.TimeoutDuration(); err == nil && d > 0 {
			var cancel context.CancelFunc
			stageCtx, cancel = context.WithTimeout(ctx, d)
			defer cancel() // ponytail: 循环内 defer 会积压到函数尾；节点数少（个位数）可接受
		}

		params, err := ps.InterpolateParams(stage.Params)
		if err != nil {
			return failStage(stage, res, st, err.Error()) // Review Focus #1
		}
		applyDefaults(pl, stage, params, ps)
		comp, err := components.Get(stage.Type)
		if err != nil {
			return failStage(stage, res, st, err.Error())
		}

		say("--- [%s] %s ---", stage.Name, stage.Type)
		start := time.Now()
		results, err := comp.Run(stageCtx, components.Request{
			Params: params, Out: out, Notifier: notifier,
		})
		if err == nil {
			for k, raw := range stage.Output {
				v, oerr := ps.Interpolate(raw, results) // 裸名（如 ${stdout}）在这里生效
				if oerr != nil {
					err = fmt.Errorf("output.%s 求值失败: %w", k, oerr)
					break
				}
				ps.AddOutput(stage.Name, k, v)
			}
		}
		dur := time.Since(start).Round(time.Millisecond)

		if err != nil {
			if isPost {
				// 后置节点失败只记日志，不改变最终状态、不递归
				say("--- [%s] 后置节点失败（忽略）: %v ---", stage.Name, err)
				record(st, res, stage.Name, "failed", dur.String())
				continue
			}
			return failStage(stage, res, st, err.Error())
		}
		record(st, res, stage.Name, "success", dur.String())
		say("--- [%s] 完成 %s ---", stage.Name, dur)
	}
	return nil
}

// applyDefaults 注入组件缺省值（插值后、执行前）：
// shell.cwd / git.workspace / cleanup.workspace → 工作项 workspace；
// git.branch 列表 → 具体分支（system.branch 命中列表用之，否则第一个）。
func applyDefaults(pl *config.PipelineConfig, stage config.StageConfig, params map[string]any, ps *param.Set) {
	switch stage.Type {
	case "shell":
		if _, ok := params["cwd"]; !ok {
			params["cwd"] = pl.Workspace
		}
	case "git":
		if _, ok := params["workspace"]; !ok {
			params["workspace"] = pl.Workspace
		}
		if list, ok := params["branch"].([]any); ok {
			concrete := firstOr(ps.System["branch"], list)
			params["branch"] = concrete
		}
	case "cleanup":
		if _, ok := params["workspace"]; !ok {
			params["workspace"] = pl.Workspace
		}
	}
}

func firstOr(concrete string, list []any) string {
	for _, b := range list {
		if s, _ := b.(string); s == concrete && concrete != "" {
			return concrete
		}
	}
	if len(list) > 0 {
		if s, ok := list[0].(string); ok {
			return s
		}
	}
	return concrete
}

func record(st *runstate.RunState, res *Result, name, state, dur string) {
	st.Stages = append(st.Stages, runstate.StageState{Name: name, State: state, Duration: dur})
	res.Stages = append(res.Stages, StageResult{Name: name, State: state, Duration: dur})
}

func failStage(stage config.StageConfig, res *Result, st *runstate.RunState, reason string) error {
	res.FailedStage = stage.Name
	res.Error = reason
	record(st, res, stage.Name, "failed", "")
	return fmt.Errorf("%s: %s", stage.Name, reason)
}

// watchCancel 轮询取消 sentinel，命中即 cancel（沿用旧 deploy 的机制与节奏）。
func watchCancel(ctx context.Context, name string, cancel context.CancelFunc, log *logger.Logger) {
	ticker := time.NewTicker(cancelPollEvery)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if runstate.HasCancel(name) {
				log.Printf("收到取消信号，中止 %s", name)
				_ = runstate.ClearCancel(name)
				cancel()
				return
			}
		}
	}
}

func buildNotifier(cfg *config.AppConfig) *notify.Notifier {
	if cfg == nil {
		return nil
	}
	hasSMTP := cfg.SMTP.Host != ""
	hasResend := cfg.Resend.APIKey != ""
	if !hasSMTP && !hasResend {
		return nil
	}
	return notify.New(cfg.SMTP.Host, cfg.SMTP.Port, cfg.SMTP.Username, cfg.SMTP.Token,
		cfg.SMTP.TLS, cfg.Resend.APIKey, cfg.Resend.From)
}
```

注意两个实现细节：
- `runFlow` 循环内 `defer cancel()` 会积压到函数返回——节点数是个位数且每节点 ctx 很轻，加 ponytail 注释即可接受；若想干净可包一层闭包。
- email 组件的 `to` 缺省值（全局 notifications.to + pushers）：在 `applyDefaults` 加 `case "email":`，`params["to"]` 缺失时拼 `cfg.Notifications.To + strings.Split(ps.System["pushers"], ",")`——需要把 `opts.Cfg` 传进 applyDefaults（或预拼好放在 ps.System["default_to"]）。实现取后者：Run 里 `ps.System["default_to"] = join(append(cfg.Notifications.To, pushers...), ", ")`，applyDefaults 的 email case 读它。

- [ ] **Step 4: 运行测试通过**

Run: `go test ./internal/pipeline/ -v`
Expected: PASS 全部（重点：TestTotalTimeoutStillRunsAlways / TestStageTimeout / TestCancelRunsAlwaysNotFailure / TestUndefinedRefFailsStage）

- [ ] **Step 5: Commit**

```bash
git add internal/pipeline/
git commit -m "feat(pipeline): 执行引擎——fail-fast 主流程 + when 后置流程 + 超时层级 + 取消"
```

---

### Task 9: webhook + daemon + queue 切换到 pipelines

**Files:**
- Modify: `internal/webhook/server.go`（MatchPipelines / ExecutePipeline / payload 增加 commit+message）
- Modify: `internal/deployqueue/scheduler.go`（Task.ServiceName → Task.PipelineName，机械替换）
- Modify: `internal/webhook/server_test.go`、`internal/deployqueue/scheduler_test.go`（适配）
- Modify: `internal/daemon/daemon.go`（遍历 cfg.Pipelines；删除 registry 引用）

**Interfaces:**
- Consumes: Task 7 `FindPipeline`/`TriggerURL`/`TriggerBranches`；Task 8 `pipeline.Run/Options`
- Produces:
  - `func MatchPipelines(pipelines []config.PipelineConfig, result *DispatchResult) []*config.PipelineConfig` — URL 双方过 `build.HTTPSToSSH` 归一化后相等；branch 用 `path.Match`（无通配符时即精确匹配，`release/*` 这类 glob 生效）比对 `result.Branch`，命中任一 pattern 即匹配
  - `DispatchResult` 增加 `Commit string`、`Message string`（从 payload 第一个 commit 的 `id`/`message` 提取；`GitHubCommit` 增加 `ID string \`json:"id"\`` 与 `Message string \`json:"message"\`` 字段——GitHub/Gitee 同构）
  - `func ExecutePipeline(ctx context.Context, task deployqueue.Task, pushers []string) error` — 替代 ExecuteDeploy：loadConfig → FindPipeline → `pipeline.Run(ctx, pl, pipeline.Options{Trigger:"webhook", Pushers:pushers, Branch:task.Branch, Commit:task.Commit, Author:task.AuthorEmail, Message:task.Message, Cfg:cfg})`
  - `deployqueue.Task{PipelineName, Branch, RepoURL, AuthorEmail, Commit, Message, Source string}`（Source 存 "github"/"gitee"）
  - daemon.Start：`for _, p := range cfg.Pipelines`（MkdirAll workspace + EnsureGitConfig）；scheduler 构造改 `deployqueue.NewScheduler(webhook.ExecutePipeline)`

- [ ] **Step 1: 改造 scheduler（机械替换）**

`Task.ServiceName` → `Task.PipelineName`；`scheduler_test.go` 同步替换。新增字段 Commit/Message。跑 `go test ./internal/deployqueue/`，应仍绿。

- [ ] **Step 2: 写 webhook 失败测试（server_test.go 追加/改写）**

```go
func TestMatchPipelines(t *testing.T) {
	pipelines := []config.PipelineConfig{
		{Name: "app", TriggerURL: "https://github.com/u/r.git", TriggerBranches: []string{"main"}},
		{Name: "app-glob", TriggerURL: "https://github.com/u/r.git", TriggerBranches: []string{"release/*"}},
		{Name: "other-repo", TriggerURL: "https://github.com/u/other.git", TriggerBranches: []string{"main"}},
		{Name: "manual-only", TriggerURL: "", TriggerBranches: nil}, // 无 git 节点
	}
	cases := []struct {
		branch string
		want   []string
	}{
		{"main", []string{"app"}},                        // 精确命中
		{"release/1.2", []string{"app-glob"}},            // glob 命中
		{"dev", nil},                                     // 无命中
	}
	for _, c := range cases {
		got := MatchPipelines(pipelines, &DispatchResult{
			RepoURL: "https://github.com/u/r.git", Branch: c.branch,
		})
		var names []string
		for _, p := range got {
			names = append(names, p.Name)
		}
		if fmt.Sprint(names) != fmt.Sprint(c.want) {
			t.Errorf("branch=%s got %v want %v", c.branch, names, c.want)
		}
	}
}

func TestParsePayloadCommitMessage(t *testing.T) {
	body := []byte(`{"ref":"refs/heads/main","repository":{"clone_url":"https://github.com/u/r.git"},
		"commits":[{"id":"abc123def","message":"fix: something","author":{"email":"a@b.c"}}]}`)
	res, err := ParsePayload(body, "github")
	if err != nil {
		t.Fatal(err)
	}
	if res.Commit != "abc123def" || res.Message != "fix: something" || res.AuthorEmail != "a@b.c" {
		t.Fatalf("res=%+v", res)
	}
}
```

- [ ] **Step 3: 运行确认失败**

Run: `go test ./internal/webhook/`
Expected: FAIL

- [ ] **Step 4: 实现 server.go 改造**

要点（旧 MatchServices/ExecuteDeploy/registry 引用全部删除）：

```go
// MatchPipelines 返回 URL（双方 HTTPSToSSH 归一化）与 branch（path.Match，
// 无通配符即精确）命中的所有工作项——monorepo 一 push 多工作项各自独立入队。
func MatchPipelines(pipelines []config.PipelineConfig, result *DispatchResult) []*config.PipelineConfig {
	pushURL := build.HTTPSToSSH(result.RepoURL)
	var matched []*config.PipelineConfig
	for i := range pipelines {
		p := &pipelines[i]
		if p.TriggerURL == "" {
			continue // 无 git 节点，仅手动
		}
		if build.HTTPSToSSH(p.TriggerURL) != pushURL {
			continue
		}
		for _, pattern := range p.TriggerBranches {
			if ok, _ := path.Match(pattern, result.Branch); ok {
				matched = append(matched, p)
				break
			}
		}
	}
	return matched
}
```

Handle 内：`matched := MatchPipelines(cfg.Pipelines, result)`，入队 task 带 `Commit: result.Commit, Message: result.Message`；删掉 `registry.Get(m.Type)` 检查（校验已在 daemon 启动期完成）。ExecutePipeline：

```go
func ExecutePipeline(ctx context.Context, task deployqueue.Task, pushers []string) error {
	cfg, err := loadConfig()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	pl := config.FindPipeline(cfg, task.PipelineName)
	if pl == nil {
		return fmt.Errorf("工作项 %s 不在配置中", task.PipelineName)
	}
	pipeline.Run(ctx, pl, pipeline.Options{
		Trigger: "webhook", Pushers: pushers, Branch: task.Branch,
		Commit: task.Commit, Author: task.AuthorEmail, Message: task.Message, Cfg: cfg,
	})
	return nil
}
```

`daemon.go`：`cfg.Services` 循环改 `cfg.Pipelines`（workspace MkdirAll + EnsureGitConfig 用 `p.Workspace`/`p.Name`）；`deployqueue.NewScheduler(webhook.ExecutePipeline)`。若 daemon/commands.go 编译因旧 services 引用报错，此处一并把 `Status()` 函数里 services 循环删掉（Task 10 会重写，先留 daemon pid 一行）。

- [ ] **Step 5: 全量测试 + Commit**

Run: `go build ./... && go test ./...`
Expected: PASS（旧 deploy 相关包不动仍绿）

```bash
git add internal/webhook/ internal/deployqueue/ internal/daemon/
git commit -m "feat(webhook): 切换到 pipelines 匹配与执行；queue 字段更名"
```

---

### Task 10: cmd — exec / status / cancel / logs

**Files:**
- Create: `cmd/exec.go`
- Modify: `cmd/status.go`（重写：daemon pid + 各工作项最后执行状态）
- Modify: `cmd/cancel.go`（文案改为工作项；逻辑不变）
- Modify: `cmd/logs.go` + `internal/daemon/commands.go`（Logs 默认显示最近一次 run 的分节）
- Modify: `internal/term/color.go`（新状态配色）
- Test: `cmd/exec_test.go`（args 解析单测）、`internal/term/color_test.go`（新状态）

**Interfaces:**
- Consumes: Task 8 `pipeline.Run/Options`；`config.FindPipeline`；`runstate.Read/StaleRecover`；`deploylock.TryAcquire/IsHeld`
- Produces:
  - `deployd exec <name> [-c cfg] [--]key=value...`：TryAcquire 抢锁（忙 → 报错退出码 1），Options{Trigger:"manual", Args, Console:os.Stdout, Cfg}；结果 failed/cancelled → 进程退出码 1
  - `parseExecArgs(args []string) (name string, kv map[string]string, err error)` — args[0]=name，其余每项形如 `--foo=bar` 或 `foo=bar`（前缀 `--` 可选），必须含 `=`，否则报错
  - status 输出：`deployd: running (pid N)` + 每行 `  <name>  <彩色状态> <trigger> <耗时> <commit短sha> <failed_stage>`
  - term 配色：success→绿、failed→红、cancelled→黄、running→蓝、未执行（空）→灰
  - logs：给定 name 且非 follow 时，默认从文件里最后一个 `=== ` run 头开始打印（`-n`/`-f` 语义不变）

- [ ] **Step 1: 写 exec 参数解析失败测试**

```go
package cmd

import "testing"

func TestParseExecArgs(t *testing.T) {
	name, kv, err := parseExecArgs([]string{"app", "--env=prod", "debug=true"})
	if err != nil || name != "app" || kv["env"] != "prod" || kv["debug"] != "true" {
		t.Fatalf("name=%q kv=%v err=%v", name, kv, err)
	}
	if _, _, err := parseExecArgs([]string{"app", "no-equals"}); err == nil {
		t.Fatal("无 = 的参数必须报错")
	}
	if _, _, err := parseExecArgs([]string{}); err == nil {
		t.Fatal("缺工作项名必须报错")
	}
}
```

- [ ] **Step 2: 运行确认失败**

Run: `go test ./cmd/ -run TestParseExecArgs`
Expected: FAIL

- [ ] **Step 3: 实现 exec.go**

```go
package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/auto-deployer/auto-deployer/internal/config"
	"github.com/auto-deployer/auto-deployer/internal/deploylock"
	"github.com/auto-deployer/auto-deployer/internal/pipeline"
	"github.com/spf13/cobra"
)

func init() {
	// interspersed=false：name 之后的一切都是 key=value 参数（--foo=bar 形式），
	// 不再被 cobra 当未知 flag 拒绝
	execCmd.Flags().SetInterspersed(false)
	execCmd.Flags().StringVarP(&configFile, "config", "c", "", "config file path")
	rootCmd.AddCommand(execCmd)
}

var execCmd = &cobra.Command{
	Use:   "exec <pipeline_name> [--key=value ...]",
	Short: "前台执行工作项（实时输出，Ctrl+C 取消）",
	Args:  cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name, kv, err := parseExecArgs(args)
		if err != nil {
			return err
		}
		path := configFile
		if path == "" {
			path = config.DefaultConfig()
		}
		if path == "" {
			return fmt.Errorf("config file not found")
		}
		cfg, err := config.Load(path)
		if err != nil {
			return fmt.Errorf("failed to load config: %w", err)
		}
		pl := config.FindPipeline(cfg, name)
		if pl == nil {
			return fmt.Errorf("工作项 %q 不在配置中", name)
		}
		// 手动执行优先级最低：队列忙（pending 或 in-flight）直接拒绝，不排队
		lock, err := deploylock.TryAcquire(name)
		if err != nil {
			return fmt.Errorf("工作项 %s 正在执行或排队中，请勿重复操作", name)
		}
		defer lock.Release()

		res := pipeline.Run(cmd.Context(), pl, pipeline.Options{
			Trigger: "manual", Args: kv, Console: os.Stdout, Cfg: cfg,
		})
		if res.Status != "success" {
			os.Exit(1) // 失败/取消非零退出码，供脚本判断
		}
		return nil
	},
}

// parseExecArgs 解析 exec 的位置参数：args[0]=工作项名，其余为 --k=v / k=v。
func parseExecArgs(args []string) (string, map[string]string, error) {
	if len(args) == 0 {
		return "", nil, fmt.Errorf("缺少工作项名")
	}
	kv := map[string]string{}
	for _, a := range args[1:] {
		a = strings.TrimPrefix(a, "--")
		k, v, ok := strings.Cut(a, "=")
		if !ok || k == "" {
			return "", nil, fmt.Errorf("参数 %q 需要 key=value 形式", a)
		}
		kv[k] = v
	}
	return args[0], kv, nil
}
```

- [ ] **Step 4: 重写 status.go + cancel.go 文案 + term 配色 + logs 最近 run**

`status.go` 重写（daemon 自身 PID 探测暂继续用 `internal/process.NewManager`，Task 11 才删该包）：

```go
var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "查看所有工作项最后执行状态",
	RunE: func(cmd *cobra.Command, args []string) error {
		home, _ := os.UserHomeDir()
		mgr := process.NewManager(filepath.Join(home, ".deployd", "run", "deployd.pid"))
		fmt.Printf("deployd: %s\n", mgr.Status())

		path := configFile
		if path == "" {
			path = config.DefaultConfig()
		}
		if path == "" {
			return nil
		}
		cfg, err := config.Load(path)
		if err != nil {
			return fmt.Errorf("failed to load config: %w", err)
		}
		for i := range cfg.Pipelines {
			p := &cfg.Pipelines[i]
			st := runstate.StaleRecover(p.Name) // running 且锁空 → 就地改写 failed
			fmt.Printf("  %-30s %s\n", p.Name, pipelineStatusLine(st))
		}
		return nil
	},
}

// pipelineStatusLine 拼一行状态摘要：彩色状态 + 触发方式 + 耗时 + commit + 失败节点。
func pipelineStatusLine(st runstate.RunState) string {
	if st.State == "" {
		return term.Colorize("never") // 从未执行
	}
	line := term.Colorize(st.State)
	if st.Trigger != "" {
		line += "  " + st.Trigger
	}
	if st.FinishedAt != nil {
		line += "  " + st.FinishedAt.Sub(st.StartedAt).Round(time.Second).String()
	}
	if len(st.Commit) >= 7 {
		line += "  " + st.Commit[:7]
	}
	if st.FailedStage != "" {
		line += "  失败节点: " + st.FailedStage
	}
	return line
}
```

`term/color.go` 的 `colorFor` 改为：

```go
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
```

`color_test.go` 补一条：`Colorize("success")` 非 TTY 时原样返回（沿用现有 defaultIsTTY 覆盖手法）。

`daemon/commands.go` 的 `Logs`：`serviceName != ""` 且 `!follow` 且 `tail == 0` 时，读文件后从**最后一次出现的 `\n=== `** 开始打印（实现：`bytes.LastIndex(data, []byte("\n=== "))`，命中则 `data = data[idx+1:]`；没命中则整文件——旧格式日志兼容）。cancel.go：文案把「服务」改「工作项」即可。

- [ ] **Step 5: 构建 + 全量测试 + Commit**

Run: `go build ./... && go test ./...`
Expected: PASS（若旧 cmd 测试引用了 deploy/svc 相关 helper 报错，先 `go test ./cmd/ -run TestParseExecArgs` 通过即可，旧文件 Task 11 删除）

```bash
git add cmd/ internal/term/ internal/daemon/
git commit -m "feat(cmd): exec 前台执行 + status 显示工作项执行状态 + logs 最近 run 分节"
```

---

### Task 11: 删除旧实现 + wizard 改造

**Files:**
- Delete: `plugins/`（整个目录）、`internal/registry/`、`internal/process/`、`internal/servstate/`、`internal/deploy/`、`cmd/deploy.go`、`cmd/restart.go`、`cmd/service_start.go`、`cmd/service_stop.go`、`cmd/service_restart.go`、`cmd/cancel_test.go`（若引用旧包）、对应 `*_test.go`
- Delete: `internal/build/artifact.go`、`internal/build/java.go`；`internal/build/executor.go` 只留 `MergeEnv`/`RunCommandCtx`（删 ExecuteBuild/DetectJavaVersion）；`internal/build/git.go` 删掉未被引用的 `Clone`（git 组件只用 Fetch）与 `GetLatestAuthorEmail`/`GetLatestCommit`
- Modify: `cmd/root.go`（删 serviceCmd/svc 及 runSvcShortFlags 等）、`internal/config/config.go`（删 ServiceConfig/RepoConfig/BuildConfig/Command/parseCommonDeployFields/encodeNode/StrictDecodeDeploy/warnLegacy*；AppConfig 删 Services 字段）、`internal/config/validate.go`（删 services 旧校验段）、`internal/config/wizard.go`（重写为生成 pipeline 格式）、`internal/daemon/daemon.go` 与 `commands.go`（daemon 自己的 PID 管理内置到 daemon 包）
- Test: `internal/daemon/pid_test.go`（新增）

**Interfaces:**
- Consumes: 无新依赖
- Produces:
  - `internal/daemon` 新增 `pid.go`：`type PIDFile struct{ path string }`、`NewPIDFile(path string) *PIDFile`、`func (p *PIDFile) Status() string`（running/stopped/unknown）、`func (p *PIDFile) Write(pid int) error`、`func (p *PIDFile) Stop() error`（SIGTERM→10s→SIGKILL，从旧 process.Manager 精简移植——只管 daemon 自己的 pid，无进程组/PID 管理语义）
  - wizard 新输出：pipelines 格式（git + shell 构建 + shell 部署 + email success/failure + cleanup always 的完整模板）

- [ ] **Step 1: 新增 daemon/pid.go + 测试（先写测试）**

```go
package daemon

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestPIDFileLifecycle(t *testing.T) {
	p := NewPIDFile(filepath.Join(t.TempDir(), "d.pid"))
	if p.Status() != "stopped" {
		t.Fatalf("无 pid 文件应为 stopped: %s", p.Status())
	}
	if err := p.Write(os.Getpid()); err != nil {
		t.Fatal(err)
	}
	if p.Status() != "running" {
		t.Fatalf("写当前进程 pid 应为 running: %s", p.Status())
	}
	// 停一个不存在的进程组：写入一个必然不存在的 pid 后 Stop 不应挂死
	_ = os.WriteFile(p.path, []byte("999999"), 0644)
	done := make(chan error, 1)
	go func() { done <- p.Stop() }()
	select {
	case <-done:
	case <-time.After(15 * time.Second):
		_ = syscall.Kill(os.Getpid(), syscall.SIGKILL)
		t.Fatal("Stop 对已死 pid 不应挂死")
	}
}
```

实现 `pid.go`：从 `internal/process/manager.go` 移植 ReadPID/WritePID/Status/Stop（signalPid/waitPidGone/pidAlive 原样带走，删 Start/StartShell/Alive/SetOutput 等）。`daemon.go` 的 `mgr := process.NewManager(pidFile)` 改 `NewPIDFile(pidFile)`（Status/WritePID→Write/CleanupPID 适配）；`commands.go` 的 Stop 同理；`cmd/status.go`、`cmd/start.go`/`cmd/stop.go` 里的 process 引用全部换成 daemon 包导出（必要时在 daemon 包加 `func Status() string` / `func Stop() error` 包装，cmd 调它们）。

- [ ] **Step 2: 删除旧包与旧命令**

```bash
git rm -r plugins internal/registry internal/process internal/servstate internal/deploy
git rm cmd/deploy.go cmd/restart.go cmd/service_start.go cmd/service_stop.go cmd/service_restart.go
git rm internal/build/artifact.go internal/build/java.go
```

`cmd/root.go`：删 serviceCmd/init 注册/runSvcShortFlags/runSvcStart/runSvcRestart/boolFlag 及 deploy/registry/deploy imports。`internal/webhook/server.go` 若残留 registry/deploy import 已在 Task 9 清掉，此处 `go build ./...` 逐个修编译错误：
- `internal/config`：删 Services 及全部旧类型/函数（grep `ServiceConfig` 清引用）；`warnUnknownFields` 的严格解码结构体随 AppConfig 缩小自动生效
- `internal/config/validate.go`：只留 Task 7 的 pipeline 段 + 通知配置段
- 旧测试文件随对应源文件删除（`config_test.go` 里 services 用例删掉）

- [ ] **Step 3: 重写 wizard.go**

问题集（全部 ask() 单行输入，沿用现有 ask helper）：端口/监听地址、工作项名、git 仓库 URL、分支（默认 main）、workspace、构建命令（默认 `mvn package -DskipTests`）、部署命令（默认 `java -jar target/*.jar` 替换为提示用户填写）、SMTP 三件套（沿用现有问题）。生成 YAML：

```yaml
# deployd 配置（由 deployd config 生成）
server:
  host: "0.0.0.0"
  port: 9527

smtp:
  host: "smtp.qq.com"
  port: 465
  username: "..."
  token: "..."
  tls: true

notifications:
  to: ["admin@example.com"]

pipelines:
  - name: "my-app"
    workspace: "/opt/deployd/apps/my-app"
    timeout: "45m"
    stages:
      - name: 拉取代码
        type: git
        params:
          url: "https://github.com/u/r.git"
          branch: ["main"]
      - name: 构建
        type: shell
        params:
          sh: "mvn package -DskipTests"
      - name: 部署
        type: shell
        params:
          sh: "systemctl restart my-app"
      - name: 成功通知
        type: email
        params:
          subject: "${system.name} 部署成功 ${system.commit}"
          body: "分支 ${system.branch} 由 ${system.author} 触发"
      - name: 失败通知
        type: email
        when: failure
        params:
          subject: "${system.name} 部署失败于 ${system.failed_stage}"
          body: "${system.message}"
      - name: 清理工作空间
        type: cleanup
        when: always
```

（wizard 生成逻辑就是把上面的模板按用户答案 `fmt.Fprintf` 出去——比旧 wizard 的分模型 writeDeployBlock 简单得多。`wizard_test.go` 改成断言输出含 `pipelines:`/`type: git`/`when: always` 三处关键行。）

- [ ] **Step 4: 全量构建与测试**

Run: `go build ./... && go vet ./... && go test ./...`
Expected: PASS，且 `grep -r "ServiceConfig\|registry.Get\|servstate\." --include='*.go' .` 无残留

- [ ] **Step 5: Commit**

```bash
git add -A
git commit -m "refactor!: 删除服务类型插件体系，daemon PID 管理内置，向导改生成 pipeline 格式"
```

---

### Task 12: 示例配置 + README + 迁移文档

**Files:**
- Modify: `config.yaml.example`（整文件重写为 pipeline 格式，含四种组件全部用法注释）
- Modify: `README.md`（重写：架构、四套参数表、组件表、when 语义、CLI 用法、兼容矩阵）
- Create: `docs/migration-v2.md`（旧 services → pipelines 迁移对照）

**Interfaces:** 无代码。内容要求：

- `config.yaml.example`：一个完整的 springboot 工作项（git → 构建 → 部署 → 成功通知 → 失败通知 → 清理 always）+ 一个注释掉的无 git 节点纯脚本工作项示例 + `${output.构建.xxx}`/`${args.xxx}`/`skip` 的用法注释。**凭据占位不留真实值**（wiki 硬规则：凭据永不入文档）。
- README 关键段落：状态含义表（success/failed/cancelled/running/never）、`deployd exec` 的 key=value 传参、总/节点两级超时、取消语义（cancel 后 always 仍跑）、队列合并与锁、与旧版差异表（照抄 spec §11 兼容矩阵）。
- `docs/migration-v2.md`：五种旧 type 各给一个 stages 改写示例（jvm：shell 里的 mvn + cp + 重启命令要自己写就绪轮询 curl 循环；static：shell 里 rsync dist + nginx -s reload；node/python/docker 同理）。

- [ ] **Step 1: 写三个文档**（内容如上，直接编辑文件）
- [ ] **Step 2: 校验示例配置可被工具解析**

Run: `go run . exec --config config.yaml.example no-such-pipeline 2>&1 | head -3`
Expected: 报「工作项 no-such-pipeline 不在配置中」（证明 example YAML 语法/结构能通过 Load+校验，而非解析报错）

- [ ] **Step 3: Commit**

```bash
git add config.yaml.example README.md docs/migration-v2.md
git commit -m "docs: pipeline 格式示例配置 + README 重写 + 旧配置迁移指南"
```

---

### Task 13: e2e — webhook → 队列 → 流水线全链路

**Files:**
- Delete/ Rewrite: `test/e2e_test.go`

**Interfaces:** 无。这是最后一道集成闸门：起真 daemon、发真 webhook payload、断言状态文件与日志。

- [ ] **Step 1: 重写 e2e_test.go**

```go
//go:build e2e

package test

// 轻量 e2e：本地裸仓库 + 临时 HOME + 真 daemon 进程 + 真 HTTP webhook。
// 跳过条件：git 不可用。跑法：go test -tags e2e ./test/
// （OrbStack ubuntu22 上的完整手工验证步骤见 wiki runtime-environment 页）。
```

测试流程（实现要点）：
1. `t.Setenv("HOME", t.TempDir())`；`newBareRepo` 手法建本地仓库（从 Task 4 git_test 复制 helper）并 push 一个 commit
2. 写 config.yaml：`server.host=127.0.0.1`、随机端口；pipeline：git（本地 bare 路径）→ shell（`echo built > ${system.workspace}/marker`）→ shell `cat marker`（output 断言材料）→ cleanup when:always（keep .git + marker 留给断言？keep 不能留 marker——断言改用日志/状态文件，cleanup keep [".git"]）
3. `exec.Command(binary)` 起 `deployd start -c <cfg> --no-fork`（若 start 命令无 --no-fork flag 则直接 `go run . start -c` 后台 + 结束时 kill 进程组；参考 cmd/start.go 现有 fork 逻辑，macOS 本就前台）
4. `http.Post` GitHub 格式 payload（`X-GitHub-Event: push`，clone_url 填本地 bare 路径，ref=refs/heads/main，commits[0].id/email）
5. 轮询（≤30s，500ms 间隔）`~/.deployd/run/<name>.status` 变为 `success` 且 Stages 顺序 = 拉取/构建/清理
6. 断言日志文件含 `--- [构建] ---` 分节与 shell 输出；断言 `changed=true` output 未被 cleanup 误删（读状态文件 stages 全 success）
7. 再发第二个 webhook 触发合并（立即连发两个）→ 断言最终仍 success 且日志出现「合并」行

- [ ] **Step 2: 本地跑通**

Run: `go test -tags e2e ./test/ -v -timeout 120s`
Expected: PASS（macOS 本地即可跑，不需要 OrbStack；OrbStack 上的完整部署演练是发布前的手工步骤）

- [ ] **Step 3: Commit**

```bash
git add test/e2e_test.go
git commit -m "test(e2e): webhook→队列→流水线全链路集成测试"
```

---

## Self-Review 记录

- **Spec 覆盖**：§3 配置（T7）、§4 四套参数（T1/T8）、§5 when（T8）、§6 四组件（T3-T6）、§7 执行引擎/超时/取消/锁/队列（T8/T9）、§8 状态与日志（T2/T10）、§9 CLI（T10/T11）、§12 代码组织（T11）、§13 测试（各任务 + T13）、迁移文档（T12）。spec 偏差已注明：internal/build 缩减保留而非删除（Task 11）。
- **Review Focus 5 项**全部有对应测试：#1 T1/T8、#2 T8、#3 T8、#4 T1、#5 T3。
- **类型一致性**：param.Set.Interpolate(str, extra)、components.Request{Params/Out/Notifier}、runstate.RunState 字段、config.FindPipeline、pipeline.Options/Result 跨任务签名已逐一核对一致。
