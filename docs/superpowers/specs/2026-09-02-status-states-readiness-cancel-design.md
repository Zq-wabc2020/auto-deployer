# 服务状态升级 / 就绪判定 / 取消机制 / artifact 多文件 设计

**创建日期：** 2026-09-02
**状态：** 设计待评审
**关联分支：** feat/status-states-readiness-cancel（从 feat/deployment-lifecycle-redesign 迁出）
**关联文档：** 生命周期重设计 `2026-08-13`、问题审计 `2026-08-22`、服务管理 `2026-08-02`、合并队列 `2026-08-09`、邮件通知 `2026-07-24`

---

## 0. 边界与约束

本设计**不删减任何既有功能**。合并队列、per-service flock 部署锁、日志方案A、服务级日志隔离、SMTP/Resend 通知、收件人合并、webhook 解析与多服务匹配、Jenkins 风格 fetch、SSH 密钥、Java 版本检测、Linux fork / `--locked`、配置优先级与向导、PID 进程管理、`logs -n/-t`、`timeout` 总预算、产物 pattern 语义、未知字段警告——全部保留。

新增/变更集中在三处内层：**服务状态机与持久化**、**部署成功判定（就绪门控）**、**中途取消**，外加一个小型配置增强（`artifact` 列表 / `health` 强制）。外层（队列 / 锁 / 日志 / 通知 / webhook / fork）原样保留。

---

## 1. 背景与目标

### 1.1 用户四项诉求

| # | 诉求 | 现状缺陷 |
|---|---|---|
| 1 | 服务状态增加「启动中」「启动失败」；deploy/webhook/svc start/restart 都要有中间态与失败态；启动中再次启动要报错（加锁）；`status`/`svc <name>` 用颜色区分（停止灰/运行绿/启动蓝/失败红） | 现仅 `running`/`stopped`/`unknown`，无中间态；状态按需探测、不持久化；`svc start/restart` 不持锁，并发启动无互斥；终端无颜色 |
| 2 | 部署成功判定改为「服务最终是否部署完毕/就绪」，覆盖全部 5 种类型 | 现成功 = `Start` 返回 nil（`sh -c` 已起 + pid 文件已写），**无健康/存活门控**；应用起了但没真正就绪（端口没起/依赖未连/启动即崩）会被误判成功并发成功邮件 |
| 3 | 支持中途取消部署中/启动中的服务（手动部署/webhook/手动启动），避免启动中一直占用而无法手动取消 | 无取消路径；只有 `timeout` 的 ctx deadline 能中止 build/stage；fork 出去的手动部署子进程 PID 不落盘，无法从外部精准取消 |
| 4 | 说清 `deploy.artifact`/`deploy.dest` 语义；支持只拷贝两个指定文件（如 hello1.txt、test.json） | `artifact` 是**单个** glob/目录字符串；Go `filepath.Glob` 无 brace 展开，两个任意文件不支持 |

### 1.2 目标

在不删减功能的前提下，建立：**带中间态/失败态的状态机 + 持久化 + 颜色**、**基于 health 的就绪门控**、**跨进程可取消**、**artifact 多文件**。满足兼容性、扩展性、逻辑严谨性、封装性、性能；兼容不同 Linux 发行版。

---

## 2. 现状（已审计确认的代码事实）

- **状态**：`deploy.GetServiceStatus`（`orchestrator.go:194`）透传到各插件 `Status()`。PID 模型（jvm/node/python）= pid 文件 + `Signal(0)`；static = `deploy.health` URL HTTP 2xx/3xx；docker = `docker ps`。值 `running`/`stopped`/`unknown`。**无任何持久化状态文件**（仅 pid 文件）。
- **锁**：per-service flock `~/.deployd/run/<name>.deploy.lock`（空文件，仅 flock）。`deployd deploy` 用 `TryAcquire`（失败快返「部署中」）；webhook 队列用 `Acquire`（阻塞）。**`svc start/restart/stop` 不持锁**（`service_start.go:55`/`service_restart.go:55`/`service_stop.go:55` 直调 `deploy.ServiceStart/Stop/Restart`，无锁）。
- **成功判定**：`Deploy` 跑 `fetch→Build→Stage→(Stop)→(Start)→notify`；`result.Status="success"`（`orchestrator.go:159`）iff fetch+Build+Stage+Start 全 nil。`process.Manager.Start/StartShell`：`cmd.Start()` + reap goroutine + **同步写 pid 文件** + return nil，**不复查存活、无 health/就绪探测**。
- **超时**：`deployCtx = WithTimeout(ctx, svc.timeout)`（默认 30m）覆盖 fetch/build/stage；Stop/Start 用 parent `ctx`（无超时）。build/stage 命令经 `RunCommandCtx`，ctx 到期杀整组。
- **取消**：无。fork 手动部署子进程 PID 仅打印到 stdout，不落盘。
- **颜色**：无 ANSI 库（deps 仅 cobra + yaml.v3）。
- **artifact**：单 glob/目录字符串（jvm/static/node 有；docker/python 无）。`build.CopyArtifact` 语义：字面目录→拷内容到 dest 根；通配 pattern 匹配目录→`dest/<目录名>/`；跳过 `*.original.jar`；无匹配报错。

