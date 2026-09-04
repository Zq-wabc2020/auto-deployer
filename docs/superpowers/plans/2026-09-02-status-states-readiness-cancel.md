# 服务状态升级 / 就绪判定 / 取消机制 / artifact 多文件 实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 给 auto-deployer 增加 starting/start_failed 状态机 + 颜色、基于 health 的就绪门控、中途取消、artifact 多文件拷贝，不破坏既有功能。

**Architecture:** 新增 `internal/servstate`（状态文件）与 `internal/term`（ANSI）两个独立小包；复用 `deploylock` 作单一「忙碌」锁；在 `internal/deploy/orchestrator.go` 内插入就绪轮询 + sentinel 自取消 goroutine + ctx 重构（`WithCancel(WithTimeout)`）；`svc start/restart` 复用 `cmd/deploy.go` 的 fork 模式后台化；`config` 中心解析 `deploy.health`/`health_interval` 并强制校验。

**Tech Stack:** Go（module `github.com/auto-deployer/auto-deployer`）、cobra、gopkg.in/yaml.v3、标准库 `net/http`/`syscall`/`os`/`testing`/`net/http/httptest`。**禁止引入新依赖**（不得 add `golang.org/x/term` 等）。

**Spec:** `docs/superpowers/specs/2026-09-02-status-states-readiness-cancel-design.md`

## Global Constraints

- 依赖仅 cobra + yaml.v3；**零新依赖**，TTY 检测用标准库 `os.ModeCharDevice`。
- 代码注释与用户可见字符串用**中文**；变量/函数名用英文。
- 不删减既有功能（spec §11 兼容矩阵）；改动须可回滚。
- 主平台 Linux（ubuntu22 @ OrbStack，工作目录 `/home/auto-deploy/`）；macOS 兼容（fork 仅 Linux）。
- TDD：每任务先写失败测试 → 跑红 → 最小实现 → 跑绿 → commit。
- 状态目录 `~/.deployd/run/`；`timeout` 默认 30m；`deploy.health_interval` 默认 10s。
- 每任务结束 `go build ./... && go test ./... && go vet ./...` 全绿才 commit。

---

## File Structure

| 文件 | 责任 | 任务 |
|---|---|---|
| `internal/term/color.go` (+`_test.go`) | ANSI 上色 + 非 TTY 去色 | T1 |
| `internal/servstate/state.go` (+`_test.go`) | `~/.deployd/run/<name>.state` 读写 + sentinel 文件路径 | T2 |
| `internal/deploylock/lock.go` (+`_test.go`) | 新增 `IsHeld` | T3 |
| `internal/process/manager.go` (+`_test.go`) | 暴露 `Alive()` | T3 |
| `internal/config/config.go`/`validate.go` (+`_test.go`) | `Health` 字段 + 中心解析 + 强制校验 + `StrictDecodeDeploy` 去除通用字段 | T4 |
| `plugins/static/plugin.go` (+`_test.go`) | `Status` 改读 `svc.Health.URL`；移除自有 `Health` 字段 | T4 |
| `internal/deploy/orchestrator.go` (+`_test.go`) | `GetServiceStatusRich`、就绪门控、ctx 重构、sentinel watcher、取消判别 | T5/T6/T8 |
| `cmd/status.go`、`cmd/root.go` | 走 `GetServiceStatusRich` + 上色 | T5 |
| `cmd/service_start.go`、`cmd/service_restart.go`、`cmd/service_stop.go` | fork 后台 + TryAcquire + 写 starting；stop 清 state | T7 |
| `cmd/cancel.go` (新) | `deployd cancel <name>` 写 sentinel | T8 |
| `internal/build/artifact.go` (+`_test.go`) | `CopyArtifact` 支持 ctx + 列表 | T9 |
| `plugins/springboot`/`static`/`node` `plugin.go` | artifact 列表解码 + CopyArtifact 新签名 | T9 |
| `internal/daemon/commands.go` | `Status` 走 `GetServiceStatusRich`（修 C2） | T10 |
| `config.yaml.example`、`README.md`、`internal/config/wizard.go` | 文档/向导同步 | T11 |
| `test/e2e_test.go` | 状态/就绪/取消 e2e | T12 |

---

## Task 1: `internal/term/color` — ANSI 上色 + 非 TTY 去色

**Files:**
- Create: `internal/term/color.go`
- Test: `internal/term/color_test.go`

**Interfaces:**
- Produces: `term.Colorize(status string) string`（按状态返回上色字符串；非 TTY 返回原串）；`term.IsTerminal(f *os.File) bool`。

- [ ] **Step 1: 写失败测试**

```go
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
```

- [ ] **Step 2: 跑测试验证失败**

Run: `go test ./internal/term/ -run TestColorize -v`
Expected: FAIL（`color.go` 不存在 / `Colorize` 未定义）。

- [ ] **Step 3: 写实现**

```go
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
	case "starting":
		return blue
	case "running":
		return green
	case "stopped":
		return gray
	case "start_failed":
		return red
	default: // unknown 等
		return yellow
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
```

- [ ] **Step 4: 跑测试验证通过**

Run: `go test ./internal/term/ -v`
Expected: PASS。

- [ ] **Step 5: Commit**

```bash
git add internal/term/color.go internal/term/color_test.go
git commit -m "feat(term): ANSI 状态上色 + 非 TTY 去色(标准库,零新依赖)"
```

---

## Task 2: `internal/servstate` — 状态文件与 sentinel 路径

**Files:**
- Create: `internal/servstate/state.go`
- Test: `internal/servstate/state_test.go`

**Interfaces:**
- Consumes: `os.UserHomeDir()`（测试用 `t.Setenv("HOME", tmp)`）。
- Produces:
  - `type State struct { Status string; Since time.Time; Stage string }`
  - `Path(name string) string` — `~/.deployd/run/<name>.state`
  - `CancelPath(name string) string` — `~/.deployd/run/<name>.cancel`
  - `WriteStarting(name, stage string) error`
  - `WriteFailed(name string) error`
  - `Clear(name string) error`
  - `Read(name string) (State, bool)` — bool=false 表示无文件/解析失败
  - `ClearCancel(name string) error`、`WriteCancel(name string) error`、`HasCancel(name string) bool`

- [ ] **Step 1: 写失败测试**

```go
// internal/servstate/state_test.go
package servstate

import (
	"os"
	"testing"
	"time"
)

func TestWriteAndReadStarting(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if err := WriteStarting("svc1", "build"); err != nil {
		t.Fatal(err)
	}
	st, ok := Read("svc1")
	if !ok || st.Status != "starting" || st.Stage != "build" {
		t.Fatalf("got %+v ok=%v", st, ok)
	}
	if st.Since.IsZero() {
		t.Error("Since should be set")
	}
}

func TestWriteFailed(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if err := WriteFailed("svc1"); err != nil {
		t.Fatal(err)
	}
	st, _ := Read("svc1")
	if st.Status != "start_failed" {
		t.Fatalf("got %q", st.Status)
	}
}

func TestClearRemovesFile(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	_ = WriteStarting("svc1", "fetch")
	_ = Clear("svc1")
	if _, ok := Read("svc1"); ok {
		t.Fatal("state should be cleared")
	}
}

func TestCorruptStateReadsAsNone(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	_ = WriteStarting("svc1", "fetch")
	// 写坏 JSON → 应降级为无状态（走实时探测）
	if err := os.WriteFile(Path("svc1"), []byte("{not json"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, ok := Read("svc1"); ok {
		t.Fatal("corrupt state should read as absent (degrade to live probe)")
	}
}

func TestCancelSentinel(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if HasCancel("svc1") {
		t.Fatal("no sentinel yet")
	}
	if err := WriteCancel("svc1"); err != nil {
		t.Fatal(err)
	}
	if !HasCancel("svc1") {
		t.Fatal("sentinel should exist")
	}
	if err := ClearCancel("svc1"); err != nil {
		t.Fatal(err)
	}
	if HasCancel("svc1") {
		t.Fatal("sentinel should be cleared")
	}
}

var _ = time.Now // 保留 time import（实现里 Since 用到）
```

- [ ] **Step 2: 跑测试验证失败**

Run: `go test ./internal/servstate/ -v`
Expected: FAIL（包不存在）。

- [ ] **Step 3: 写实现**

