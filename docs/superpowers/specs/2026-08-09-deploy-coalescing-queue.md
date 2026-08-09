# 同服务部署合并队列 + 日志方案A 设计

**创建日期：** 2026-08-09
**分支：** feat/deploy-coalescing-queue
**状态：** 设计待评审

---

## 1. 背景与目标

### 1.1 问题一：应用运行日志污染服务日志
- webhook 触发时，daemon 常驻，插件 `cmd.Stdout = p.output`（*logger.Logger 非 *os.File）经"管道+goroutine"持续把应用运行期 stdout 写入服务日志 → 服务日志 = 部署日志 + 应用运行日志，无限增长。
- 手动 fork 触发时，fork 子进程退出 → goroutine 死、管道关 → 应用启动日志丢失。
- 两者不一致，且 webhook 场景日志无限增长。

**目标（方案A）：** 服务日志只放部署流水线日志；应用运行日志交给应用自身日志框架（logback 等）。webhook 与手动行为一致。

### 1.2 问题二：同服务并发部署损坏工作区
- 同服务并发部署共享 workspace，`git init/rm -rf .git/fetch/checkout` + maven 构建互相破坏、PID 文件互写、端口抢占。
- 当前无任何串行化机制。

**目标：** 同服务部署串行化；webhook 连续推送合并为一次（只部署最新）；手动部署优先级最低，队列忙时直接失败提示。

---

## 2. 方案A：应用运行日志不进服务日志

### 改动
`plugins/springboot/plugin.go` 的 `Start`：
- `cmd.Stdout = nil`、`cmd.Stderr = nil`（exec 对 nil 连 /dev/null，无管道无 goroutine）。
- 保留 `fmt.Fprintf(p.output, "started %s with pid %d\n", ...)`（这是部署流水线消息，仍进服务日志）。

### 效果
- 服务日志只含部署流水线日志（orchestrator、git、maven、`[build]`、`[springboot]` 部署消息）。
- 应用运行 stdout 写入 /dev/null，不进服务日志，不无限增长。
- webhook 与手动行为一致（都不捕获应用 stdout）。

### 前提（文档注明）
应用需配置文件日志（如 logback 写自己的文件），否则应用运行日志将丢失。部署工具不再承担应用日志收集职责。

### 不变
- `Build`/`ExecuteBuild`/`moveJarToRoot` 的部署期输出仍走 `p.output`（服务日志），不变。
- `Stop` 的 `stopped process N` 仍走 `p.output`，不变。

---

## 3. 合并队列设计

### 3.1 协调机制：per-service 文件锁（flock）

**锁文件：** `~/.deployd/run/<name>.deploy.lock`（独立空文件，非日志文件，非 PID 文件）。

**语义：** 锁被持有 = 该服务"忙碌"（队列有待处理任务 或 正在部署）。

**两种获取方式：**
| 触发源 | 获取方式 | 拿不到时 |
|--------|----------|----------|
| webhook（队列处理器） | 阻塞 `flock(LOCK_EX)` | 等待（排队） |
| 手动部署 | 非阻塞 `flock(LOCK_EX\|LOCK_NB)` | 立即失败"部署中，请勿重复操作" |

### 3.2 webhook 合并队列（daemon 内，每服务一个）

**组件：** `internal/deployqueue/scheduler.go`
- `Scheduler` 持有 `map[service]*serviceQueue`（mutex 保护）。
- 每个 `serviceQueue`：一个 `chan Task`（缓冲 256）+ 一个处理 goroutine。

**Task 模型：**
```go
type Task struct {
    ServiceName string
    Branch      string
    RepoURL     string
    AuthorEmail string  // 操作人：webhook=提交作者(payload)；manual=""
    Source      string  // "webhook" | "manual"
}
```

**处理流程（单 goroutine 串行）：**
```
for {
    first := <-ch                          // 阻塞等任务（不持锁）
    lock.Acquire(svc)                      // 阻塞拿锁（等手动/其他释放）
    for {
        tasks := [first] + drain(ch)       // 合并：排空待处理，取最新
        latest := tasks[len-1]
        discarded := tasks[:len-1]
        // 1) 丢弃任务日志留痕（写服务日志）
        // 2) 收集所有 tasks 的 AuthorEmail 去重 -> recipients
        deploy(latest, recipients)         // 执行（调 orchestrator.Deploy）
        select {
        case first = <-ch: continue        // 执行期间又来任务，保持锁，继续合并
        default: lock.Release(); break     // 队列空，释放锁，回外层阻塞等
        }
    }
}
```

**关键点：** 锁在"队列有待处理任务"期间持续持有，仅在队列空时释放。因此手动 try-lock 能正确感知"队列有任务"。

**Submit（webhook 调用）：**
```go
select { case ch <- task: default: log.Printf("[queue] %s 队列满，丢弃任务", svc) }
```
缓冲满（极端高频）时丢弃，但此时 fetch 取的是分支最新 commit（已含被丢推送的代码），仅该作者不在邮件收件人。可接受。

### 3.3 手动部署（try-lock 失败快返）

**语义：** 手动优先级最低。锁占用 → 立即失败提示；锁空闲 → 执行。

**Linux fork（保留后台 + 抗 SSH 断连）：**
- 父进程 `forkDeploy`：`deploylock.TryAcquire(svc)`（非阻塞）。
  - 失败 → 打印"服务 X 正在部署中，请勿重复操作"，return（不 fork）。
  - 成功 → `cmd.ExtraFiles = []*os.File{lockFile}` 把锁 fd 传给子进程；子进程命令加 `--locked` 标志；父进程 close 自己的锁 fd 后 return（锁由子进程继承的 fd 持有）。