---

## 3. 设计理念变更（显式声明）

| # | 变更点 | 旧 | 新 | 影响 |
|---|---|---|---|---|
| 1 | 状态集合 | running/stopped/unknown | + starting/start_failed（共 5，含 unknown 兜底） | 状态机 + 持久化（见 §4/§5） |
| 2 | 状态持久化 | 不持久化（按需探测） | starting/start_failed 落盘 `.state` 文件 | 新增状态文件（见 §5） |
| 3 | 成功判定 | Start 返回 nil | health 就绪门控通过 | 就绪轮询（见 §6） |
| 4 | `deploy.health` 适用范围 | 仅 static | **全部 5 类型强制必填** | 配置校验 + 向导（见 §10） |
| 5 | `timeout` 语义 | fetch+build+stage | fetch+build+stage+**就绪** | 行为变更（见 §10.3） |
| 6 | svc start/restart 互斥 | 无锁 | TryAcquire（启动中/部署中报错） | 复用 deploylock（见 §5/§7） |
| 7 | 中途取消 | 不支持 | sentinel 文件 + orchestrator 自取消 | 新命令 `deployd cancel <name>`（见 §7） |
| 8 | 终端颜色 | 无 | ANSI 4 色，非 TTY 去色 | 自带辅助，无新依赖（见 §8） |
| 9 | `artifact` 形态 | 单字符串 | 字符串**或列表** | 多文件拷贝（见 §9） |

---

## 4. 服务状态机

### 4.1 状态集合与颜色

| 状态 | 含义 | 颜色 | 来源 |
|---|---|---|---|
| `starting` | 正在部署/启动中（流水线未结束） | 蓝 | `.state` 文件 + 锁持有 |
| `running` | 服务就绪（health 通过 / 进程存活） | 绿 | 实时探测 |
| `stopped` | 已停止 | 灰 | 实时探测 |
| `start_failed` | 上一次 deployd 管控的部署/启动失败 | 红 | `.state` 文件（粘性） |
| `unknown` | 探测异常（EPERM / health fetch 错 / docker ps 错） | 暗黄 | 兜底 |

### 4.2 状态转换

```
                  ┌──────────────────────────────────────┐
                  │  (deployd deploy / svc start/restart 入口) │
                  └─── 写 .state=starting + TryAcquire 锁 ─┘
                                   │
            ┌──────────────────────┼──────────────────────┐
            ▼                      ▼                      ▼
      health 就绪通过         流水线任一阶段失败       手动取消(-x/Ctrl-C)
   (Deploy/Start 成功)    (含就绪超时=timeout 到期)    (ctx.Err()==Canceled)
            │                      │                      │
   清除 .state            写 .state=start_failed       清除 .state
   → running(绿,探测)     → start_failed(红,粘性)      → 探测决定(stopped/running)
                                   │
                          下一次 deployd 管控的
                          deploy/start/restart/stop
                                   │
                            清除 .state(转 starting 或 stopped)
```

### 4.3 status 汇报优先级（`deployd status` / `deployd svc <name>`）

1. `.state=starting` **且** deploylock 被持有 → `starting`（蓝）。
2. `.state=starting` **且** deploylock 空闲 → 陈旧（部署进程已死）→ 恢复为 `start_failed`（红），并重写 `.state=start_failed`（使后续稳定）。
3. `.state=start_failed` → `start_failed`（红）。**粘性**：即使实时探测显示 running，仍报红，直到下一次 deployd 管控操作清除（见 §4.4）。
4. 无 `.state` 文件（或已被清除）→ 实时探测：running/stopped/unknown。

> 设计要点：`.state` 文件**只**承载 `starting`/`start_failed` 两种持久态；`running`/`stopped`/`unknown` 始终由实时探测决定（进程是否存活/health/docker ps），不落盘。成功时**清除** `.state`（让探测说话），失败时**写入** `start_failed`。

### 4.4 `start_failed` 粘性语义（用户决策 Q3-A）

- **持久**：`.state=start_failed` 后，`status` 始终报红，**即使外部手动把服务拉起**（探测到 running）也不转绿——失败痕迹不被外部动作意外清除。
- **清除时机**：下一次 deployd 管控的 `deploy`/`svc start`/`svc restart`（转 `starting` 再判定）**或** `svc stop`（转 `stopped` 灰）。
  - **stop 也清除**（用户决议 #1，§15.1）：显式停止一个失败服务后转 `stopped`（灰）符合直觉；stop 是「我知道它失败了、主动下线」的确认动作，不应继续卡红。
  - **转绿（running）的唯一路径**（用户决议 #5，§15.5）：失败后，经 deployd 管控的 `deploy`/`svc start`/`svc restart`/webhook 重新启动**且 health 成功** → 清除 → running（绿）。新操作仍失败则继续红。