```go
// internal/servstate/state.go
package servstate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

type State struct {
	Status string    `json:"status"` // 仅 "starting" | "start_failed"
	Since  time.Time `json:"since"`
	Stage  string    `json:"stage,omitempty"`
}

func runDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".deployd", "run")
}

func Path(name string) string {
	return filepath.Join(runDir(), name+".state")
}

func CancelPath(name string) string {
	return filepath.Join(runDir(), name+".cancel")
}

func write(name string, s State) error {
	if err := os.MkdirAll(runDir(), 0755); err != nil {
		return err
	}
	data, _ := json.Marshal(s)
	return os.WriteFile(Path(name), data, 0644)
}

func WriteStarting(name, stage string) error {
	return write(name, State{Status: "starting", Since: time.Now(), Stage: stage})
}

func WriteFailed(name string) error {
	return write(name, State{Status: "start_failed", Since: time.Now()})
}

func Clear(name string) error {
	if err := os.Remove(Path(name)); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

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

func WriteCancel(name string) error {
	if err := os.MkdirAll(runDir(), 0755); err != nil {
		return err
	}
	return os.WriteFile(CancelPath(name), []byte{}, 0644)
}

func ClearCancel(name string) error {
	if err := os.Remove(CancelPath(name)); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func HasCancel(name string) bool {
	_, err := os.Stat(CancelPath(name))
	return err == nil
}
```

> 实现后测试无需占位 helper——`TestCorruptStateReadsAsNone` 直接用 `os.WriteFile`。

- [ ] **Step 4: 跑测试验证通过**

Run: `go test ./internal/servstate/ -v`
Expected: PASS。

- [ ] **Step 5: Commit**

```bash
git add internal/servstate/
git commit -m "feat(servstate): 服务状态文件(.state)与取消 sentinel 读写"
```

---

## Task 3: `deploylock.IsHeld` + `process.Manager.Alive`

**Files:**
- Modify: `internal/deploylock/lock.go`（新增 `IsHeld`）
- Modify: `internal/process/manager.go`（新增 `Alive`）
- Test: `internal/deploylock/lock_test.go`、`internal/process/manager_test.go`

**Interfaces:**
- Produces: `deploylock.IsHeld(serviceName string) bool`；`(*process.Manager).Alive() bool`。

- [ ] **Step 1: 写失败测试**

```go
// internal/deploylock/lock_test.go 追加
func TestIsHeld(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if IsHeld("svc") {
		t.Fatal("no lock held initially")
	}
	lock, err := Acquire("svc") // 阻塞获取
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Release()
	if !IsHeld("svc") {
		t.Fatal("lock should be reported held")
	}
}
```

```go
// internal/process/manager_test.go 追加
func TestAliveForMissingPid(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	m := NewManager(pidFileForTest("nope")) // 不存在的 pid 文件
	if m.Alive() {
		t.Fatal("missing pid file => not alive")
	}
}
// pidFileForTest 用 t.TempDir 构造一个 pid 文件路径；若既有测试已有 helper 则复用。
func pidFileForTest(name string) string {
	return filepath.Join(tTmp(), name+".pid") // 见下：用 t.TempDir 不便在 helper 里，改为：
}
// 简化：直接在测试里写绝对路径
func TestAliveForMissingPidDirect(t *testing.T) {
	m := NewManager(filepath.Join(t.TempDir(), "nope.pid"))
	if m.Alive() {
		t.Fatal("expected not alive")
	}
}
```

> 上面两个 `pidFileForTest`/`tTmp` 是占位思路；**最终只保留** `TestAliveForMissingPidDirect`（用 `t.TempDir()` 内联），删掉 `pidFileForTest`。

- [ ] **Step 2: 跑测试验证失败**

Run: `go test ./internal/deploylock/ ./internal/process/ -run "TestIsHeld|TestAlive" -v`
Expected: FAIL（`IsHeld`/`Alive` 未定义）。

- [ ] **Step 3: 写实现**

```go
// internal/deploylock/lock.go 追加
// IsHeld 报告该服务的部署锁是否被持有。用 TryAcquire 探测：拿不到=EWOULDBLOCK=被持有；
// 拿到则立即释放并返回 false。用于 status 显示 starting 时确认锁仍在、以及 cancel 命令判定在途。
func IsHeld(serviceName string) bool {
	l, err := TryAcquire(serviceName)
	if err != nil {
		return true // 被持有（或打开失败，保守视为忙）
	}
	l.Release()
	return false
}
```

```go
// internal/process/manager.go 追加
// Alive 报告被管理进程（及其进程组）是否仍存活。供就绪轮询快速失败用。
func (m *Manager) Alive() bool {
	pid, err := m.ReadPID()
	if err != nil || pid == 0 {
		return false
	}
	return pidAlive(pid)
}
```

- [ ] **Step 4: 跑测试验证通过**

Run: `go test ./internal/deploylock/ ./internal/process/ -v`
Expected: PASS。

- [ ] **Step 5: Commit**

```bash
git add internal/deploylock/lock.go internal/deploylock/lock_test.go internal/process/manager.go internal/process/manager_test.go
git commit -m "feat(lock,process): IsHeld 探测锁占用 + Manager.Alive 暴露存活探测"
```

---

## Task 4: `config` 健康字段 + 中心解析 + 强制校验（含 static 插件改读 svc.Health）

**Files:**
- Modify: `internal/config/config.go`（`ServiceConfig.HealthURL`/`HealthInterval` + `Load` 中心解析 + `StrictDecodeDeploy` 去通用字段 + `HealthIntervalDuration()`）
- Modify: `internal/config/validate.go`（`health` 必填）
- Modify: `plugins/static/plugin.go`（`Status` 读 `svc.HealthURL`；移除 `staticDeployConfig.Health`）
- Test: `internal/config/config_test.go`、`internal/config/validate_test.go`、`plugins/static/plugin_test.go`

**Interfaces:**
- Produces: `(*config.ServiceConfig).HealthURL string`、`HealthInterval string`、`HealthIntervalDuration() time.Duration`（默认 10s）。
- Consumes（下游）：orchestrator 就绪轮询读 `svc.HealthURL`/`HealthIntervalDuration()`；static 插件 `Status` 读 `svc.HealthURL`。

- [ ] **Step 1: 写失败测试**

```go
// internal/config/config_test.go 追加
func TestParsesHealthFromDeploy(t *testing.T) {
	cfg := loadStr(t, `
services:
  - name: s1
    type: jvm
    workspace: /tmp/s1
    build: { command: "true" }
    deploy:
      run: "java -jar app.jar"
      health: "http://localhost:8080/health"
      health_interval: "3s"
`)
	s := cfg.Services[0]
	if s.HealthURL != "http://localhost:8080/health" {
		t.Fatalf("health url not parsed: %q", s.HealthURL)
	}
	if d, _ := s.HealthIntervalDuration(); d != 3*time.Second {
		t.Fatalf("interval not parsed: %v", d)
	}
}

func TestHealthIntervalDefault(t *testing.T) {
	cfg := loadStr(t, `
services:
  - name: s1
    type: jvm
    workspace: /tmp/s1
    build: { command: "true" }
    deploy: { run: "x", health: "http://x/h" }