- 子进程 `deploy --no-fork --locked`：见 `--locked` 跳过 try-lock（锁已继承），直接跑 `Deploy`；进程退出时 fd 关闭、锁自动释放。

**macOS / 显式 `--no-fork`（前台）：**
- `deploylock.TryAcquire(svc)`：失败 → "部署中"提示；成功 → 跑 `Deploy` → defer 释放。

**新增 cobra 标志：** `--locked`（内部用，forkDeploy 传给子进程，标识锁已继承）。

### 3.4 跨进程串行验证（flock 跨进程有效）

| 场景 | 行为 |
|------|------|
| webhook × webhook（同服务） | 队列合并（取最新），阻塞锁串行 |
| 手动 × 手动（同服务） | 第一个 try-lock 成功执行；后续 try-lock 失败"部署中" |
| 手动 + webhook（同服务） | 锁占用方先跑；手动 try-lock 遇锁占用即失败；webhook 阻塞等锁 |
| 不同服务并发 | 各自独立锁/队列/工作区，互不干扰 |

### 3.5 通知收件人合并

- 合并执行时，收件人 = 所有合并任务（丢弃 + 最新）的 `AuthorEmail` 去重（非空）+ `cfg.Notifications.To`。
- 丢弃任务的作者也收到最终部署结果邮件（因其推送被合并进本次部署）。
- 手动任务 `AuthorEmail=""`，不进收件人（无邮箱），但进日志留痕。

### 3.6 orchestrator 变更

- `Deploy(ctx, svc, cfg, deployer, operatorEmails []string)`：新增 `operatorEmails`。
- `sendNotify`：收件人 = `operatorEmails`（非空去重）+ `cfg.Notifications.To`；若 `operatorEmails` 为空（手动直触），回退用 `GetLatestAuthorEmail`（保持旧行为）。
- 邮件正文"变更者"仍用 `GetLatestAuthorEmail`（部署代码的最新提交者）。
- 丢弃任务日志留痕：由队列处理器通过 `logger.GetServiceLogger(svc.Name).Printf` 写服务日志。

---

## 4. 边界与严谨性

1. **锁释放：** 进程退出（正常/崩溃/被 kill）→ fd 关闭 → flock 自动释放。不会死锁。
2. **子进程崩溃：** 继承的锁 fd 随进程退出关闭，锁释放。
3. **Deploy panic：** 队列处理器用 `defer lock.Release()` + `recover`，确保释放。
4. **锁文件残留：** flock 释放后空文件可残留，无害（下次复用）。
5. **收件人去重：** map 去重，空字符串过滤。
6. **队列满：** 缓冲 256，满时丢弃任务并日志告警（代码仍由 fetch 最新 commit 覆盖）。
7. **daemon 重启：** 队列内存态丢失（未持久化）。部署是短时操作，重启时大概率无在飞任务；重启后锁文件残留但未持有（flock 非持久），新部署可正常获取。可接受。
8. **`--locked` 误用：** 用户手动传 `--locked` 会导致不持锁即部署（绕过串行）。该标志不暴露 help（内部标志），降低误用。

---

## 5. 性能

- flock：内核态互斥，开销极低（微秒级），仅在部署边界获取/释放。
- 队列：in-memory channel，无额外 IO；每服务仅一个处理 goroutine，无 goroutine 爆炸。
- Submit：非阻塞 select，不阻塞 webhook HTTP goroutine。
- 无新磁盘 IO（锁文件仅 flock，不写内容）。

---

## 6. 变更文件清单

| 文件 | 变更 |
|------|------|
| `internal/deploylock/lock.go` | **新增** - flock 文件锁：Acquire(阻塞)、TryAcquire(非阻塞)、Release |
| `internal/deployqueue/scheduler.go` | **新增** - 合并队列：Submit、处理 goroutine、合并/丢弃/收件人收集 |
| `internal/webhook/server.go` | 改 - webhook 改为 `scheduler.Submit(task)`，不再直接调 Deploy |
| `internal/daemon/daemon.go` | 改 - 初始化 Scheduler，注入 webhook |
| `cmd/deploy.go` | 改 - try-lock + fork ExtraFiles + `--locked` 标志 |
| `internal/deploy/orchestrator.go` | 改 - Deploy 增加 `operatorEmails` 参数 |
| `plugins/springboot/plugin.go` | 改 - 方案A：Start 的 cmd.Stdout/Stderr = nil |
| `internal/deploylock/lock_test.go` | **新增** - 锁测试 |
| `internal/deployqueue/scheduler_test.go` | **新增** - 合并/丢弃/收件人测试 |
| 其他测试 | 改 - 适配 Deploy 新签名 |

---

## 7. 验证

1. `go build ./...`、`go test ./...`、`go vet ./...` 通过。
2. 合并队列单测：连发 3 个 webhook 任务（不同作者），断言只执行 1 次（最新），丢弃 2 个留痕，收件人含全部 3 个作者去重。
3. try-lock 单测：锁占用时 TryAcquire 失败。
4. 手动 × 手动：第一个执行，第二个立即失败"部署中"。
5. webhook + 手动：webhook 部署中，手动失败；手动部署中，webhook 排队等。
6. 方案A：服务日志无应用运行日志（只剩部署流水线）。
