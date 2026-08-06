# 服务级别日志隔离设计

**创建日期：** 2026-08-06
**状态：** 已完成
**相关提交：** 71f2853 feat: add service-level log isolation with logger package

---

## 问题背景

### 原始问题

在开发过程中发现日志系统存在两个核心问题：

1. **`deployd logs -t` 看不到 `deployd deploy hello-world` 的日志**
   - `deployd deploy` 在 Linux 上 fork 子进程，子进程 stdout 写入 `/home/deployd/.deployd/deploy/hello-world.log`
   - `deployd logs` 读取的是 `~/.deployd/services/hello-world.log`
   - 两个路径不一致，导致看不到日志

2. **Webhook 触发的日志无法按服务隔离**
   - Webhook 处理时直接调用 `deploy.Deploy()`，`fmt.Printf` 输出到 daemon stdout
   - 所有服务的日志混在 `daemon-fork.log` 里，无法区分是哪个服务的部署日志
   - 多服务场景下日志完全混乱

### 根因分析

```
┌─────────────────────────────────────────────────────────────────────────────┐
│  日志写入路径不一致                                                          │
├─────────────────────────────────────────────────────────────────────────────┤
│  deployd deploy hello-world → ~/.deployd/deploy/hello-world.log             │
│  deployd logs hello-world   → ~/.deployd/services/hello-world.log           │
│  Webhook 触发               → daemon-fork.log (daemon stdout)               │
└─────────────────────────────────────────────────────────────────────────────┘
```

---

## 设计方案

### 核心思路

创建一个 `internal/logger` 包，提供按服务名隔离的日志写入能力：

- 每个服务有独立的日志文件：`~/.deployd/services/<service-name>.log`
- 所有 deploy 相关日志（手动触发 + webhook 触发）统一写入服务日志文件
- daemon 自身日志（webhook 原始信息）继续写入 `deployd.log`

### 架构设计

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                           日志架构                                           │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                             │
│  ┌─────────────────┐    ┌─────────────────────────────────────────────┐    │
│  │  deployd deploy  │    │  Webhook Handler                           │    │
│  │  (Linux fork)    │    │  (HTTP server)                             │    │
│  └────────┬────────┘    └───────────────┬─────────────────────────────┘    │
│           │                            │                                   │
│           │ stdout/stderr              │ deploy.Deploy()                   │
│           ▼                            ▼                                   │
│  ┌─────────────────────────────────────────────────────────────────────┐   │
│  │                    logger.GetServiceLogger(svc.Name)                │   │
│  │                     ┌─────────────────────┐                        │   │
│  │                     │  Manager (单例)      │                        │   │
│  │                     │  - 缓存 logger 实例  │                        │   │
│  │                     │  - 按服务名隔离      │                        │   │
│  │                     └──────────┬──────────┘                        │   │
│  └─────────────────────────────────┼───────────────────────────────────┘   │
│                                    │                                       │
│           ┌────────────────────────┼────────────────────────┐             │
│           │                        │                        │             │
│           ▼                        ▼                        ▼             │
│  ┌─────────────────┐  ┌─────────────────┐  ┌─────────────────┐          │
│  │ hello-world.log │  │ my-api.log      │  │ another.log     │          │
│  │ (服务专属日志)    │  │ (服务专属日志)   │  │ (服务专属日志)   │          │
│  └─────────────────┘  └─────────────────┘  └─────────────────┘          │
│                                                                             │
│  +  ~/.deployd/deployd.log (daemon 自身日志)                                │
│                                                                             │
└─────────────────────────────────────────────────────────────────────────────┘
```

### 日志路径规范

| 日志类型 | 路径 | 说明 |
|----------|------|------|
| 服务部署日志 | `~/.deployd/services/<service>.log` | 按服务名隔离，所有 deploy 日志 |
| Daemon 日志 | `~/.deployd/deployd.log` | daemon 自身运行日志（webhook 原始信息） |
| 启动日志 | `~/.deployd/daemon-fork.log` | Linux fork 时的启动日志 |

---

## 实现细节

### 1. Logger 包设计

**文件：** `internal/logger/logger.go`

```go
package logger