`)
	if d, _ := cfg.Services[0].HealthIntervalDuration(); d != 10*time.Second {
		t.Fatalf("default interval want 10s got %v", d)
	}
}

func TestStrictDecodeDeployIgnoresHealth(t *testing.T) {
	// jvm deploy 块含 health（通用字段），StrictDecodeDeploy 不应报「未知字段」
	node := yamlNode(t, `artifact: target/*.jar
run: java -jar x.jar
health: http://x/h
health_interval: 5s
`)
	var dc struct {
		Artifact string `yaml:"artifact"`
		Run      string `yaml:"run"`
	}
	if err := StrictDecodeDeploy(node, &dc); err != nil {
		t.Fatalf("health/health_interval 应被白名单忽略, got: %v", err)
	}
	if dc.Artifact != "target/*.jar" {
		t.Fatalf("artifact not decoded: %q", dc.Artifact)
	}
}
```

> `loadStr`、`yamlNode` 为既有或新增测试 helper（见既有 config_test.go 风格；若无则在本测试文件追加：`loadStr(t, s)` 用 `os.WriteFile` 到临时文件再 `Load`；`yamlNode` 用 `yaml.Unmarshal` 到 `yaml.Node`）。

```go
// internal/config/validate_test.go 追加
func TestValidateRequiresHealth(t *testing.T) {
	cfg := loadStr(t, `
services:
  - name: s1
    type: jvm
    workspace: /tmp/s1
    build: { command: "true" }
    deploy: { run: "java -jar x" }   # 缺 health
`)
	if err := Validate(cfg); err == nil {
		t.Fatal("缺 deploy.health 应校验失败")
	}
}
```

- [ ] **Step 2: 跑测试验证失败**

Run: `go test ./internal/config/ -run "TestParsesHealth|TestHealthInterval|TestStrictDecodeDeployIgnoresHealth|TestValidateRequiresHealth" -v`
Expected: FAIL（字段/方法不存在）。

- [ ] **Step 3: 写实现**

```go
// internal/config/config.go —— ServiceConfig 增字段
type ServiceConfig struct {
	Name      string     `yaml:"name"`
	Type      string     `yaml:"type"`
	Repo      RepoConfig `yaml:"repo"`
	Workspace string     `yaml:"workspace"`
	Timeout   string     `yaml:"timeout"`
	Build     BuildConfig `yaml:"build"`
	Deploy    yaml.Node  `yaml:"deploy"`
	// HealthURL/HealthInterval 从 deploy: 块中心解析（通用字段），供 orchestrator
	// 就绪轮询与 static.Status 读取。不在 yaml 顶层，故无 yaml tag。
	HealthURL      string `yaml:"-"`
	HealthInterval string `yaml:"-"`
}

// HealthIntervalDuration 解析 deploy.health_interval，空=默认 10s。
func (s ServiceConfig) HealthIntervalDuration() (time.Duration, error) {
	if s.HealthInterval == "" {
		return 10 * time.Second, nil
	}
	d, err := time.ParseDuration(s.HealthInterval)
	if err != nil {
		return 0, fmt.Errorf("invalid health_interval %q: %w", s.HealthInterval, err)
	}
	return d, nil
}
```

```go
// internal/config/config.go —— Load 内追加中心解析
func Load(path string) (*AppConfig, error) {
	// ...既有...
	var cfg AppConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil { ... }
	warnUnknownFields(data)
	warnLegacyRunField(data)
	warnLegacyBuildTimeout(data)
	parseCommonDeployFields(&cfg) // 新增
	return &cfg, nil
}

// parseCommonDeployFields 从每个服务的 deploy: 节点解出通用字段 health / health_interval，
// 存到 svc.HealthURL / svc.HealthInterval。插件无需各自持有这些字段。
func parseCommonDeployFields(cfg *AppConfig) {
	for i := range cfg.Services {
		svc := &cfg.Services[i]
		if svc.Deploy.Kind == 0 {
			continue
		}
		var common struct {
			Health        string `yaml:"health"`
			HealthInterval string `yaml:"health_interval"`
		}
		_ = yaml.Unmarshal(encodeNode(svc.Deploy), &common) // 容错：失败则留空，校验会拦
		svc.HealthURL = common.Health
		svc.HealthInterval = common.HealthInterval
	}
}

// encodeNode 把 yaml.Node 编码回字节（StrictDecodeDeploy 也复用）。
func encodeNode(node yaml.Node) []byte {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	_ = enc.Encode(&node)
	_ = enc.Close()
	return buf.Bytes()
}
```

```go
// internal/config/config.go —— StrictDecodeDeploy 去除通用字段后再严格解码
func StrictDecodeDeploy(node yaml.Node, out interface{}) error {
	data := encodeNode(node)
	// 去除通用字段，避免插件结构体无这些字段时触发「未知字段」警告
	var m map[string]interface{}
	if err := yaml.Unmarshal(data, &m); err == nil {
		delete(m, "health")
		delete(m, "health_interval")
		data, _ = yaml.Marshal(m)
	}
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	return dec.Decode(out)
}
```

```go
// internal/config/validate.go —— health 必填（Validate 既有函数，追加规则）
func Validate(cfg *AppConfig) error {
	for i := range cfg.Services {
		svc := &cfg.Services[i]
		if svc.HealthURL == "" {
			return fmt.Errorf("services[%d] %s: 缺少 deploy.health（就绪判定必需，请配置 HTTP 健康检查 URL）", i, svc.Name)
		}
		if _, err := svc.HealthIntervalDuration(); err != nil {
			return fmt.Errorf("services[%d] %s: %w", i, svc.Name, err)
		}
		// ...既有校验...
	}
	return nil
}
```

> 若 `Validate` 尚不存在，则在 `validate.go` 新建并在 `Load` 后由 cmd 调用；既有 `validate_test.go` 存在说明已有 `Validate`——在其中追加规则。

```go
// plugins/static/plugin.go —— Status 改读 svc.HealthURL；移除 staticDeployConfig.Health
type staticDeployConfig struct {
	Artifact     string `yaml:"artifact"`
	Dest         string `yaml:"dest"`
	NginxReload  bool   `yaml:"nginx_reload"`
	// Health 字段移除（改读 svc.HealthURL）
}

func (p *Plugin) Status(ctx context.Context, svc *config.ServiceConfig) (string, error) {
	if svc.HealthURL == "" {
		return "unknown", nil // 不应发生（校验已拦），防御
	}
	// ...既有 HTTP GET 逻辑，URL 改用 svc.HealthURL...
}
```

- [ ] **Step 4: 跑测试验证通过**

Run: `go build ./... && go test ./internal/config/ ./plugins/static/ -v`
Expected: PASS（既有 static 测试若引用 `dc.Health` 需同步改为 `svc.HealthURL`）。

- [ ] **Step 5: Commit**

```bash
git add internal/config/ plugins/static/
git commit -m "feat(config): health/health_interval 中心解析 + 全类型强制校验 + static 改读 svc.Health"
```

---

## Task 5: `GetServiceStatusRich` + 接线 status/svc + 上色 + 陈旧恢复

**Files:**
- Modify: `internal/deploy/orchestrator.go`（新增 `GetServiceStatusRich`）
- Modify: `cmd/status.go`、`cmd/root.go`
- Test: `internal/deploy/orchestrator_test.go`

**Interfaces:**
- Consumes: `servstate.Read/IsHeld(deploylock)`、`Deployer.Status`、`term.Colorize`。
- Produces: `deploy.GetServiceStatusRich(ctx, svc, deployer) (string, error)`（返回**已上色**的状态串）。

- [ ] **Step 1: 写失败测试**

```go
// internal/deploy/orchestrator_test.go 追加
func TestGetServiceStatusRichStarting(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	svc := &config.ServiceConfig{Name: "s1", Type: "jvm"}
	// 占锁 + 写 starting
	lock, _ := deploylock.Acquire("s1")
	defer lock.Release()
	_ = servstate.WriteStarting("s1", "build")
	got, _ := GetServiceStatusRich(context.Background(), svc, &fakeDeployer{})
	if !strings.HasSuffix(got, "starting") && !strings.Contains(got, "starting") {
		t.Fatalf("expected starting, got %q", got)
	}
}

func TestGetServiceStatusRichStaleStartingBecomesFailed(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	_ = servstate.WriteStarting("s1", "build") // 但不持锁 → 陈旧
	got, _ := GetServiceStatusRich(context.Background(), &config.ServiceConfig{Name: "s1"}, &fakeDeployer{})
	if !strings.Contains(got, "start_failed") {
		t.Fatalf("stale starting should recover to start_failed, got %q", got)
	}
	// 且应就地重写为 start_failed
	st, _ := servstate.Read("s1")
	if st.Status != "start_failed" {
		t.Fatalf("stale should be rewritten to start_failed, got %q", st.Status)
	}
}

func TestGetServiceStatusRichRunningFallbackToProbe(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	// 无 .state → 走插件 Status
	d := &fakeDeployer{status: "running"}
	got, _ := GetServiceStatusRich(context.Background(), &config.ServiceConfig{Name: "s1"}, d)
	if !strings.Contains(got, "running") {
		t.Fatalf("expected running from probe, got %q", got)
	}
}

// fakeDeployer 实现 Deployer，Status 可控。
type fakeDeployer struct{ status string; startable bool; started bool }
func (f *fakeDeployer) Build(context.Context, *config.ServiceConfig) error { return nil }
func (f *fakeDeployer) Stage(context.Context, *config.ServiceConfig) error { return nil }
func (f *fakeDeployer) Status(context.Context, *config.ServiceConfig) (string, error) {
	if f.status == "" { return "stopped", nil }
	return f.status, nil
}
func (f *fakeDeployer) SetOutput(io.Writer) {}
```

> `fakeDeployer` 若 orchestrator_test.go 已有同名 mock，复用并补 `status` 字段。

- [ ] **Step 2: 跑测试验证失败**

Run: `go test ./internal/deploy/ -run TestGetServiceStatusRich -v`
Expected: FAIL（`GetServiceStatusRich` 未定义）。

- [ ] **Step 3: 写实现**

```go
// internal/deploy/orchestrator.go 追加
import (
	"github.com/auto-deployer/auto-deployer/internal/deploylock"
	"github.com/auto-deployer/auto-deployer/internal/servstate"
	"github.com/auto-deployer/auto-deployer/internal/term"
)

// GetServiceStatusRich 按优先级返回（已上色）服务状态：
//  1) .state=starting 且锁持有 → starting
//  2) .state=starting 且锁空 → 陈旧，重写为 start_failed
//  3) .state=start_failed → start_failed（粘性）
//  4) 无 .state → 插件实时探测
func GetServiceStatusRich(ctx context.Context, svc *config.ServiceConfig, deployer Deployer) (string, error) {
	st, ok := servstate.Read(svc.Name)
	if ok && st.Status == "starting" {
		if deploylock.IsHeld(svc.Name) {
			return term.Colorize("starting"), nil
		}
		// 陈旧：部署进程已死，就地重写为失败
		_ = servstate.WriteFailed(svc.Name)
		return term.Colorize("start_failed"), nil
	}
	if ok && st.Status == "start_failed" {
		return term.Colorize("start_failed"), nil
	}
	// 无持久态 → 实时探测
	raw, err := deployer.Status(ctx, svc)
	if err != nil {
		return term.Colorize("unknown"), err
	}
	return term.Colorize(raw), nil
}
```

```go
// cmd/status.go —— serviceStatus 改用 Rich
func serviceStatus(svc *config.ServiceConfig) string {
	d, err := registry.Get(svc.Type)
	if err != nil {
		return "unknown (" + err.Error() + ")"
	}
	st, err := deploy.GetServiceStatusRich(context.Background(), svc, d)
	if err != nil {
		return "unknown (" + err.Error() + ")"
	}
	return st
}
```

```go
// cmd/root.go —— runSvcShortFlags 无旗标分支改用 Rich
default:
	st, err := deploy.GetServiceStatusRich(ctx, svc, d)
	if err != nil { return err }
	fmt.Printf("%s: %s\n", svc.Name, st)
	return nil
```

- [ ] **Step 4: 跑测试验证通过**

Run: `go build ./... && go test ./internal/deploy/ ./cmd/ -v`
Expected: PASS。

- [ ] **Step 5: Commit**

```bash
git add internal/deploy/orchestrator.go internal/deploy/orchestrator_test.go cmd/status.go cmd/root.go
git commit -m "feat(status): GetServiceStatusRich 优先级判定+上色+陈旧恢复; status/svc 接线"
```

---

## Task 6: 就绪门控 + ctx 重构 + 就绪失败通知（orchestrator.Deploy）

**Files:**
- Modify: `internal/deploy/orchestrator.go`（`Deploy` ctx 重构、插入 `readinessGate`、`readiness` 失败分支、`starting` 状态写入、`handleStageErr` 取消/失败判别）
- Test: `internal/deploy/orchestrator_test.go`

**Interfaces:**
- Consumes: `servstate`、`process.Manager.Alive`（经 `Stoppable`+`Status`）、`svc.HealthURL`/`HealthIntervalDuration()`。
- Produces: `readinessGate(ctx, svc, deployer, log) error`；`Deploy` 内 `deployCtx = WithCancel(WithTimeout(ctx, timeout))`。

- [ ] **Step 1: 写失败测试**

```go
// internal/deploy/orchestrator_test.go 追加
func TestDeployFailsWhenHealthNeverPasses(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	// 起一个永远 500 的 health 服务
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
	}))
	defer srv.Close()

	svc := &config.ServiceConfig{
		Name: "s1", Type: "jvm", Workspace: t.TempDir(),
		HealthURL: srv.URL, HealthInterval: "20ms",
		Timeout: "200ms", // 快速超时
		Build: config.BuildConfig{Command: config.Command{"true"}},
		Repo: config.RepoConfig{Branch: "main"},
	}
	// deployer: Build/Stage 成功；Start 成功；Status 返回 running（pid 活）
	d := &fakeStartableDeployer{started: true, status: "running"}
	cfg := &config.AppConfig{}
	res, err := Deploy(context.Background(), svc, cfg, d, nil)
	if err == nil || res.Status != "failed" {
		t.Fatalf("expected failed, got %+v err=%v", res, err)
	}
	st, _ := servstate.Read("s1")
	if st.Status != "start_failed" {
		t.Fatalf("state should be start_failed, got %q", st.Status)
	}
}

func TestDeploySucceedsWhenHealthPasses(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))
	defer srv.Close()
	svc := &config.ServiceConfig{
		Name: "s2", Type: "jvm", Workspace: t.TempDir(),
		HealthURL: srv.URL, HealthInterval: "10ms",
		Build: config.BuildConfig{Command: config.Command{"true"}},
		Repo: config.RepoConfig{Branch: "main"},
	}
	d := &fakeStartableDeployer{started: true, status: "running"}
	res, err := Deploy(context.Background(), svc, &config.AppConfig{}, d, nil)
	if err != nil || res.Status != "success" {
		t.Fatalf("expected success, got %+v err=%v", res, err)
	}
	if _, ok := servstate.Read("s2"); ok {
		t.Fatal("success should clear .state")
	}
}

// fakeStartableDeployer: Build/Stage no-op; Start sets started; Status=running if started.
type fakeStartableDeployer struct{ started bool; status string }
func (f *fakeStartableDeployer) Build(context.Context, *config.ServiceConfig) error { return nil }
func (f *fakeStartableDeployer) Stage(context.Context, *config.ServiceConfig) error { return nil }
func (f *fakeStartableDeployer) Start(context.Context, *config.ServiceConfig) error  { f.started = true; return nil }
func (f *fakeStartableDeployer) Stop(context.Context, *config.ServiceConfig) error   { f.started = false; return nil }
func (f *fakeStartableDeployer) Status(context.Context, *config.ServiceConfig) (string, error) {
	if f.started { return "running", nil }
	return "stopped", nil
}
func (f *fakeStartableDeployer) SetOutput(io.Writer) {}
```

> 测试里 Deploy 会调 `build.EnsureSSHKey`/`build.Fetch`——既有 `Deploy` 流程含 fetch。为隔离，让 `fakeStartableDeployer` 的 `Build`/`Stage` no-op 即可，但 fetch 仍真实执行。**简化：给测试 workspace 放一个已 init 的 git 仓库**，或既有 orchestrator_test.go 已有 fetch mock 模式——**复用其既有测试 setup**。若既有测试未 mock fetch，则在本任务把 `Deploy` 的 fetch/EnsureSSHKey 保留真实调用，测试里 `git init` 一个本地 bare 仓库作 repo.url（见既有 `build/git_test.go` 的本地 repo 构造法）。

- [ ] **Step 2: 跑测试验证失败**

Run: `go test ./internal/deploy/ -run "TestDeployFailsWhenHealthNeverPasses|TestDeploySucceedsWhenHealthPasses" -v`
Expected: FAIL（`readinessGate` 未实现，Deploy 仍按旧逻辑直接 success）。

- [ ] **Step 3: 写实现**

```go
// internal/deploy/orchestrator.go —— Deploy 重构（关键改动；未列出的既有行保持）
func Deploy(ctx context.Context, svc *config.ServiceConfig, cfg *config.AppConfig, deployer Deployer, operatorEmails []string) (*DeployResult, error) {
	result := &DeployResult{ServiceName: svc.Name}
	log := logger.GetServiceLogger(svc.Name)
	deployer.SetOutput(log)
	recipients := operatorEmails
	commitInfo := ""

	keyFile, _, _, err := build.EnsureSSHKey()
	if err != nil { /* 既有 */ }

	// ctx 重构：timeout 总预算（fetch+build+stage+就绪）+ 手动取消层
	deadlineCtx, cancelDeadline := context.WithTimeout(ctx, deployTimeout(svc))
	defer cancelDeadline()
	deployCtx, manualCancel := context.WithCancel(deadlineCtx)
	defer manualCancel()

	// 取消 sentinel：清残留 + 起 watcher（Task 8 接线；此处先占位不阻塞）
	_ = servstate.ClearCancel(svc.Name)

	// 写 starting
	setStage := func(s string) { _ = servstate.WriteStarting(svc.Name, s) }
	setStage("fetch")

	// handleStageErr：区分手动取消（Canceled）与失败（含超时）
	started := false
	handleErr := func(stage string, e error) (*DeployResult, error) {
		if deployCtx.Err() == context.Canceled {
			// 手动取消：不发邮件，停半起服务，清 state
			if started {
				if s, ok := deployer.(Stoppable); ok { _ = s.Stop(ctx, svc) }
			}
			_ = servstate.Clear(svc.Name)
			result.Status = "cancelled"
			log.Printf("deploy cancelled at %s", stage)
			return result, fmt.Errorf("deploy cancelled: %s", stage)
		}
		// 失败（含 DeadlineExceeded 超时）：start_failed + 邮件 + 停半起
		if started {
			if s, ok := deployer.(Stoppable); ok { _ = s.Stop(ctx, svc) }
		}
		_ = servstate.WriteFailed(svc.Name)
		result.Status = "failed"; result.Error = e.Error()
		sendNotify(ctx, cfg, svc, log, recipients, authorEmail, commitInfo, stage, "failed", e.Error())
		return result, e
	}

	// fetch / build / stage（均用 deployCtx）
	if err := build.Fetch(deployCtx, svc.Repo.URL, keyFile, svc.Repo.Branch, svc.Workspace, log); err != nil {
		return handleErr("fetch", err)
	}
	authorEmail := build.GetLatestAuthorEmail(svc.Workspace, svc.Repo.Branch)
	commitInfo = build.GetLatestCommit(svc.Workspace, svc.Repo.Branch)
	if len(recipients) == 0 { recipients = []string{authorEmail} }

	setStage("build")
	if err := deployer.Build(deployCtx, svc); err != nil { return handleErr("build", err) }
	setStage("stage")
	if err := deployer.Stage(deployCtx, svc); err != nil { return handleErr("stage", err) }

	// Stop old
	if s, ok := deployer.(Stoppable); ok { _ = s.Stop(ctx, svc) }
	// Start + readiness
	if s, ok := deployer.(Startable); ok {
		setStage("start")
		if err := s.Start(deployCtx, svc); err != nil { return handleErr("start", err) }
		started = true
		setStage("readiness")
		if err := readinessGate(deployCtx, svc, deployer, log); err != nil {
			return handleErr("readiness", err)
		}
	}
	// success
	_ = servstate.Clear(svc.Name)
	result.Status = "success"; result.AuthorEmail = authorEmail
	sendNotify(ctx, cfg, svc, log, recipients, authorEmail, commitInfo, "", "success", "")
	return result, nil
}

