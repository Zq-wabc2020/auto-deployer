# 工作项流水线 + 组件化重构设计

- 日期：2026-10-01
- 分支：`feat/pipeline-components`（基于 main @ c60f92f）
- 状态：待审阅

## 1. 背景与目标

当前 deployd 以「服务类型」为核心：六种 type（jvm/springboot/docker/static/node/python）各自的插件实现 Build/Stage/Start/Stop 语义，deployd 内置了大量部署知识（进程管理、就绪门控、五态状态机、artifact 归位）。本次重构将这些全部拆掉：

**服务 → 工作项（pipeline）**：每个工作项是一条任意节点数量的流水线模板，节点以**组件**方式执行。deployd 只做编排（拉参数、跑节点、超时、互斥、记录状态），不再内置任何部署知识。

设计考量优先级：资源开销（内存/CPU）、轻量化、方案通用性。

## 2. 角色边界（已确认的决策）

| 决策点 | 结论 |
|---|---|
| deployd 角色 | **完全放手**：只管流水线编排。服务进程生命周期（启停、PID、健康探测）归用户 shell 脚本 |
| status 显示 | 各工作项**最后一次流水线执行状态**（success/failed/cancelled），不是服务健康状态 |
| shell 组件执行位置 | **仅本机**。要操作远程机器由用户自己写 `ssh user@host '...'`（复用本机 SSH 配置/密钥），deployd 不内置 SSH 客户端 |
| daemon 生命周期 | `deployd start/stop` 保留（只管 daemon 本身，不涉及工作项） |
| `deployd exec` 执行模型 | **前台执行**：实时输出各节点日志，Ctrl+C 等价取消；与 daemon webhook 触发共用同一把 flock 竞争锁互斥 |
| `deployd cancel` | **保留**（修正最初「去掉 cancel」的想法）：取消执行中的工作项，状态记为 cancelled（区别于 failed） |

## 3. 配置结构

```yaml
server: {host, port}            # 保留
webhook: {secret}               # 保留现状（预留未启用）
smtp / resend / notifications   # 原样保留，供 email 组件使用

pipelines:                      # 取代 services
  - name: my-springboot-app     # 唯一标识，命令行引用
    workspace: "/opt/deployd/apps/my-springboot-app"   # 工作目录
    timeout: "45m"              # 整条流水线总预算，默认 30m（Go duration 语法）
    env:                        # 环境变量参数（工作项级静态键值对，值为字面量）
      JAVA_OPTS: "-Xmx512m"
    stages:
      - name: 拉取代码           # 工作项内必须唯一（output 引用命名空间）
        type: git               # 组件类型
        params:
          url: "https://github.com/user/repo.git"
          branch: ["main", "release/*"]   # 匹配其一
        output: {v: "${commit}"}           # 可选：结果映射成命名参数
        skip: ${env.skipBuild}  # 可选：插值后为 "true" 则跳过
        when: failure           # 可选：failure / always，缺省 = 主流程
        timeout: "10m"          # 可选：单节点超时，缺省只有总预算
```

### repo 收敛到 git 组件（已确认）

`repo` 顶层字段取消，url/branch 只在 git 组件声明一份。配套规则：

1. **webhook 匹配索引**在配置加载期从「每个工作项第一个 `type: git` 的节点」提取 url/branch，两项**必须是字面量**（写 `${...}` → 启动校验报错）。
2. **无 git 节点的工作项只能手动 `exec`**（纯脚本任务：清理、备份、通知类流水线），不参与 webhook 匹配。
3. **checkout 分支**：webhook 触发 = 命中的分支；手动 exec = 列表第一项。

### 校验规则（配置加载期）

- stage name 工作项内唯一；type 必须已注册；when 枚举合法；timeout 为合法 duration
- git 触发节点的 url/branch 为字面量
- 必填 params（如 shell 的 `sh`、email 的 `subject`/`body`）缺失即报错
- 未定义的参数引用：**不在加载期做全量静态检查**（引用可指向运行期才有的 output/system 值），运行期节点启动时求值失败即该节点失败并报明确错误——不静默当空串