import (
    "fmt"
    "io"
    "os"
    "path/filepath"
    "sync"
)

// Logger wraps an io.Writer with service-specific prefix.
type Logger struct {
    writer io.Writer
    prefix string  // 例如: "[hello-world] "
}

// Manager provides service-specific loggers.
type Manager struct {
    mu       sync.Mutex
    loggers  map[string]*Logger
    logDir   string
}

var defaultManager *Manager
var once sync.Once

// GetServiceLogger 获取服务专属 logger
func GetServiceLogger(serviceName string) *Logger {
    return defaultManager.GetServiceLogger(serviceName)
}

// GetServiceLogger 内部实现
func (m *Manager) GetServiceLogger(serviceName string) *Logger {
    m.mu.Lock()
    defer m.mu.Unlock()

    // 缓存已创建的 logger
    if l, ok := m.loggers[serviceName]; ok {
        return l
    }

    // 创建新的 logger，写入服务专属文件
    homeDir, _ := os.UserHomeDir()
    logPath := filepath.Join(homeDir, m.logDir, serviceName+".log")

    f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
    if err != nil {
        // 失败时降级到 stdout
        return &Logger{writer: os.Stdout, prefix: fmt.Sprintf("[%s] ", serviceName)}
    }

    l := &Logger{
        writer: f,
        prefix: fmt.Sprintf("[%s] ", serviceName),
    }
    m.loggers[serviceName] = l
    return l
}
```

### 2. 集成到 Deploy 流程

**文件：** `internal/deploy/orchestrator.go`

```go
func Deploy(ctx context.Context, svc *config.ServiceConfig, cfg *config.AppConfig, deployer Deployer) (*DeployResult, error) {
    result := &DeployResult{ServiceName: svc.Name}
    log := logger.GetServiceLogger(svc.Name)  // 获取服务专属 logger

    log.Printf("fetching %s to %s...", svc.Repo.URL, svc.Workspace)
    // ... 其他日志统一使用 log.Printf
}
```

### 3. 集成到 Webhook 处理

**文件：** `internal/webhook/server.go`

```go
func Handle(w http.ResponseWriter, r *http.Request) {
    // ... webhook 解析和匹配 ...

    log := logger.GetServiceLogger(matched.Name)  // 创建服务专属 logger
    deployResult, err := deploy.Deploy(ctx, matched, cfg, deployer)
    if err != nil {
        log.Printf("deploy failed: %v", err)  // 写入服务日志
    } else {
        log.Printf("%s deployed: %s", matched.Name, deployResult.Status)
    }
}
```

### 4. deployd deploy 命令（Linux fork）

**文件：** `cmd/deploy.go`

```go
func forkDeploy(configPath, serviceName string) error {
    // 使用 home 目录作为日志路径，与 deployd logs 命令保持一致
    homeDir, _ := os.UserHomeDir()
    logDir := filepath.Join(homeDir, ".deployd", "services")
    logPath := filepath.Join(logDir, fmt.Sprintf("%s.log", serviceName))

    logFile, _ := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
    cmd.Stdout = logFile
    cmd.Stderr = logFile
    // ...
}
```

---

## 日志内容示例

### hello-world.log

```
[hello-world] fetching git@gitee.com:zhouqiang-study-java/auto-deployer-test.git to /opt/app...
[hello-world] building hello-world...
[hello-world] copying hello-world-0.0.1.jar to /opt/app
[hello-world] cleaning workspace
[hello-world] stopping hello-world...
[hello-world] starting hello-world...
[hello-world] started with pid 12345
[hello-world] sending notification to: user@example.com
[hello-world] notification sent successfully
[hello-world] hello-world deployed successfully
```

### deployd.log (daemon 日志)

```
[webhook] received gitee push to git@gitee.com:xxx/auto-deployer-test.git/main
[webhook] matched service: hello-world
```

---

## 多服务场景

### 配置示例

```yaml
services:
  - name: "hello-world"
    type: springboot
    repo:
      url: "git@gitee.com:user/repo1.git"
      branch: main
    workspace: "/opt/hello-world"
    build:
      command: "mvn package -Dmaven.test.skip=true"
    run:
      command: "java -jar hello-world-0.0.1.jar"

  - name: "my-api"
    type: springboot
    repo:
      url: "git@gitee.com:user/repo2.git"
      branch: main
    workspace: "/opt/my-api"
    build:
      command: "mvn package -Dmaven.test.skip=true"
    run:
      command: "java -jar my-api-0.0.1.jar"