- 非.deployd. 管控的外部动作（手动 `java -jar`、外部 `docker run`、pm2 等）**不**清除 `.state`（粘性，Q3-A）。

### 4.5 陈旧 `starting` 恢复

- `.state=starting` 但 deploylock 空闲 = 部署进程在写入 `starting` 后崩溃/被强杀、未来得及清/写失败态。
- 处理：`status` 探测到此时，**就地重写** `.state=start_failed`（视为一次失败），报红。原因：部署进程异常中断 = 部署未成功 = 失败。便于排查。
- 锁作为「部署在途」的唯一可信存活信号（pid 不可靠：webhook 跑在 daemon 内，daemon 存活不代表本次部署存活）。

---

## 5. 状态文件与锁

### 5.1 状态文件

- 路径：`~/.deployd/run/<name>.state`
- 格式：JSON
  ```json
  { "status": "starting", "since": "2026-09-02T15:04:05+08:00", "stage": "build" }
  ```
  - `status`：`starting` | `start_failed`（只这两种会落盘）。
  - `since`：进入该状态的时间戳（用于日志/排查，不用于判定——判定靠锁）。
  - `stage`：进入 starting 时记录的当前阶段（fetch/build/stage/start/readiness），便于 `status` 输出「启动中（build 阶段）」。
- 写入时机：
  - Deploy/Start 入口：写 `starting`（+stage）。
  - 任一阶段失败（含就绪超时）：写 `start_failed`。
  - 成功：**删除**文件。
  - 手动取消：**删除**文件（让探测决定；§7 详述）。
- 并发安全：状态文件读写短促。`.state` 的 `starting`/`start_failed` 写入方为唯一部署/启动进程（已被 deploylock 串行化）。**唯一例外**：`status` 读方在探测到陈旧 `starting`（锁空）时，会就地重写为 `start_failed`（§4.5）——此时无部署在跑（锁空），故无并发写方，安全。读时遇到半写文件（JSON 解析失败）→ 视为无 `.state`，走实时探测（降级安全）。

### 5.2 锁复用（单一 per-service「忙碌」锁）

- **复用** `~/.deployd/run/<name>.deploy.lock`（flock），语义升级为「该服务正在 deploy 或 start/restart」。一把锁、一个含义，避免双锁死锁面。
- 各入口获取方式：

| 入口 | 获取方式 | 拿不到时 |
|---|---|---|
| `deployd deploy`（手动） | `TryAcquire`（非阻塞） | 「服务 X 正在启动/部署中，请勿重复操作」 |
| webhook（队列） | `Acquire`（阻塞） | 排队等（合并语义不变） |
| `svc <name> -s`/`-r`（start/restart） | `TryAcquire` | 「服务 X 正在启动/部署中，请勿重复操作」 |
| `svc <name> -t`（stop） | **不获取** | 停止无需互斥（停止一个死/活进程安全） |
| `deployd cancel <name>` | **不获取** | 仅写 sentinel（见 §7） |

- 生命周期不变：进程退出（正常/崩溃/被 kill）→ fd 关闭 → flock 自动释放；Linux fork 子进程经 `ExtraFiles` 继承 fd，`ReleaseInherited` 处理（既有机制）。
- **`svc start/restart` 非阻塞**（用户决议 #2，§15.2）：Linux 下像 `deploy` 一样 **fork 到后台**（setsid + 锁 fd 继承 + `--no-fork/--locked`，复用 `forkDeploy` 模式）。父进程 TryAcquire 成功后立即返回「已在后台启动，用 `deployd status` 查看」，**不阻塞终端**。子进程：写 `.state=starting` → `Start` → 就绪轮询 → health 通过才清 `.state`（实时探测→running 绿）；失败写 `start_failed`。macOS / `--no-fork` 前台（同 deploy）。
- **不引入新锁**，避免 deploy lock + start lock 交叉等待的死锁。

### 5.3 `status`/`svc <name>` 实现路径

- `cmd/status.go` 与 `cmd/root.go runSvcShortFlags`（无旗标分支）统一走新函数 `deploy.GetServiceStatusRich(ctx, svc, deployer)`：
  1. 读 `.state` 文件 + `deploylock.IsHeld(name)`（新增 `IsHeld`：`TryAcquire` 成功则立即 `Release` 并返回 false；失败返回 true）。
  2. 按 §4.3 优先级决定 `starting`/`start_failed`。
  3. 否则调插件 `Status()`（既有）得 running/stopped/unknown。
  4. 颜色渲染（§8）。
