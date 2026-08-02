# Service Management & Config Loading Design

## Overview

修复 `deployd status` 服务状态显示问题，实现服务管理命令，完善 `deployd deploy` 完整部署流程，并优化配置文件加载逻辑。

## 设计目标

1. **status 正确显示服务状态** — 修复默认配置路径查找逻辑
2. **实现服务生命周期管理** — start/stop/restart 服务命令
3. **实现完整部署流程** — deploy 命令：git fetch → get author email → build → stop → start → notify
4. **通知邮件正确传递 authorEmail** — CLI 部署通过 git log 获取最近提交者邮箱
5. **配置加载优先级优化** — 当前目录 > ~/.deployd/，daemon 只加载一次
6. **可扩展** — 新增服务类型只需实现 Deployer 接口

---

## 一、配置文件加载

### 优先级

```
1. -c 命令行参数指定的路径（绝对或相对）
2. 当前工作目录的 config.yaml
3. ~/.deployd/config.yaml
```

### 实现

新增 `config.DefaultConfig() string` 函数，按优先级查找。

### 守护进程配置缓存

`deployd start` 加载配置后通过 `daemon.SetConfigPath(path)` 缓存，
后续所有命令通过 `configPath` 参数或缓存值查找配置。

### 重启守护进程

`deployd restart [-c config.yaml]`：
- 找到并停止当前 daemon 进程（读取 PID 文件发送 SIGTERM）
- 用指定配置（或默认配置）重新启动 daemon
- 返回子进程 PID

---

## 二、命令设计

为避免 daemon 管理和服务管理命令冲突，服务管理命令使用 `service` 子命令命名空间：

| 命令 | 用途 |
|------|------|
| `deployd start` | 启动 daemon |
| `deployd stop` | 停止 daemon |
| `deployd restart` | 重启 daemon |
| `deployd status` | 显示 daemon + 所有服务状态 |
| `deployd deploy <name>` | 完整部署流程 |
| `deployd service start <name>` | 启动服务（不重新构建） |
| `deployd service stop <name>` | 停止服务 |
| `deployd service restart <name>` | 重启服务（不重新构建） |
| `deployd logs [name]` | 查看日志 |
| `deployd config` | 交互式配置向导 |

---

## 三、orchestrator 层设计

### 文件位置

```
internal/deploy/
  orchestrator.go   # 编排部署/启停流程
```

### Deployer 接口（已有，保持兼容）

```go
// internal/webhook/server.go 中已定义
type Deployer interface {
    Build(ctx context.Context, svc *config.ServiceConfig) error
    Start(ctx context.Context, svc *config.ServiceConfig) error
    Stop(ctx context.Context, svc *config.ServiceConfig) error
    Status(ctx context.Context, svc *config.ServiceConfig) (string, error)
}
```

### Orchestrator 职责

orchestrator 负责编排跨 plugin 的通用流程（git fetch、author email 获取、通知），
plugin 负责各自类型特有的 Build/Start/Stop 实现。

```go
package deploy

// Deploy 执行完整部署流程
// fetch → getAuthorEmail → plugin.Build → plugin.Stop → plugin.Start → notify
func Deploy(ctx context.Context, svc *config.ServiceConfig, cfg *config.AppConfig) (*DeployResult, error)

// ServiceStart 启动服务（不重新 fetch/build）
func ServiceStart(ctx context.Context, svc *config.ServiceConfig) error

// ServiceStop 停止服务
func ServiceStop(ctx context.Context, svc *config.ServiceConfig) error

// ServiceRestart 重启服务（不重新 fetch/build）
func ServiceRestart(ctx context.Context, svc *config.ServiceConfig) error
```

### DeployResult

```go
type DeployResult struct {
    ServiceName string
    Status      string // "success" | "failed"
    AuthorEmail string
    Error       string
}
```

### 流程细节

```
Deploy():
  1. build.Fetch(svc.Repo.URL, keyFile, svc.Repo.Branch, svc.Workspace)
  2. authorEmail := build.GetLatestAuthorEmail(svc.Workspace, svc.Repo.Branch)
  3. plugin.Build(ctx, svc)        // 构建（不含 fetch）
  4. plugin.Stop(ctx, svc)         // 停止旧进程
  5. plugin.Start(ctx, svc)        // 启动新进程
  6. notify.DeployResult(...)      // 发送通知

ServiceStart():
  1. plugin.Start(ctx, svc)

ServiceStop():
  1. plugin.Stop(ctx, svc)

ServiceRestart():
  1. plugin.Stop(ctx, svc)
  2. plugin.Start(ctx, svc)
  3. notify.DeployResult(...)
```

---

## 四、Spring Boot Plugin 调整