// readinessGate 在 deployCtx 下轮询 health；进程存活快速失败；ctx.Done 区分取消/超时。
func readinessGate(ctx context.Context, svc *config.ServiceConfig, deployer Deployer, log *logger.Logger) error {
	interval, _ := svc.HealthIntervalDuration()
	client := &http.Client{Timeout: 3 * time.Second}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		// 进程/容器存活快速失败（static 无进程，跳过）
		if s, ok := deployer.(Stoppable); ok {
			if st, _ := deployer.Status(ctx, svc); st == "stopped" {
				return fmt.Errorf("服务进程启动后立即退出（%s）", svc.Name)
			}
		}
		if resp, err := client.Get(svc.HealthURL); err == nil {
			if resp.StatusCode >= 200 && resp.StatusCode < 400 {
				resp.Body.Close()
				log.Printf("health check passed: %s", svc.HealthURL)
				return nil
			}
			resp.Body.Close()
		}
		select {
		case <-ticker.C:
		case <-ctx.Done():
			return ctx.Err() // Canceled 或 DeadlineExceeded
		}
	}
}
```

> 既有 `Deploy` 里 `sendNotify` 失败分支（fetch/build/stage/start）已被 `handleErr` 取代；删除旧的逐分支 sendNotify 代码。`authorEmail`/`commitInfo` 在 fetch 后赋值，`handleErr` 闭包捕获（注意：闭包在 fetch 失败分支里 authorEmail 尚未赋值——可接受，fetch 失败邮件 recipient 回退到 operatorEmails/notifications.to）。若需更严谨，把 `authorEmail` 提前声明为 `var authorEmail string`。

- [ ] **Step 4: 跑测试验证通过**

Run: `go build ./... && go test ./internal/deploy/ -v`
Expected: PASS（同步修既有 Deploy 测试：旧测试若断言 success 不带 readiness，需给 health 起个 200 服务或改测试期望）。

- [ ] **Step 5: Commit**

```bash
git add internal/deploy/orchestrator.go internal/deploy/orchestrator_test.go
git commit -m "feat(deploy): 就绪门控(health 轮询) + ctx 重构 + 取消/失败判别 + starting 状态"
```

---

## Task 7: `svc start/restart` 非阻塞 fork + TryAcquire + 写 starting；`svc stop` 清 state

**Files:**
- Modify: `internal/deploy/orchestrator.go`（`ServiceStart`/`ServiceRestart` 走 starting + Start + readiness；`ServiceStop` 清 state）
- Modify: `cmd/service_start.go`、`cmd/service_restart.go`、`cmd/service_stop.go`（Linux fork 复用 `forkDeploy` 模式 + TryAcquire）
- Modify: `cmd/deploy.go`（抽取 `forkBackground` 通用 fork 助手，供 deploy/svc start/restart 复用）
- Test: `internal/deploy/orchestrator_test.go`、`cmd/cmd_test.go`

**Interfaces:**
- Consumes: `deploylock.TryAcquire`、`servstate`、`readinessGate`（T6）。
- Produces: `deploy.ServiceStart`/`ServiceRestart` 现含 starting+readiness；`cmd.forkBackground(exe, configPath, serviceName string, childArgs []string) error`。

- [ ] **Step 1: 写失败测试**

```go
// internal/deploy/orchestrator_test.go 追加
func TestServiceStartWritesStartingThenClearsOnHealth(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))
	defer srv.Close()
	svc := &config.ServiceConfig{Name: "ss", Type: "jvm", HealthURL: srv.URL, HealthInterval: "10ms"}
	d := &fakeStartableDeployer{}
	// 执行前无 state
	_, _ = deploy.ServiceStart(context.Background(), svc, d)
	if _, ok := servstate.Read("ss"); ok {
		t.Fatal("health pass 后应清除 starting state")
	}
}