- `internal/daemon/commands.go Status`（现死代码，按审计 C2 未处理）：一并改为走 `GetServiceStatusRich`，消除「static 在 daemon 侧永远 stopped」的残留 bug。

---

## 6. 就绪判定（health 门控）

### 6.1 机制（非忙循环）

`Start` 成功后（static 在 `Stage` 后）进入就绪阶段，跑在 `deployCtx`（即 `timeout` 总预算的 ctx）下。**这是状态由 `starting`→`running` 的唯一闸门**：health 不通过就不清 `.state`，`status` 持续显蓝；只有 health 通过才清 `.state` 让实时探测转绿。对 `deploy`（fork 子进程）与 `svc start/restart`（同样 fork 后台，§5.2）均跑在后台子进程内，不阻塞终端：

```
deadlineCtx, deadlineCancel := WithTimeout(ctx, svc.timeout)   // 既有 30m 预算
deployCtx, manualCancel := WithCancel(deadlineCtx)             // 手动取消层
defer deadlineCancel(); defer manualCancel()
go sentinelWatcher(name, manualCancel)                         // §7：探测 .cancel 文件则调 manualCancel
... fetch/build/stage 用 deployCtx（既有）...
deployer.Start(deployCtx, svc)                                // 启动
// 就绪轮询：
t := time.NewTicker(svc.healthInterval)                        // 默认 10s，可配
defer t.Stop()
for {
    // 进程存活快速失败：启动即崩秒级感知，不傻等
    if 进程/docker模型 && !aliveNow() { → start_failed（启动后立即退出） }
    code := httpGet(svc.health, 3s 超时)                        // 单次探测自带 3s 超时
    if 200 <= code < 400 { → 成功：清除 .state，return running }
    select {
    case <-t.C:                                                // 10s 间隔，ticker 等待，非 for 空转
    case <-deployCtx.Done():
        switch deployCtx.Err() {
        case Canceled:        → 手动取消（§7）
        case DeadlineExceeded: → 就绪超时 = start_failed（发失败邮件，同 build/stage 超时语义）
        }
    }
}
```

### 6.2 关键点

- **非忙循环**：用 `time.Ticker`（10s 默认）+ `select`，绝不裸 `for` 无等待。
- **进程存活快速失败**：PID 模型查 pid；docker 查 `docker ps`。服务启动即崩（端口没起/依赖未连/配置错/OOM）秒级捕获，**不必等满 timeout**。static 无进程，跳过此检查，仅靠 health。
- **HTTP 探测**：单次 3s 超时；挂死的端点不会占满窗口；非 2xx/3xx（含连接拒绝、503）视为未就绪，继续轮询。
- **间隔可配**：`deploy.health_interval`（Go duration，默认 `10s`）。
- **无独立 `health_timeout`**（用户决策）：就绪阶段复用 `timeout` 总预算。`deployCtx.Err()` 区分两种结束：
  - `DeadlineExceeded` → 总预算到期 → `start_failed` + 失败邮件（与 build/stage 超时=失败一致）。
  - `Canceled` → 手动取消 → §7 路径（不发失败邮件）。
- **`timeout` 语义扩展**（§3 变更 #5 / §10.3）：原 fetch+build+stage → 现 fetch+build+stage+**就绪**。正常情况下 build 几分钟、就绪几十秒，30m 充裕；病态慢 build 挤占就绪时间则整体超时失败（可接受）。

### 6.3 各模型落地

| 模型 | aliveNow() | 就绪信号 | 说明 |
|---|---|---|---|
| jvm | pid `Signal(0)` 存活 | health 2xx/3xx | 启动即崩→pid 死→秒级 start_failed |
| node | pid 存活 | health | 同上 |
| python | pid 存活 | health | 同上 |
| docker | `docker ps` running | health | 容器即退→秒级 start_failed |
| static | （无进程，跳过） | health | Stage（拷贝+nginx reload）后直接进就绪轮询 |

### 6.4 成功/失败邮件

- 成功：health 通过 → `sendNotify(stage="", status="success")`（既有，不变）。
- 失败：新增就绪失败分支 → `sendNotify(stage="readiness", status="failed", err="health 未在 timeout 内就绪" 或 "进程启动后立即退出")`。与现有 fetch/build/stage/start 失败分支并列。
- 手动取消：**不发邮件**（§7）。

---

## 7. 中途取消

### 7.1 机制：sentinel 文件 + orchestrator 自取消

跨进程通用（daemon 内 / fork 子进程 / 前台 CLI），无需 IPC：