```

### 日志隔离效果

```
~/.deployd/services/
├── hello-world.log   # hello-world 服务的所有日志
├── my-api.log        # my-api 服务的所有日志
└── another-service.log  # 新增服务自动隔离
```

### 命令使用

```bash
# 查看 hello-world 的最后 20 行日志
deployd logs hello-world -n 20

# 实时跟踪 hello-world 的日志
deployd logs hello-world -t

# 实时跟踪多个服务的日志（需要开多个终端）
deployd logs hello-world -t  # 终端 1
deployd logs my-api -t       # 终端 2
```

---

## 扩展性

### 新增服务类型

无需修改日志逻辑。只要 `ServiceConfig` 有 `Name` 字段，自动获得日志隔离：

```go
// 任何新的 Deployer 实现
type Deployer interface {
    Build(ctx context.Context, svc *config.ServiceConfig) error
    Start(ctx context.Context, svc *config.ServiceConfig) error
    Stop(ctx context.Context, svc *config.ServiceConfig) error
    Status(ctx context.Context, svc *config.ServiceConfig) (string, error)
}
```

### 日志格式扩展

```go
// Logger 支持的方法
log.Printf("format %s %d", "string", 123)  // 格式化输出
log.Fprintln("simple text")                 // 简单文本输出
```

---

## 变更文件清单

| 文件 | 变更内容 |
|------|----------|
| `internal/logger/logger.go` | **新增** - Logger 包实现 |
| `internal/deploy/orchestrator.go` | 修改 - 使用 logger 替换 fmt.Printf |
| `internal/webhook/server.go` | 修改 - 为 webhook 触发添加 logger |
| `cmd/deploy.go` | 修改 - 修复日志路径到 home 目录 |
| `internal/daemon/commands.go` | 无需修改 - 已使用 homeDir |

---

## 测试验证

### 本地验证（macOS）

```bash
# 1. 编译
go build -o deployd .

# 2. 测试手动部署（前台模式，日志输出到终端）
./deployd deploy hello-world -c /tmp/test-config.yaml

# 3. 检查日志文件是否存在
ls -lh ~/.deployd/services/
```

### 服务器验证（Linux）

```bash
# 1. 编译
export GO111MODULE=on GOPROXY='https://goproxy.cn,direct'
go build -o /home/deployd/deployd .

# 2. 重启 daemon
cd /home/deployd && ./deployd stop && ./deployd start -c config.yaml

# 3. 测试手动部署
./deployd deploy hello-world

# 4. 验证日志路径
ls -lh ~/.deployd/services/

# 5. 查看服务日志
./deployd logs hello-world -n 20

# 6. 实时跟踪
./deployd logs hello-world -t

# 7. 测试 webhook
# push 到仓库，验证日志写入服务专属文件
```

---

## 已知限制

1. **Logger 实例缓存**：当前实现缓存 logger 实例直到进程退出，不会自动关闭文件句柄。对于长时间运行的 daemon，这通常不是问题。

2. **日志轮转**：当前不支持日志轮转（log rotation）。如果服务频繁部署，日志文件可能会很大。未来可以考虑集成 logrotate。

3. **并发写入**：同一服务的日志写入是串行的（Mutex 保护），但不同服务的日志可以并发写入。

---

## 未来改进

1. **结构化日志**：可以考虑支持 JSON 格式输出，便于日志聚合和分析
2. **日志级别**：添加 INFO/WARN/ERROR 级别支持
3. **日志轮转**：集成 logrotate 或实现自动轮转
4. **远程日志**：支持将日志发送到远程日志服务（如 ELK）