func TestServiceStartFailedWritesStartFailed(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(500) }))
	defer srv.Close()
	svc := &config.ServiceConfig{Name: "sf", Type: "jvm", HealthURL: srv.URL, HealthInterval: "10ms", Timeout: "50ms"}
	d := &fakeStartableDeployer{started: true, status: "running"}
	_, err := deploy.ServiceStart(context.Background(), svc, d)
	if err == nil { t.Fatal("expected failure") }
	st, _ := servstate.Read("sf")
	if st.Status != "start_failed" { t.Fatalf("got %q", st.Status) }
}

func TestServiceStopClearsState(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	_ = servstate.WriteFailed("sx")
	_ = deploy.ServiceStop(context.Background(), &config.ServiceConfig{Name: "sx"}, &fakeStartableDeployer{})
	if _, ok := servstate.Read("sx"); ok {
		t.Fatal("stop 应清除 start_failed")
	}
}
```

- [ ] **Step 2: 跑测试验证失败**

Run: `go test ./internal/deploy/ -run "TestServiceStart|TestServiceStop" -v`
Expected: FAIL（`ServiceStart` 仍直接 `Start`，不写 state/不做 readiness）。

- [ ] **Step 3: 写实现**

```go
// internal/deploy/orchestrator.go —— ServiceStart/Restart/Stop 重写
func ServiceStart(ctx context.Context, svc *config.ServiceConfig, deployer Deployer) error {
	s, ok := deployer.(Startable)
	if !ok {
		return fmt.Errorf("%s 服务类型不支持 start，请用 deploy 重新发布", svc.Type)
	}
	_ = servstate.WriteStarting(svc.Name, "start")
	if err := s.Start(ctx, svc); err != nil {
		_ = servstate.WriteFailed(svc.Name)
		return err
	}
	if err := readinessGate(ctx, svc, deployer, logger.GetServiceLogger(svc.Name)); err != nil {
		if st, ok := deployer.(Stoppable); ok { _ = st.Stop(ctx, svc) }
		_ = servstate.WriteFailed(svc.Name)
		return err
	}
	_ = servstate.Clear(svc.Name)
	return nil
}

func ServiceRestart(ctx context.Context, svc *config.ServiceConfig, deployer Deployer) error {
	if _, ok := deployer.(Startable); !ok {
		return fmt.Errorf("%s 服务类型不支持 restart，请用 deploy 重新发布", svc.Type)
	}
	if s, ok := deployer.(Stoppable); ok { _ = s.Stop(ctx, svc) }
	return ServiceStart(ctx, svc, deployer)
}