### 变化

`springboot.Build()` 当前包含了 git fetch 逻辑。重构后：
- `Build()` 只负责：mvn build + move jar + clean workspace
- git fetch 由 orchestrator 在调用 Build() 之前完成
- `Start()`/`Stop()`/`Status()` 不变

### PID 文件路径统一

plugin 和 daemon commands 使用相同的路径：
```
~/.deployd/run/<serviceName>.pid
```
通过 `daemon.ServicePIDDir()` 公共函数访问，避免路径不一致。

---

## 五、Git 工具函数

### 新增函数 (`internal/build/git.go`)

```go
// GetLatestAuthorEmail 在 workspace 执行 git log -1 --format=%ae
// 获取失败（目录不存在、非 git 仓库等）返回空字符串
func GetLatestAuthorEmail(workspace, branch string) string
```

---

## 六、通知邮件修复

### 问题

`buildNotifier(cfg, "")` 传入空 authorEmail 时：
- SMTP 路径：`client.Rcpt("")` 会报错
- Resend 路径：空字符串作为收件人可能导致 API 报错

### 修复

在 `sendResend()` 和 `sendSMTP()` 中跳过空字符串收件人：

```go
// sendSMTP
for _, r := range recipients {
    if r == "" {
        continue
    }
    if err := client.Rcpt(r); err != nil { ... }
}

// sendResend
validRecipients := make([]string, 0, len(recipients))
for _, r := range recipients {
    if r != "" {
        validRecipients = append(validRecipients, r)
    }
}
// 使用 validRecipients
```

### authorEmail 获取优先级

1. git fetch 后从 `git log -1 --format=%ae` 获取（CLI 部署）
2. webhook payload 中的 commit author email（webhook 触发）
3. 获取失败返回空字符串，通知仍能发到 `notifications.to` 列表

---

## 七、Status 修复

### 问题根因

`commands.go:Status()` 中 `homeDir(configPath)` 当 `configPath=""` 时：
- `filepath.Join("", defaultPidDir, svc.Name+".pid")` = `.deployd/run/hello-world.pid`
- 这是一个相对路径，从当前工作目录查找，而非 home 目录

### 修复

1. 使用 `config.DefaultConfig()` 找到配置文件
2. `homeDir` 函数改为始终返回 `$HOME`，不依赖 configPath

```go
func homeDir(_ string) string {
    h, _ := os.UserHomeDir()
    return h
}
```

---

## 八、文件变更清单

### 新增

```
internal/deploy/orchestrator.go   # 编排部署/启停流程
cmd/service_start.go              # service start <name>
cmd/service_stop.go               # service stop <name>
cmd/service_restart.go            # service restart <name>
cmd/restart.go                    # restart daemon
```

### 修改

```
internal/config/config.go         # 新增 DefaultConfig()
internal/build/git.go             # 新增 GetLatestAuthorEmail()
internal/daemon/daemon.go         # SetConfigPath 已存在，restart 支持
internal/daemon/commands.go       # 修复 status 路径、实现完整 deploy/stopService
internal/webhook/server.go        # 改为调用 orchestrator.Deploy()
plugins/springboot/plugin.go      # Build() 移除 fetch，只保留构建
internal/notify/email.go          # 跳过空字符串收件人
cmd/root.go                       # 配置路径缓存
cmd/status.go                     # 使用缓存配置
cmd/deploy.go                     # 使用缓存配置
cmd/start.go                      # 保留 daemon start，增加 --config
cmd/stop.go                       # 保留 daemon stop，增加 --config
cmd/config_cmd.go                 # 使用 DefaultConfig()
```

---

## 九、错误处理

| 场景 | 处理方式 |
|------|----------|
| GetLatestAuthorEmail 失败 | 返回空字符串，部署继续，通知中"变更者"为空 |
| 服务停止失败 | 记录警告日志，继续启动新实例 |
| 通知发送失败 | 记录警告日志，不中断部署 |
| 配置加载失败 | 返回明确错误信息，列出查找路径 |
| 服务不存在 | 返回错误 "service X not found in config" |
| 服务类型不支持 | 返回错误 "unknown service type" |

---

## 十、测试计划

1. `config.DefaultConfig` — 优先级测试（当前目录 > ~/.deployd/ > 不存在）
2. `build.GetLatestAuthorEmail` — 单元测试（有 git 仓库/无 git 仓库/空目录）
3. `deploy` 包 — 单元测试（mock Deployer，验证调用顺序）
4. `notify` 包 — 修复后验证空收件人被跳过
5. e2e 测试 — 完整 deploy 流程（mock git/构建）
6. `daemon.Status` — 验证 PID 文件路径正确性