- 取消命令 `deployd cancel <name>` 仅做：检查 deploylock 是否被持有（`IsHeld`）；未持有 → 打印「服务未在部署/启动中，无需取消」并退出；已持有 → 写 sentinel 文件 `~/.deployd/run/<name>.cancel`（空文件），打印「已发出取消信号」，退出。
- orchestrator（在 deploy/start 进程内）入口起一个 `sentinelWatcher` goroutine：每 ~1s 探测 `<name>.cancel` 是否存在；存在则调 `manualCancel()`（取消 `deployCtx`）→ 并**删除** sentinel（防下一次部署误触发）。
- ctx 取消后：
  - build/stage：`RunCommandCtx` 的 `ctx.Done()` 触发 → 杀整组 → 阶段返回 ctx 错误。
  - 就绪轮询：`select` 的 `<-deployCtx.Done()` 触发 → `Canceled` 分支。
- orchestrator 在每个阶段错误分支前判别：若 `deployCtx.Err()==Canceled` → **走取消路径**（不发失败邮件、按 §7.2 处理半起服务、清 `.state`），而非普通失败路径。

### 7.2 取消时的服务处置（用户决策 Q2-A）

取消 = 人为操作，**不发失败邮件**。按取消发生时流水线位置分：

| 取消时位置 | 旧服务状态 | 处置 | 结果状态 |
|---|---|---|---|
| fetch/build/stage（Stop 旧之前） | 旧服务仍在跑 | 中止流水线，**不动旧服务** | 清 `.state` → 探测 = running（旧） |
| Stop→Start 之间或 Start/readiness 中 | 旧已停、新半起 | 中止 + **Stop 新的半起进程/容器** | 清 `.state` → 探测 = stopped |
| static 的 Stage 中 | （无进程） | 中止拷贝/reload | 清 `.state` → 探测 = health 决定 |

- 规则一句话：**中止未完成阶段；若本次部署已 Start 新进程则 Stop 之；不重启旧服务；清 `.state`；不发邮件。**
- 结果状态由实时探测决定（running 若旧服务幸存；stopped 若新服务被 Stop 且旧已停）。
- docker 取消：`docker stop` 半起容器。
- static 取消：中止 `CopyArtifact`/`nginx reload`（都在 `deployCtx` 下，可中断）；结果由 health 探测。

### 7.3 命令与跨进程

- 命令：`deployd cancel <name>`（顶层，与 `deploy` 同级——属部署控制动作，非服务态切换，故不进 `svc` 短旗标）。用途见 §1.1#3：取消该服务正在进行的部署/启动（`starting` 态）。实现：`IsHeld` 判定在途 → 写 `~/.deployd/run/<name>.cancel` sentinel → 打印「已发出取消信号」退出；未在途 → 提示「服务未在部署/启动中，无需取消」。
- 同时支持前台进程的 Ctrl-C（SIGINT）：orchestrator 捕获 SIGINT → 同 `manualCancel()`（与 sentinel 等价）。对后台 fork 子进程（deploy/svc start 的 setsid 子进程）SIGINT 不直达，须用 `deployd cancel <name>`。
- 跨进程有效：sentinel 是文件，任何 CLI 进程都能写；部署进程（daemon/fork 子进程/前台）都轮询它。无需 IPC。

### 7.4 边界

- sentinel 残留：orchestrator 入口先 `os.Remove(<name>.cancel)` 清理可能的上次残留，再起 watcher。取消处理后也删。无残留误触发。
- 无在途部署时写 sentinel：取消命令靠 `IsHeld` 判定，未持锁则不写、提示用户。
- watcher 泄漏：goroutine 在 `deployCtx.Done()` 后退出（`select` 同时听 ctx），不泄漏。
- fork 子进程：sentinel watcher 跑在子进程内；子进程被强杀 → 锁释放 → `.state=starting` 走 §4.5 陈旧恢复为 `start_failed`。
- 取消生效粒度：shell 命令阶段（fetch/build/migrate/nginx reload）经 `RunCommandCtx`，ctx 取消即杀整组，**即时**中断；`CopyArtifact`（Go 文件 I/O，原无 ctx）改为接受 `ctx` 并在**每个文件之间** `select{case <-ctx.Done(): return err}`，使取消能在拷贝途中尽快生效（即便不改，按审计 C1 典型拷贝 <1s，取消延迟到拷贝完成也可接受——但改之更严谨）。

---

## 8. 颜色输出

### 8.1 状态→颜色映射

| 状态 | ANSI | 说明 |
|---|---|---|
| starting | 蓝（`\033[34m`） | 启动中 |
| running | 绿（`\033[32m`） | 运行中 |
| stopped | 灰/暗（`\033[90m`） | 停止 |
| start_failed | 红（`\033[31m`） | 启动失败 |
| unknown | 暗黄（`\033[33m`） | 探测异常 |

### 8.2 实现