## 4. 四套参数

引用语法 `${前缀.键}`，统一插值器，值可以是参数引用与字面量的拼接（如 `"部署 ${system.name} @ ${output.拉取代码.v}"`）。

| 参数集 | 前缀 | 来源 | 生成时机 |
|---|---|---|---|
| 系统参数 | `${system.*}` | name、trigger（manual/webhook）、commit、branch、author、message、pushers（webhook 合并任务的作者列表）、workspace、result（success/failed，仅后置节点有值）、failed_stage | 运行期，每次执行生成一份 |
| 环境变量参数 | `${env.*}` | 工作项 `env:` 块 | 配置期定义 |
| 命令行参数 | `${args.*}` | `deployd exec xxx --foo=bar` → `args.foo=bar`；webhook 触发时为空集 | 触发时 |
| 节点输出参数 | `${output.<节点名>.<键>}` | 各节点 `output:` 映射结果 | 节点执行后累积 |

**系统参数变更**：旧设计的 `system.repo.*` 取消（repo 收敛进 git 组件）；webhook 触发信息（commit/branch/author/message/pushers）来自 payload，进 `system`；git 相关运行值走 git 节点自己的 output。

### 组件结果变量与 output 映射

组件执行后产生一组**裸名结果变量**，只能在**本节点的 `output:` 值里**用裸名引用（`output: {app_version: "${stdout}"}`），映射后才能被后续节点以 `${output.<节点名>.<键>}` 引用。`output` 值也可以是字面量或四套参数引用（透传）。

- `stdout` 等大结果存内存，**超过 1MB 截断**（防构建日志撑爆参数集；完整日志在日志文件里）

### skip / when 求值

- `skip`：插值后等于字符串 `true` 即跳过（不做表达式语言）
- `when`：枚举 `failure` / `always`，缺省 = 主流程

## 5. 后置执行机制（when）

回答「失败必发邮件 + 成功发邮件 + 无论成败都清理」的抽象：

- **缺省 when** = 主流程节点：按列表顺序执行，任一失败则跳过剩余主流程节点（fail-fast）
- `when: failure` = 仅主流程失败时执行
- `when: always` = 无论成败都执行

主流程结束后（成功或失败），后置节点（when=failure/always）按**列表顺序**执行。成功邮件 = 主流程末尾的普通 email 节点；失败邮件 = `when: failure` 的 email 节点；清理 = `when: always` 的 cleanup 节点。

- 后置节点自身失败：只记日志，不递归触发其他后置节点，不影响最终状态
- 取消（cancelled）：`always` 节点仍执行（清理语义）；`failure` 节点不执行（cancelled ≠ failed）
- 本机制等价于 GitHub Actions `if: always()/failure()` 的扁平化

## 6. 四种组件

组件接口统一为：`Run(ctx, 插值后的 params) → (结果变量 map, error)`，注册表按 `type` 名分发（保留 registry 模式，接口从五个方法收缩为一个）。

| 组件 | params | 结果变量 | 说明 |
|---|---|---|---|
| `git` | `url`*、`branch`*（列表）、`workspace`（缺省 `${system.workspace}`） | `commit`、`changed`（bool，无新提交）、`branch` | 复用现有 fetch 快路径（fetch + reset --hard origin/<branch>）+ 失败回退干净克隆；HTTPS 自动转 SSH |
| `shell` | `sh`*（单行或多行 YAML 块）、`cwd`（缺省 workspace）、`env`（本次执行额外环境变量） | `exit_code`、`stdout`、`stderr` | 写临时脚本文件、`sh` 执行、等退出码；多行块即多行脚本（Jenkins `sh '''` 对应物）；进程组信号继承现有 StartShell 机制（超时杀得干净）；非零退出码 = 失败 |
| `email` | `to`（缺省 = 全局 `notifications.to` + `system.pushers`）、`subject`*、`body`* | `error`（发送失败信息） | 复用现有 SMTP/Resend 发送层；subject/body 全参数插值 |
| `cleanup` | `keep`（glob 列表，缺省 `[".git"]`） | `deleted`（清理条目数） | 删 workspace 下不在 keep 内的一切；`.git` 缺省保留是为了下轮 fetch 快路径 |