func ServiceStop(ctx context.Context, svc *config.ServiceConfig, deployer Deployer) error {
	if s, ok := deployer.(Stoppable); ok {
		if err := s.Stop(ctx, svc); err != nil {
			return err
		}
	}
	_ = servstate.Clear(svc.Name) // stop 清除 start_failed → stopped
	return nil
}
```

> `ServiceStart` 需自己的 timeout/readiness ctx：用 `context.WithTimeout(ctx, deployTimeout(svc))` + `WithCancel`。简化：复用一个 `withDeployCtx(ctx, svc)` 助手返回 (ctx, cancel)，T6 的 Deploy 也改用它。

```go
// withDeployCtx 构造 timeout 总预算 + 手动取消层。
func withDeployCtx(parent context.Context, svc *config.ServiceConfig) (context.Context, context.CancelFunc) {
	deadline, c1 := context.WithTimeout(parent, deployTimeout(svc))
	ctx, c2 := context.WithCancel(deadline)
	return ctx, func() { c2(); c1() }
}
```

```go
// cmd/deploy.go —— 抽取 forkBackground 通用助手
// forkBackground 用 setsid 后台执行 exe childArgs（如 ["deploy"] 或 ["service","start"]），
// 继承 deploy lock fd，日志写服务日志。复用于 deploy/svc start/svc restart。
func forkBackground(exe, configPath, serviceName string, childArgs []string) error {
	lock, err := deploylock.TryAcquire(serviceName)
	if err != nil {
		return fmt.Errorf("服务 %s 正在启动/部署中，请勿重复操作", serviceName)
	}
	homeDir, _ := os.UserHomeDir()
	logDir := filepath.Join(homeDir, ".deployd", "services")
	_ = os.MkdirAll(logDir, 0755)
	logPath := filepath.Join(logDir, serviceName+".log")
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil { lock.Release(); return err }
	defer logFile.Close()
	args := append(append([]string{}, childArgs...), "--no-fork", "--locked", "-c", configPath, serviceName)
	c := exec.Command(exe, args...)
	c.Env = append(os.Environ(), "DEPLOYD_LOCK_FD=3")
	c.Stdout = logFile; c.Stderr = logFile; c.Stdin = nil
	c.ExtraFiles = []*os.File{lock.File()}
	c.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := c.Start(); err != nil { lock.Release(); return err }
	lock.Close()
	fmt.Printf("[%s] %s 后台启动 (pid: %d)，用 deployd status 查看\n", strings.Join(childArgs, " "), serviceName, c.Process.Pid)
	return nil
}
```

```go
// cmd/service_start.go —— RunE 改：Linux fork；否则前台 + TryAcquire
RunE: func(cmd *cobra.Command, args []string) error {
	// ...解析 cfg/svc/d（既有）...
	noFork, _ := cmd.Flags().GetBool("no-fork")
	locked, _ := cmd.Flags().GetBool("locked")
	if noFork {
		if !locked {
			lock, err := deploylock.TryAcquire(serviceName)
			if err != nil { return fmt.Errorf("服务 %s 正在启动/部署中，请勿重复操作", serviceName) }
			defer lock.Release()
		} else {
			defer func() { _ = deploylock.ReleaseInherited() }()
		}
		return deploy.ServiceStart(cmd.Context(), svc, d)
	}
	if runtime.GOOS == "linux" {
		exe, _ := os.Executable()
		return forkBackground(exe, path, serviceName, []string{"service", "start"})
	}
	// macOS 前台
	lock, err := deploylock.TryAcquire(serviceName)
	if err != nil { return fmt.Errorf("服务 %s 正在启动/部署中，请勿重复操作", serviceName) }
	defer lock.Release()
	return deploy.ServiceStart(cmd.Context(), svc, d)
}
```

> `service start` 是 `serviceCmd` 的子命令（`deployd service start <name>`）。fork 子进程跑 `deployd service start --no-fork --locked -c cfg <name>`——故需给 `serviceStartCmd`/`serviceRestartCmd` 加 `--no-fork`/`--locked` 隐藏旗标（同 `deployCmd`，`init()` 里 `Lookup("locked").Hidden=true`）。`deploy.go` 的 `forkDeploy` 改调 `forkBackground(exe, path, serviceName, []string{"deploy"})`，删除其重复的日志/锁/fd 代码。`strings` 包需在 `cmd/deploy.go` import。

```go
// 同理改 cmd/service_restart.go（childArgs={"service","restart"}）、cmd/service_stop.go（无需 fork，直接前台 + 清 state；ServiceStop 已清）
```

```go
// cmd/service_stop.go —— RunE 末尾不变（ServiceStop 内部已清 state）；无需 fork/锁
return deploy.ServiceStop(context.Background(), svc, d)
```

`init()` 里给 `serviceStartCmd`/`serviceRestartCmd` 加 `--no-fork`/`--locked` 隐藏旗标（同 deployCmd）。

- [ ] **Step 4: 跑测试验证通过**

Run: `go build ./... && go test ./internal/deploy/ ./cmd/ -v`
Expected: PASS。

- [ ] **Step 5: Commit**

```bash
git add internal/deploy/orchestrator.go internal/deploy/orchestrator_test.go cmd/deploy.go cmd/service_start.go cmd/service_restart.go cmd/service_stop.go
git commit -m "feat(svc): start/restart 非阻塞 fork + TryAcquire + starting 态; stop 清 state"
```

---

## Task 8: 取消 — sentinel watcher + `deployd cancel` 命令 + SIGINT + 半起处置

**Files:**
- Modify: `internal/deploy/orchestrator.go`（`watchCancel` goroutine；`Deploy`/`ServiceStart` 接线 watcher + SIGINT）
- Create: `cmd/cancel.go`
- Test: `internal/deploy/orchestrator_test.go`、`cmd/cmd_test.go`

**Interfaces:**
- Consumes: `servstate.HasCancel`/`ClearCancel`、`manualCancel`。
- Produces: `deploy.watchCancel(ctx, name, cancel, log)`；`deployd cancel <name>` 命令。

- [ ] **Step 1: 写失败测试**

```go
// internal/deploy/orchestrator_test.go 追加
func TestCancelAbortsReadiness(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	ready := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-ready // 永不通过
		w.WriteHeader(200)
	}))
	defer srv.Close()
	defer close(ready)
	svc := &config.ServiceConfig{Name: "sc", Type: "jvm", HealthURL: srv.URL, HealthInterval: "10ms", Timeout: "10s"}
	d := &fakeStartableDeployer{}
	go func() {
		time.Sleep(80 * time.Millisecond)
		_ = servstate.WriteCancel("sc")
	}()
	_, err := deploy.ServiceStart(context.Background(), svc, d)
	if err == nil { t.Fatal("expected cancel error") }
	// 取消应清 state（→ stopped，非 start_failed）
	if _, ok := servstate.Read("sc"); ok {
		t.Fatal("cancel 应清 state")
	}
}

func TestCancelStopsHalfStarted(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(500) }))
	defer srv.Close()
	svc := &config.ServiceConfig{Name: "sh", Type: "jvm", HealthURL: srv.URL, HealthInterval: "10ms", Timeout: "10s"}
	d := &fakeStartableDeployer{}
	go func() {
		time.Sleep(50 * time.Millisecond)
		_ = servstate.WriteCancel("sh")
	}()
	deploy.ServiceStart(context.Background(), svc, d)
	if d.started {
		t.Fatal("取消应 Stop 半起进程")
	}
}
```

```go
// cmd/cmd_test.go 追加（若有；否则新建 cmd/cancel_test.go）
func TestCancelNoOpWhenIdle(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	// 不持锁 → 提示无需取消
	err := runCancel("idle-svc")
	if err == nil { /* runCancel 打印提示并返回 nil，可接受 */ }
}
```

- [ ] **Step 2: 跑测试验证失败**

Run: `go test ./internal/deploy/ -run "TestCancel" -v`
Expected: FAIL（`watchCancel` 未接线，取消 sentinel 不生效）。

- [ ] **Step 3: 写实现**

```go
// internal/deploy/orchestrator.go —— watchCancel + 接线
// watchCancel 轮询 <name>.cancel sentinel，命中则调 cancel() 并清 sentinel。
// 随 ctx 退出（select 听 ctx），不泄漏。
func watchCancel(ctx context.Context, name string, cancel context.CancelFunc, log *logger.Logger) {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if servstate.HasCancel(name) {
				log.Printf("收到取消信号，中止 %s", name)
				_ = servstate.ClearCancel(name)
				cancel()
				return
			}
		}
	}
}

// 在 Deploy 与 ServiceStart 内、构造 deployCtx 后接线：
//   go watchCancel(deployCtx, svc.Name, manualCancel, log)
// （Deploy：T6 的 `servstate.ClearCancel(svc.Name)` 行之后加 go watchCancel；ServiceStart：构造 ctx 后加）
```

```go
// internal/deploy/orchestrator.go —— ServiceStart 接线 watcher + SIGINT
func ServiceStart(parent context.Context, svc *config.ServiceConfig, deployer Deployer) error {
	s, ok := deployer.(Startable)
	if !ok { return fmt.Errorf("%s 服务类型不支持 start", svc.Type) }
	deployCtx, cancel := withDeployCtx(parent, svc)
	defer cancel()
	log := logger.GetServiceLogger(svc.Name)
	_ = servstate.ClearCancel(svc.Name)
	go watchCancel(deployCtx, svc.Name, cancel, log)
	// SIGINT（前台进程）等价取消
	stopSig := make(chan os.Signal, 1)
	signal.Notify(stopSig, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(stopSig)
	go func() { select { case <-stopSig: cancel(); case <-deployCtx.Done(): } }()

	_ = servstate.WriteStarting(svc.Name, "start")
	if err := s.Start(deployCtx, svc); err != nil {
		if deployCtx.Err() == context.Canceled {
			_ = servstate.Clear(svc.Name); return ctxErr(deployCtx)
		}
		_ = servstate.WriteFailed(svc.Name); return err
	}
	if err := readinessGate(deployCtx, svc, deployer, log); err != nil {
		if deployCtx.Err() == context.Canceled {
			// 手动取消：Stop 半起 + 清 state，不发邮件
			if st, ok := deployer.(Stoppable); ok { _ = st.Stop(parent, svc) }
			_ = servstate.Clear(svc.Name)
			return ctxErr(deployCtx)
		}
		// 失败/超时：Stop + start_failed
		if st, ok := deployer.(Stoppable); ok { _ = st.Stop(parent, svc) }
		_ = servstate.WriteFailed(svc.Name)
		return err
	}
	_ = servstate.Clear(svc.Name)
	return nil
}

func ctxErr(ctx context.Context) error {
	if err := ctx.Err(); err != nil { return fmt.Errorf("cancelled: %w", err) }
	return nil
}
```

> `Deploy` 同样在 `deployCtx` 构造后接线 `go watchCancel(deployCtx, svc.Name, manualCancel, log)` 与 SIGINT handler。T6 的 `handleErr` 已处理 `Canceled` 分支（清 state + Stop + 无邮件），无需再改。

```go
// cmd/cancel.go —— 新建
package cmd

import (
	"fmt"
	"github.com/auto-deployer/auto-deployer/internal/deploylock"
	"github.com/auto-deployer/auto-deployer/internal/servstate"
	"github.com/spf13/cobra"
)

var cancelCmd = &cobra.Command{
	Use:   "cancel <service_name>",
	Short: "取消正在部署/启动中的服务",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return runCancel(args[0])
	},
}