- 自带小型 ANSI 辅助（`internal/term/color.go` 或并入既有包），**不引入新依赖**（cobra/yaml 之外零增加）。
- 非 TTY（管道/重定向）自动去色：用**标准库** `os.Stdout.Stat()` 判断 `fi.Mode()&os.ModeCharDevice != 0`（字符设备=终端）；非终端则输出纯文本。不引入 `golang.org/x/term`。跨发行版一致（POSIX 字符设备语义通用）。
- `deployd status` 与 `deployd svc <name>`（无旗标）均上色；`logs`、`deploy`、邮件等不变（邮件已有 ❌/✅ emoji）。

---

## 9. artifact 多文件（用户诉求 #4）

### 9.1 回答

- `deploy.artifact` = **构建产物**（要拷贝的东西）；`deploy.dest` = **归位目录**；`Stage` 阶段把 `artifact` 拷到 `dest`。是的，`dest` 收的就是 `artifact` 指定的产物。
- 两个任意文件（`hello1.txt`、`test.json`）：**现不支持**（单 glob、无 brace 展开）。本设计扩展为支持。

### 9.2 扩展：`artifact` 接受列表

- `artifact` 既接受单字符串（既有），也接受列表：
  ```yaml
  deploy:
    artifact: ["hello1.txt", "test.json"]   # 列表
    # artifact: "target/*.jar"               # 或单字符串（既有）
    dest: "/opt/app"
  ```
- 语义：列表每项**独立**用 `filepath.Glob` 解析（相对 workspace）；每项按既有 pattern 语义处理（字面文件→平铺到 dest 根；字面目录→拷内容；通配→匹配项按既有规则）。
- 多项拷贝结果**汇总**：`CopyArtifact` 返回全部已拷贝基名列表（既有返回值，jvm `cleanWorkspace` keep 集用）。
- 配置解码：`artifact` 字段类型从 `string` 改为 `interface{}`/自定义 `UnmarshalYAML`，兼容单字符串与列表（参考 `build.command` 既有「单条或列表」模式）。jvm/static/node 三个有 artifact 的插件共用此解码。
- 错误：任一项无匹配 → 报错（既有「无匹配」语义不变）。

### 9.3 边界

- 列表项之间不应有 dest 内冲突（同名文件后者覆盖前者，按列表顺序）。文档注明。
- 不引入 `**`（Go 标准库限制，既有不变）。

---

## 10. 配置变更

### 10.1 `deploy.health` 全类型强制必填

- 现仅 static 要求。新：jvm/static/node/python/docker **都必填** `deploy.health`（HTTP 健康检查 URL）。
- config 校验（`config.Load`）：缺 `deploy.health` → **报错拒绝加载**（明确提示「<name> 缺少 deploy.health，就绪判定必需」）。
- docker 的 health 指向容器映射端口（如 `http://localhost:8000/health`）；运维需确保各服务暴露 health 端点。

### 10.2 `deploy.health_interval`（新增，可选）

- 默认 `10s`（用户决策）。Go duration 语法。
- 放在 `deploy:` 下（与 `health` 同级）。

### 10.3 `timeout` 语义扩展

- 现：fetch+build+stage 总预算。
- 新：fetch+build+stage+**就绪**总预算。默认 30m 不变。
- README/配置模板注释更新为「fetch+build+stage+就绪 总超时」。
- 既有 `warnLegacyBuildTimeout`（旧 `build.timeout` 迁移）不变。

### 10.4 `deploy.health`/`health_interval` 解析归属

- 两者为**全类型通用**字段，但物理上仍写在 `deploy:` 块内（与 static 既有用法一致，减少迁移）。
- 中心解析：`config.go` 在交付 `Deploy yaml.Node` 给插件前，先从该 node 解出 `health`/`health_interval` 存入 `ServiceConfig.Health`（新增字段 `HealthConfig{URL, Interval}`）。插件 `Status()`（static）与 orchestrator 就绪轮询统一读 `svc.Health`，不再各自持 `Health` 字段（移除 `staticDeployConfig.Health`，改读 `svc.Health`）。
- `StrictDecodeDeploy` 白名单加入 `health`/`health_interval`（视为通用字段，不触发「未知字段」警告，避免 B12 误报）。

### 10.5 迁移与向导

- 旧 jvm/node/python/docker 配置无 `deploy.health` → 启动报错提示补 health（一次性配置补全；服务少，成本低，同 §9 既有「单一新格式」权衡）。
- 向导 `config/wizard.go`：对全部模型引导填 `deploy.health`（必填）与可选 `health_interval`。
- `config.yaml.example`：各模型块补 `deploy.health` 示例 + `health_interval` 注释。

---

## 11. 既有功能兼容性矩阵（逐项）