`*` = 必填（有缺省值者除外）。

节点失败定义：组件返回 error，或节点超时。

## 7. 执行引擎

一次执行（exec 前台或 daemon 队列触发，同一套引擎）：

1. **生成参数集**：system + env + args，output 集为空
2. **主流程**：按列表顺序执行 when 缺省节点；skip=true 标记 skipped 不执行；任一失败 → 记录 `failed_stage`，跳过剩余主流程节点
3. **后置流程**：按列表顺序执行 when 匹配结果的节点
4. **状态落盘**：结果 = success / failed（含超时）/ cancelled

### 超时层级

- 总预算 ctx（默认 30m）罩全程（复用 `WithTimeout`）
- 节点 `timeout` 是总 ctx 的子 ctx；节点超时 → 该节点 failed（reason=timeout），后置节点照常

### 取消

- `deployd cancel <name>` 写 `~/.deployd/run/<name>.cancel` sentinel（复用现有机制），执行器轮询命中即取消当前节点
- 取消后执行 `when: always` 节点，不执行 `when: failure` 节点，状态 = cancelled
- exec 前台时 Ctrl+C / SIGINT / SIGTERM 等价 cancel

### 锁与队列（原样保留现有设计）

- 每工作项一把 flock（`~/.deployd/run/<name>.lock`）
- webhook 触发不直接执行，入每工作项队列；队列处理器阻塞抢锁，持锁期间 drain 队列只执行**最新任务**，被合并任务的作者并入 `system.pushers`
- `deployd exec` TryAcquire 非阻塞抢锁：队列忙（pending 或 in-flight）直接拒绝
- 一条 push 匹配多个工作项（monorepo）时各工作项独立队列独立执行

## 8. 状态与日志

### 状态（取代五态状态机）

状态文件 `~/.deployd/run/<name>.status`（JSON，仅执行进程写）：

```json
{
  "state": "success | failed | cancelled",
  "trigger": "manual | webhook",
  "started_at": "...", "finished_at": "...",
  "commit": "abc1234", "branch": "main",
  "failed_stage": "构建", "failure_reason": "exit_code=1 / timeout / ...",
  "stages": [{"name": "拉取代码", "state": "success|skipped|failed", "duration": "3s"}]
}
```

- 执行期间先写 `running`（含 PID）
- `deployd status` 读到 `running` 但 flock 未被持有 → 判定执行进程已死，就地改写为 `failed`（reason=进程中断）——沿用现有「陈旧恢复」思路，是唯一非执行方的写例外（锁空保证无并发写方）
- `deployd status`：列出所有工作项，彩色显示 状态/触发方式/耗时/commit 短 sha/失败节点

### 日志

- `~/.deployd/logs/<name>.log` 追加写（保留日志隔离），按 run 分节：`=== <时间> <name> [webhook main abc1234] ===`，节点内 `--- [节点名] ---` 分节
- shell 节点 stdout/stderr 原样落入日志文件（参数集里那份是 1MB 截断副本）
- `deployd logs <name>`：显示最近一次 run 完整日志；`-n <行数>` 限制、`-f` 跟随
- exec 前台时日志同时输出到终端和日志文件

## 9. CLI 全集

| 命令 | 行为 |
|---|---|
| `deployd start / stop` | daemon 启停（保留 Linux fork / macOS 前台差异） |
| `deployd status` | 所有工作项最后执行状态 |
| `deployd exec <name> [--key=value...]` | 前台执行，实时输出，Ctrl+C=cancel，TryAcquire 抢锁；`--key=value` 进 `${args.*}` |
| `deployd cancel <name>` | 取消执行中的工作项（sentinel） |
| `deployd logs <name> [-n N] [-f]` | 最近一次 run 各节点日志 |
| `deployd config` | 交互式向导，改造为生成 pipeline 格式（保留） |