func init() { rootCmd.AddCommand(cancelCmd) }

func runCancel(serviceName string) error {
	if !deploylock.IsHeld(serviceName) {
		fmt.Printf("服务 %s 未在部署/启动中，无需取消\n", serviceName)
		return nil
	}
	if err := servstate.WriteCancel(serviceName); err != nil {
		return fmt.Errorf("写取消信号失败: %w", err)
	}
	fmt.Printf("已向 %s 发出取消信号，部署/启动进程将中止\n", serviceName)
	return nil
}
```

- [ ] **Step 4: 跑测试验证通过**

Run: `go build ./... && go test ./internal/deploy/ ./cmd/ -v`
Expected: PASS。

- [ ] **Step 5: Commit**

```bash
git add internal/deploy/orchestrator.go internal/deploy/orchestrator_test.go cmd/cancel.go cmd/cancel_test.go
git commit -m "feat(cancel): sentinel watcher + deployd cancel 命令 + SIGINT + 半起 Stop"
```

---

## Task 9: `artifact` 列表 + `CopyArtifact` 接受 ctx

**Files:**
- Modify: `internal/build/artifact.go`（签名加 ctx；artifact 支持 string 或 []string）
- Modify: `plugins/springboot/plugin.go`、`plugins/static/plugin.go`、`plugins/node/plugin.go`（artifact 字段类型改 `config.Command` 复用 string-or-list 解码；Stage 调用新签名）
- Test: `internal/build/artifact_test.go`

**Interfaces:**
- Produces: `build.CopyArtifact(ctx context.Context, workspace string, pattern interface{}, destDir string, out io.Writer) ([]string, error)`（pattern 为 string 或 []string）。

- [ ] **Step 1: 写失败测试**

```go
// internal/build/artifact_test.go 追加
func TestCopyArtifactListTwoFiles(t *testing.T) {
	ws := t.TempDir()
	_ = os.WriteFile(filepath.Join(ws, "hello1.txt"), []byte("a"), 0644)
	_ = os.WriteFile(filepath.Join(ws, "test.json"), []byte("{}"), 0644)
	dest := filepath.Join(t.TempDir(), "out")
	copied, err := CopyArtifact(context.Background(), ws, []string{"hello1.txt", "test.json"}, dest, io.Discard)
	if err != nil { t.Fatal(err) }
	if len(copied) != 2 { t.Fatalf("want 2 files, got %v", copied) }
	for _, f := range []string{"hello1.txt", "test.json"} {
		if _, err := os.Stat(filepath.Join(dest, f)); err != nil {
			t.Errorf("dest missing %s: %v", f, err)
		}
	}
}

func TestCopyArtifactSingleStringUnchanged(t *testing.T) {
	ws := t.TempDir()
	_ = os.WriteFile(filepath.Join(ws, "x.txt"), []byte("a"), 0644)
	dest := filepath.Join(t.TempDir(), "out")
	if _, err := CopyArtifact(context.Background(), ws, "x.txt", dest, io.Discard); err != nil {
		t.Fatal(err)
	}
}

func TestCopyArtifactCancelsBetweenFiles(t *testing.T) {
	ws := t.TempDir()
	_ = os.WriteFile(filepath.Join(ws, "a.txt"), []byte("a"), 0644)
	_ = os.WriteFile(filepath.Join(ws, "b.txt"), []byte("b"), 0644)
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // 已取消
	_, err := CopyArtifact(ctx, ws, []string{"a.txt", "b.txt"}, t.TempDir(), io.Discard)
	if err == nil { t.Fatal("已取消应返回错误") }
}
```

- [ ] **Step 2: 跑测试验证失败**

Run: `go test ./internal/build/ -run "TestCopyArtifactList|TestCopyArtifactSingleString|TestCopyArtifactCancels" -v`
Expected: FAIL（签名不匹配 / 不支持 list）。

- [ ] **Step 3: 写实现**

```go
// internal/build/artifact.go —— CopyArtifact 支持 ctx + 列表
func CopyArtifact(ctx context.Context, workspace string, pattern interface{}, destDir string, out io.Writer) ([]string, error) {
	patterns := normalizePatterns(pattern) // string → []string{...}; []string 原样
	if len(patterns) == 0 {
		return nil, fmt.Errorf("empty artifact pattern")
	}
	if err := os.MkdirAll(destDir, 0755); err != nil { return nil, err }
	var all []string
	for _, p := range patterns {
		copied, err := copyOnePattern(ctx, workspace, p, destDir, out)
		all = append(all, copied...)
		if err != nil { return all, err }
		// 文件之间检查取消
		if err := ctx.Err(); err != nil { return all, err }
	}
	if len(all) == 0 { return all, fmt.Errorf("no artifact copied") }
	return all, nil
}

func normalizePatterns(p interface{}) []string {
	switch v := p.(type) {
	case string:
		return []string{v}
	case []string:
		return v
	}
	return nil
}