| 既有功能 | 处置 |
|---|---|
| 同服务部署合并队列 | **保留**。队列→锁→Deploy 不变；Deploy 内新增就绪/取消不感知队列 |
| per-service flock 部署锁 | **保留并复用**为「deploy+start/restart」忙碌锁；语义升级，获取方式不变 |
| 日志方案A（应用 stdout→/dev/null） | **保留**。就绪轮询不捕获应用 stdout |
| 服务级日志隔离 | **保留**。就绪/取消日志走 `logger.GetServiceLogger(name)` |
| SMTP/Resend 通知 | **保留**。新增 `readiness` 失败分支；取消不发 |
| 收件人合并 / 只发一次 | **保留**。取消不发，不破坏「一次」 |
| webhook 解析与多服务匹配 | **保留** |
| Jenkins 风格 fetch + 回退 clean | **保留** |
| SSH 密钥 / Java 版本检测 | **保留** |
| Linux fork + setsid / `--locked` | **保留**。fork 子进程内增 sentinel watcher 与状态写入 |
| macOS 前台 / `--no-fork` | **保留** |
| 配置加载优先级 / config.path 兜底 | **保留** |
| 交互式向导 | **保留并扩展**（引导 health） |
| PID 进程管理 | **保留**。`Manager` 增加 `Alive()` 供就绪快速失败（内部用既有 `pidAlive`） |
| `logs -n/-t` | **保留** |
| `timeout` 总预算 | **保留**，语义扩展含就绪 |
| 产物 pattern 语义 | **保留**，`artifact` 增列表形态 |
| 未知字段警告（B12） | **保留**，白名单加 health/health_interval |
| daemon 侧 `Status` 死代码 | **修正**为走 `GetServiceStatusRich`（顺手修 C2 残留） |

**结论：零功能删减**；状态机/就绪/取消为新增，锁/通知/日志/队列/fork 原样保留。

---

## 12. 严谨性考量

- **死锁**：仅一把 per-service 锁，获取顺序单一（入口→释放），无交叉等待；`svc start/restart` 用 `TryAcquire`（不阻塞），不存在「等锁方持其它锁」环。
- **资源占用**：sentinel watcher goroutine 随 `deployCtx.Done()` 退出，不泄漏；就绪轮询 `time.Ticker` defer Stop；HTTP 探测 3s 超时 + `defer resp.Body.Close()`，不积压连接。
- **死循环**：就绪轮询用 ticker+select，必有出口（health 通过 / 进程死 / ctx Done）；无裸 for。
- **陈旧状态**：`.state=starting` + 锁空 → 恢复 start_failed（§4.5）；半写 JSON → 降级实时探测（§5.1）；sentinel 残留 → 入口清理（§7.4）。
- **竞争**：状态文件写方唯一（锁串行化）；读方降级安全。`status` 的 `IsHeld` 与部署方获取锁之间存在极短 race（status 探测锁瞬间部署方刚获取/释放），影响仅是「偶发显示非 starting」，下一次刷新即正确，可接受。
- **发行版兼容**：ANSI 用标准转义码；非 TTY 检测用字符设备判定（不依赖平台专用 API）；`health` 是普通 HTTP，无平台依赖；取消 sentinel 是文件，跨发行版一致。
- **fork 子进程取消**：sentinel watcher 在子进程内；子进程被强杀 → 锁释放 → 陈旧恢复（§4.5）。daemon 内取消 → watcher 在 daemon goroutine，取消该次部署 ctx，不影响 daemon 其它服务。

---

## 13. 分阶段落地

| 阶段 | 内容 | 验证 |
|---|---|---|
| P1 | `.state` 文件读写 + `GetServiceStatusRich` + 颜色 + `IsHeld` + 陈旧恢复 | `status` 四色显示；starting/failed 正确；非 TTY 去色 |
| P2 | `svc start/restart` TryAcquire 锁 + 入口写 starting + Linux fork 后台（复用 forkDeploy） | 启动中再 start 报错；start 中 status 显蓝；父进程立即返回 |
| P3 | 就绪轮询（health 门控）+ `health` 全类型强制 + `health_interval` + timeout 语义扩展 | jvm/docker 启动即崩→start_failed；health 通过→running；就绪超时→start_failed+邮件 |
| P4 | 取消 sentinel + watcher + `deployd cancel <name>` + SIGINT + §7.2 半起处置 | 各阶段取消正确；不发邮件；半起新进程被 Stop |
| P5 | `artifact` 列表 + `CopyArtifact` 汇总 + 向导/example/README | 两文件拷贝成功；单字符串不回归 |
| P6 | daemon 侧 `Status` 走 `GetServiceStatusRich`（修 C2 残留） | static 在 daemon 侧不再永远 stopped |

每阶段独立可验证、可回滚，不破坏外层（队列/锁/日志/通知）。

---

## 14. 验证计划（ubuntu22 @ OrbStack）