## 10. 设计理念变更（vs 旧设计）

1. **服务类型 → 组件编排**：从「六种 type 各自的 Build/Stage/Start/Stop 语义」变为「四种正交组件 + 用户 shell 自由组合」。deployd 不再内置任何部署知识
2. **deployd 不再拥有进程**：PID 管理、进程组信号、就绪门控、五态状态机全部删除。服务死活 deployd 不再知道
3. **通知从框架行为变为节点**：成功/失败邮件由用户编排（when 机制），不配邮件节点就完全不发（旧版全局通知行为消失）
4. **取消语义变化**：从「停半起服务」变为「停当前节点 + 跑 always 节点」
5. **健康检查没有内置替代**：需要就绪等待就在 shell 节点里自己写轮询 curl

## 11. 兼容矩阵（旧能力 → 新去向）

| 旧能力 | 去向 |
|---|---|
| 五种服务类型插件 | **删除**，由 shell+git+cleanup 组合表达（迁移文档提供 springboot 等五类对照示例） |
| fetch 快路径/干净克隆/HTTPS→SSH | **完全保留**（git 组件） |
| 部署竞争锁 flock | **完全保留** |
| 队列合并最新任务 + 作者并入通知 | **保留并泛化**（作者进 `system.pushers`，email 节点 to 缺省引用它） |
| 五态状态机/健康探测/start_failed 粘性 | **删除** → 流水线执行状态 |
| PID 文件/进程组信号/僵尸回收 | **删除**（进程生命周期归用户脚本） |
| svc -s/-t/-r 免构建启停 | **删除**（用户写 restart 流水线或用 systemd） |
| deploy/cancel 命令 | **变更** → exec / cancel（取消语义变化，见第 10 节） |
| 内置成功/失败邮件 | **保留为可选**（email 组件 + when） |
| artifact 归位/迁移/nginx reload | **删除**（shell 节点 cp/rsync/nginx -s reload 自理） |
| timeout 总预算语义 | **完全保留** + 新增节点级 timeout |
| 日志隔离/彩色 status/配置发现顺序/校验向导 | **完全保留** |
| 旧 config.yaml | **不兼容**，需手改（迁移文档给出五种 type → 组件编排对照） |

## 12. 代码组织

**新增**：
- `internal/pipeline`：执行引擎（主流程/后置流程、fail-fast、超时层级、取消轮询）
- `internal/param`：四套参数集 + 统一插值器 + skip/when 求值
- `internal/components/{git,shell,email,cleanup}` + 轻量组件注册表

**改造**：
- `internal/config`：pipeline 结构 + 加载期校验（含 webhook 匹配索引提取）
- `internal/runstate`（取代 servstate）：status 文件读写 + 陈旧恢复 + cancel sentinel
- `cmd/`：exec / cancel / logs / status / config 向导

**原样保留**：`internal/webhook`、`internal/deployqueue`、`internal/deploylock`、`internal/notify`、`internal/logger`、`internal/term`

**删除**：`plugins/`、`internal/registry`（旧五方法接口）、`internal/process`、`internal/build`、`internal/servstate`、`internal/deploy`

## 13. 测试策略

- 单测：插值器（四套参数、未定义引用报错、截断）、when/skip 求值、配置校验（stage 重名/字面量检查）、runstate 读写与陈旧恢复
- 组件单测：git（本地裸仓库 fetch/克隆/changed）、shell（多行/退出码/超时杀进程组）、cleanup（keep glob）、email（现有 SMTP 测试沿用）
- 引擎集成测：主流程 fail-fast、后置 always/failure 组合、取消后 always 仍执行、总/节点超时、锁互斥（exec TryAcquire 拒绝）
- e2e：沿用 OrbStack ubuntu22 验证机的轻量 e2e 手法（真实 git 仓库 + shell 流水线 + 状态/日志断言）