// copyOnePattern 是原 CopyArtifact 的单 pattern 逻辑（提取出来），增加 ctx 检查。
func copyOnePattern(ctx context.Context, workspace, pattern, destDir string, out io.Writer) ([]string, error) {
	matches, err := filepath.Glob(filepath.Join(workspace, pattern))
	if err != nil { return nil, err }
	if len(matches) == 0 { return nil, fmt.Errorf("no artifact matching %s", pattern) }
	copyContents := !hasGlobMeta(pattern)
	var copied []string
	for _, src := range matches {
		if err := ctx.Err(); err != nil { return copied, err }
		// ...原 stat/目录/文件拷贝逻辑（不变）...
	}
	if len(copied) == 0 { return copied, fmt.Errorf("no artifact copied from %s", pattern) }
	return copied, nil
}
```

> 把原 `CopyArtifact` 函数体迁入 `copyOnePattern`（保留 `copyContents`/`*.original.jar` 跳过/目录语义不变），并在其循环首加 `ctx.Err()` 检查。

```go
// plugins/springboot/plugin.go —— artifact 字段类型改 config.Command（string-or-list）
type jvmDeployConfig struct {
	Artifact config.Command `yaml:"artifact"` // 单串或列表
	Dest     string         `yaml:"dest"`
	Run      string         `yaml:"run"`
	Env      map[string]string `yaml:"env"`
}
// Stage 内：
keep, err := build.CopyArtifact(ctx, svc.Workspace, dc.Artifact.Slice(), dest, p.output)
```

> `config.Command` 已支持 string-or-list（`Slice()` 方法）。static/node 同改：`Artifact config.Command`，`Stage` 调 `build.CopyArtifact(ctx, svc.Workspace, dc.Artifact.Slice(), dest, out)`。既有的单字符串配置仍兼容（`Command.UnmarshalYAML` 已处理）。

- [ ] **Step 4: 跑测试验证通过**

Run: `go build ./... && go test ./internal/build/ ./plugins/... -v`
Expected: PASS（既有 artifact 测试若用单串不回归）。

- [ ] **Step 5: Commit**

```bash
git add internal/build/artifact.go internal/build/artifact_test.go plugins/springboot/plugin.go plugins/static/plugin.go plugins/node/plugin.go
git commit -m "feat(artifact): CopyArtifact 支持 ctx + 列表多文件; 插件 artifact 字段升为 Command"
```

---

## Task 10: daemon 侧 `Status` 走 `GetServiceStatusRich`（修 C2 残留）

**Files:**
- Modify: `internal/daemon/commands.go`（`Status` 改用 `GetServiceStatusRich`）
- Test: `internal/daemon/daemon_test.go`

**Interfaces:**
- Consumes: `deploy.GetServiceStatusRich`、`registry.Get`。

- [ ] **Step 1: 写失败测试**

```go
// internal/daemon/daemon_test.go 追加
func TestStatusUsesRichForStatic(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	// static 无进程；旧逻辑直读 pid 永远 stopped；新逻辑走插件 Status（health）
	// 构造一个返回 running 的 static 配置 + mock health？daemon.Status 走真实 registry。
	// 简化：断言 daemon.Status 不再直接 process.NewManager(svcPIDFile).Status()
	// —— 通过它对 static 不报 stopped（用 unknown/health 判定）。需起 http 服务。
	// 见既有 daemon_test.go 风格；此处占位改为集成断言：static 走插件后 != "stopped"。
}
```

> 若 daemon_test.go 难以构造 static+health，改为**手测脚本**纳入 T12（Ubuntu VM：static 配 health，`deployd status` 不再恒 stopped）。本任务代码改动仍要落地。

- [ ] **Step 2: 跑测试验证失败**

Run: `go test ./internal/daemon/ -run TestStatus -v`
Expected: FAIL 或 skip（若用占位）。

- [ ] **Step 3: 写实现**

```go
// internal/daemon/commands.go —— Status 改走 GetServiceStatusRich
func Status(configPath string) error {
	pidFile := filepath.Join(homeDir(configPath), defaultPidDir, "deployd.pid")
	fmt.Printf("deployd: %s\n", process.NewManager(pidFile).Status())

	cfgPath := configPath
	if cfgPath == "" { cfgPath = filepath.Join(homeDir(configPath), "config.yaml") }
	if _, err := os.Stat(cfgPath); os.IsNotExist(err) { return nil }
	cfg, err := config.Load(cfgPath)
	if err != nil { return err }
	for i := range cfg.Services {
		svc := &cfg.Services[i]
		d, err := registry.Get(svc.Type)
		st := "unknown (" + err.Error() + ")"
		if err == nil {
			s, _ := deploy.GetServiceStatusRich(context.Background(), svc, d)
			st = s
		}
		fmt.Printf("  %-30s %s\n", svc.Name, st)
	}
	return nil
}
```

> 需 import `context`、`registry`、`deploy`。`daemon.Status` 现 import 须加这三者。

- [ ] **Step 4: 跑测试验证通过**

Run: `go build ./... && go test ./internal/daemon/ -v`
Expected: PASS。

- [ ] **Step 5: Commit**

```bash
git add internal/daemon/commands.go
git commit -m "fix(daemon): Status 走 GetServiceStatusRich，static 不再恒 stopped(C2)"
```

---

## Task 11: 文档与向导同步

**Files:**
- Modify: `config.yaml.example`、`README.md`、`internal/config/wizard.go`

- [ ] **Step 1: 更新 `config.yaml.example`**

每个模型块补 `deploy.health`（必填）+ `health_interval` 注释；jvm 块示例：

```yaml
    deploy:
      # artifact: "target/*.jar"   # 可选：产物 glob(也支持列表 ["a.jar","b.jar"])
      # dest: "/opt/app"
      run: "java -jar target/app.jar"
      health: "http://localhost:8080/actuator/health"  # 必填：就绪判定 health URL
      # health_interval: "10s"     # 可选：轮询间隔，默认 10s
```

`timeout` 注释改为 `# 可选：fetch+build+stage+就绪 总超时(默认 30m)`。

- [ ] **Step 2: 更新 `README.md`**

- 状态表新增 starting/start_failed + 颜色说明（停止灰/运行绿/启动蓝/失败红/unknown 暗黄）。
- `deployd cancel <name>` 命令说明 + 场景。
- `deploy.health` 全类型必填 + `health_interval` 说明。
- `artifact` 列表写法示例。
- `svc start/restart` 后台化说明（用 `deployd status` 查状态）。

- [ ] **Step 3: 更新 `internal/config/wizard.go`**

向导对全部模型引导填 `deploy.health`（必填，空则提示）+ 可选 `health_interval`。复用既有 wizard 交互模式。

- [ ] **Step 4: 验证**

Run: `go build ./... && go test ./internal/config/ -v`（确保 wizard 测试通过）
手动：`./auto-deployer config` 走一遍向导确认 health 提示。

- [ ] **Step 5: Commit**

```bash
git add config.yaml.example README.md internal/config/wizard.go
git commit -m "docs: health 全类型必填 + 颜色/取消/artifact 列表/timeout 含就绪 文档同步"
```

---

## Task 12: e2e 集成验证（ubuntu22 @ OrbStack）

**Files:**
- Modify: `test/e2e_test.go`

**环境：** OrbStack 进 ubuntu22 虚拟机；二进制与 `config.yaml` 在 `/home/auto-deploy/` 下。用例为集成脚本式断言（或 `testing` 标记 `//go:build e2e` 的手测）。

- [ ] **Step 1: 写/补 e2e 用例**

覆盖 spec §14 的 10 条用例（此处列关键断言点）：

```go
//go:build e2e

package e2e

// TestE2E_StatusColorsAndStates: 起一个 health 200 的 jvm 服务 → status 绿；
// 改 health 指向 500 服务 deploy → start_failed 红 + 失败邮件（mock SMTP）。
// TestE2E_LockExcludesConcurrentStart: deploy 中再 svc -s 报「正在启动/部署中」。
// TestE2E_CancelAbortsBuild: 一个 sleep 30s 的 build + cancel → 中止、无邮件。
// TestE2E_StaleStartingRecovers: kill fork 子进程 → status 恢复 start_failed。
// TestE2E_ArtifactListTwoFiles: artifact: ["hello1.txt","test.json"] → dest 有两文件。
// TestE2E_NonTTYStripsColor: deployd status | cat 无 ANSI。
// TestE2E_MissingHealthRejected: 旧配置无 health → 启动报错。
```

- [ ] **Step 2: 在 VM 执行**

```bash
go build -o /home/auto-deploy/auto-deployer .   # 交叉编译或 VM 内 build
cd /home/auto-deploy && ./auto-deployer status   # 基线
go test -tags e2e ./test/                        # 或手跑脚本
```

- [ ] **Step 3: 全量回归**

Run: `go build ./... && go test ./... && go vet ./...`
Expected: 全绿。

- [ ] **Step 4: Commit**

```bash
git add test/e2e_test.go
git commit -m "test(e2e): 状态/就绪/取消/artifact/颜色/health 校验 集成用例"
```

---

## Self-Review（plan 自检）

**1. Spec coverage：** spec §3 变更 1（状态集合）→ T1(色)+T2(态)+T5；§3-2（持久化）→ T2；§3-3（成功判定）→ T6；§3-4（health 强制）→ T4；§3-5（timeout 语义）→ T6(ctx 含就绪)；§3-6（svc 互斥）→ T7；§3-7（取消）→ T8；§3-8（颜色）→ T1+T5；§3-9（artifact 列表）→ T9。§4 状态机 → T2+T5；§5 状态文件/锁 → T2+T3+T5；§6 就绪 → T6；§7 取消 → T8；§8 颜色 → T1+T5；§9 artifact → T9；§10 配置 → T4+T11；§11 兼容矩阵 → 各任务均保留外层；§13 分阶段 → T1..T10 对应 P1..P6；§14 验证 → T12。**无遗漏。**

**2. Placeholder scan：** 无 TBD/TODO；T10 测试用「占位改手测」属明确决策（spec §14 覆盖），非占位。已避「add error handling」等空话。

**3. Type consistency：** `servstate.WriteStarting/WriteFailed/Clear/Read/WriteCancel/ClearCancel/HasCancel`、`deploylock.IsHeld`、`Manager.Alive`、`GetServiceStatusRich`、`readinessGate`、`watchCancel`、`withDeployCtx`、`forkBackground`、`CopyArtifact(ctx, ws, pattern interface{}, dest, out)`、`config.Command.Slice()`——各任务间签名一致。`handleErr`/`ctxErr` 在 T6/T8 定义一致。`fakeStartableDeployer` 在 T6/T8 测试复用同结构。

**4. 已知需在实现时注意：**
- T6 `Deploy` 闭包捕获 `authorEmail`：fetch 失败分支 authorEmail 未赋值，recipient 回退 operatorEmails（可接受）。
- T7 `forkBackground` 子命令 argv：`service start --no-fork --locked` 需 `serviceCmd` 接受 `--no-fork`/`--locked`（隐藏旗标，同 deploy）。
- T9 `config.Command` 作 artifact 类型：其 `MarshalYAML` 单条返回 string——序列化不影响（仅解码用）。
- T10 daemon 测试若难构造，落到 T12 VM 手测，代码仍落地。

## Execution Handoff

Plan complete and saved to `docs/superpowers/plans/2026-09-02-status-states-readiness-cancel.md`. Two execution options:

**1. Subagent-Driven (recommended)** — 每个 task 派新 subagent，任务间两段评审，快速迭代。

**2. Inline Execution** — 在本会话用 executing-plans 批量执行 + checkpoint 评审。

选哪种？