- 验证环境：OrbStack 进 ubuntu22 虚拟机；打包命令与 `config.yaml` 在 `/home/auto-deploy/` 下。
- 用例：
  1. jvm 正常部署 → `status` 绿；启动即崩（改错端口）→ 红 start_failed + 失败邮件。
  2. docker 同上。
  3. static 部署 → health 通过绿；health 不通红。
  4. 部署中 `status` 显蓝；再次 `deploy`/`svc -s` 报「正在启动/部署中」。
  5. `deployd cancel <name>` 取消 build 中 / 就绪中的部署 → 中止、半起被 Stop、无邮件、status 灰/红正确。
  6. `--no-fork` 前台 `svc start` + Ctrl-C → 同上（Linux 后台 fork 的用 `cancel` 命令）。
  7. kill fork 子进程 → 锁释放、`.state=starting` 恢复为 start_failed。
  8. `artifact: ["hello1.txt","test.json"]` → 两文件拷到 dest。
  9. 非 TTY（`deployd status > file`）→ 无 ANSI。
  10. 旧配置无 `deploy.health` → 启动报错提示补 health。

---

## 15. 决议（spec 评审已确认）

1. **stop 清除 start_failed → stopped（灰）**（§4.4）：确认。stop 是「主动下线」确认动作，转灰，不再卡红。
2. **svc start/restart 非阻塞 + 状态管理**（§5.2）：确认。Linux 下像 `deploy` 一样 **fork 到后台**（复用 `forkDeploy` 模式：setsid + 锁 fd 继承 + `--no-fork/--locked`）；父进程立即返回「已在后台启动，用 `deployd status` 查看」，不阻塞终端。子进程：写 starting → Start → 就绪轮询 → **只有 health 通过才清 `.state`**（→ running 绿）；失败写 start_failed。macOS/`--no-fork` 前台（同 deploy）。
3. **`health_interval` 仅每服务级**（§10.2）：确认。`deploy.health_interval` 默认 10s，每服务可配；**不**设 config 顶层全局默认（YAGNI）。
4. **取消命令 = `deployd cancel <name>`**（§7.3）：顶层命令，与 `deploy` 同级。用途：取消该服务在途的部署/启动（`starting` 态），即用户诉求 #3。不进 `svc` 短旗标（控制动作，非服务态切换）。
5. **start_failed 转绿条件**（§4.4）：失败后，经 deployd 管控的 `deploy`/`svc start`/`svc restart`/webhook 重新启动**且 health 成功** → 清除 → running（绿）；新操作仍失败则继续红。外部（非 deployd）拉起不影响（粘性，Q3-A）。

---

## 16. 文件变更清单（预估）

| 文件 | 变更 |
|---|---|
| `internal/deploy/orchestrator.go` | Deploy 内插入就绪轮询；ctx 改 `WithCancel(WithTimeout)`；sentinel watcher；取消路径判别；`readiness` 失败分支；`GetServiceStatusRich` |
| `internal/deploylock/lock.go` | 新增 `IsHeld(name)` |
| `internal/process/manager.go` | 暴露 `Alive()`（复用 `pidAlive`） |
| `internal/config/config.go` | `ServiceConfig.Health`；中心解 `health`/`health_interval`；`StrictDecodeDeploy` 白名单；校验 health 必填 |
| `internal/config/validate.go` | health 必填校验 |
| `internal/config/wizard.go` | 引导 health/health_interval |
| `internal/build/artifact.go` | `CopyArtifact` 支持 artifact 列表（单字符串兼容）；签名加 `ctx context.Context`，文件间检查取消 |
| `plugins/springboot/plugin.go` | artifact 列表解码；`Health` 改读 `svc.Health` |
| `plugins/static/plugin.go` | 同上 |
| `plugins/node/plugin.go` | 同上 |
| `plugins/docker/plugin.go` | `Health` 改读 `svc.Health` |
| `plugins/python/plugin.go` | `Health` 改读 `svc.Health` |
| `cmd/status.go` | 走 `GetServiceStatusRich` + 颜色 |
| `cmd/root.go` | `svc <name>` 无旗标走 `GetServiceStatusRich`+颜色 |
| `cmd/cancel.go`（新增） | `deployd cancel <name>`：`IsHeld` 判定 → 写 sentinel |
| `cmd/service_start.go`/`service_restart.go` | Linux fork 到后台（复用 `forkDeploy` 模式 + `--no-fork/--locked`） + TryAcquire 锁 + 写 starting + 前台 SIGINT 处理 |
| `cmd/service_stop.go` | 清 `.state`（stop 清除 start_failed → stopped） |
| `internal/daemon/commands.go` | `Status` 走 `GetServiceStatusRich` |
| `internal/term/color.go`（新增） | ANSI 辅助 + 非 TTY 检测 |
| `config.yaml.example` / `README.md` | health 全类型、health_interval、timeout 含就绪、artifact 列表、颜色说明 |
| 测试 | 状态机/就绪/取消/artifact 列表/颜色 各单测 + e2e |
