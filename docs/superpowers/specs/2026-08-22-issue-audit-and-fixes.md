# 2026-08-22 重设计落地后问题审计与修复

> 背景：2026-08-13 生命周期重设计（见 [2026-08-13-deployment-lifecycle-redesign.md](2026-08-13-deployment-lifecycle-redesign.md)）在
> Ubuntu 22 服务器上实际使用后，用户反馈了一批问题。本文档逐条记录根因、结论与修复设计，
> 作为后续开发的留痕。日期均为 2026 年。

---

## 一、问题清单（含根因与结论）

### A1. 构建/部署失败后工作空间没有清除 —— 【设计如此，维持】

**现象**：构建或部署失败后，workspace 里的源码和构建产物没有被清掉。

**清除逻辑**：清理只发生在 jvm 插件的 `Stage()`（`plugins/springboot/plugin.go`），且必须同时满足三个条件：
配置了 `deploy.artifact` -> 构建成功 -> 产物**成功拷贝到 dest 之后**。核心原则是"产物安全落盘后才清源码"，
失败时故意保留现场便于排查和重试。

**用户决策（2026-08-22）**：失败不清理，维持现状。唯一要求是"每次拉取代码都保证覆盖"——
该保证成立：`fastFetch` 每次执行 `checkout -f` + `reset --hard origin/<分支>`，已跟踪文件每次都强制等于远端。
（未跟踪的构建残留会留下，已知并接受。）

### A2. `switch_java: command not found` —— 【机制说明 + 使用约定】

**现象**：build 命令里写 `switch_java 8` 报 command not found，但终端里手动执行正常；
`["source ~/.bash_profile", "switch_java 8", ...]` 却能用。

**根因**：`ExecuteBuild` 用 `sh -c` 起的是**非登录、非交互** shell。`~/.bash_profile` 只有登录
shell 才加载，`~/.bashrc` 只有交互 shell 才加载，所以函数没被定义。终端里能用是因为登录交互
bash 已经 source 过 profile。加 source 能用是因为 build 命令列表用 `" && "` 拼成一条字符串交给
同一个 `sh -c`，函数对后续命令可见。

**环境变量到底从哪来**：`exec.Command` 默认继承父进程环境，链路是
`启动者的 shell -> deployd -> fork 的 daemon -> sh -c "mvn ..."`。所以**谁启动 daemon，
构建环境就是谁的**。之前 Ubuntu 上报 "mvn is not installed" 正是因为 daemon 从一个没有 mvn
环境变量的上下文启动。这是与 cron/systemd/Jenkins 相同的固有特性，不是缺陷。

**用户决策（2026-08-22）**：继续用 source 写法。
**注意**：`source` 是 bash 语法，Ubuntu 的 `/bin/sh`（dash）不识别（报 `source: not found`），
POSIX 通用写法是 `. ~/.bash_profile`（bash/dash 都认），且函数体需避免 bash 特有语法。

### A3. 无超时；deployd stop 与部署锁的交互 —— 【修复：超时见 B6】

**现象与结论**：
- 修复前整个 pipeline 没有任何超时，挂死的 git/mvn 会永远占着部署锁。
- `deployd stop` 的影响范围：
  - **webhook 触发的部署**在 daemon 进程内跑：stop 杀 daemon（进程组杀，连带 mvn 构建子进程），
    flock 锁随进程死亡自动释放；
  - **手动 `deployd deploy`** 在 Linux 上 fork 出独立后台进程（Setsid），deployd stop 管不到它，
    它会跑完并一直持锁。
- "stop 后仍提示正在部署中"大概率是真的在部署（fork 的手动部署子进程还在后台跑）。
  排查方法：`ps -ef | grep 'deployd deploy'` 或 `lsof ~/.deployd/run/<svc>.deploy.lock`。

### A4. workspace 固定保留 `*.jar` —— 【修复：见 B7】

**根因**：`cleanWorkspace` 按 `*.jar` 后缀保留是为了兼容"artifact 配了但 dest 没配"（dest 默认
= workspace 根）的场景——刚拷到根目录的 jar 不能被自己清掉。副作用是**历史版本 jar 永远累积**。

### A5. webhook 只能匹配一个服务 —— 【缺陷，修复：见 B1】

**根因**：`MatchService` 返回第一个 repo+branch 匹配的服务就 return，同仓库同分支的第二个服务
永远不会被触发。而 monorepo 场景（jvm 后端 + static 前端共用一个仓库分支）是合理需求。

### A6. static 的 artifact 写 `dest/` 行不行 —— 【不支持，修复：见 B2】

**实测**（Go `filepath.Glob` 语义）：

| pattern | 匹配结果 |
|---|---|
| `dist/` / `dist` | 目录本身 -> 旧代码 `copyFile` 读目录报 `is a directory` |
| `dist/*` | 文件和子目录 -> 子目录同样报错 |
| `dist/**` | 无 doublestar 语义，等价于 `dist/*` |

即旧代码只支持**扁平结构**，前端嵌套产物（`dist/js/`、`dist/css/`）必挂。

### B1. `deployd start` 失败没有终端反馈 —— 【修复：见 B4】

**根因**：fork 模式下父进程 spawn 子进程后立刻打印 "daemon started in background"，不确认子进程
是否活着；子进程的报错（config/环境校验失败）只写进 `<configDir>/.deployd/daemon-fork.log`，
终端零反馈，"启动成功"是假象。

### B2. fetch 失败没收到邮件 —— 【代码已有通知，服务器二进制过旧】

P1 修复轮已给 fetch 失败补上 `sendNotify(stage="fetch")`（`internal/deploy/orchestrator.go`）。
服务器跑的还是旧二进制所以没收到。分支不存在（`couldn't find remote ref`）本身是远端/配置问题。

**git clone 交互确认**：不用担心。`SSHCommand` 用
`ssh -i <key> -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null`，不会卡交互；
日志里的 "Warning: Permanently added" 就是自动接受的痕迹（known_hosts 指向 /dev/null，
每次都重新加一遍，无害但刷屏）。

### B3. 日志没有时间戳 —— 【修复：见 B3】

**根因**：`logger.Printf` 前缀只有 `"[服务名] "`。修复后工具自身日志行带
`2006-01-02 15:04:05` 时间戳；子进程透传输出（构建日志）保持原样不加时间。

### B4. 其他目录执行 `deployd status` 看不到服务 —— 【缺陷，修复：见 B5】

**根因**：每个 CLI 命令都是独立进程，各自找配置文件（优先 cwd，其次 `~/.deployd/config.yaml`）；
CLI 与 daemon 之间没有 IPC。且发现一个真 bug：**daemon 默认读 `~/config.yaml`，CLI 默认找
`cwd/config.yaml` 或 `~/.deployd/config.yaml`，两边默认路径不一致**。
webhook 侧每次事件都重新 loadConfig（配置改动免重启生效的机制，保留）。

### C1. static 第二次部署 vite build 被 SIGKILL -- 【环境问题：OOM killer】

**现象**（2026-08-26 服务器实测，新二进制）：`yarn build` 报
`error Command failed with signal "SIGKILL"`，随后 build failed: exit status 1（邮件正常收到）。

**诊断依据**：yarn **存活**并打印了错误 -- 说明只有 vite 的 node 子进程被杀。工具自身的超时
（`build.timeout`，默认 30m）杀的是**整个进程组**（sh + yarn + node 一起），错误会是
`build aborted (timeout or canceled): context deadline exceeded`，yarn 不会有机会输出。
能精确杀单个进程的只有内核 OOM killer。确认方法：

```bash
dmesg -T | grep -iE "oom|out of memory|killed process" | tail
free -h
```

**缓解**（按优先级）：加 swap；控制 node 堆（build 命令前缀
`NODE_OPTIONS=--max-old-space-size=1024`，仅让失败信息更可读）；换更大内存机器构建。

**产物拷贝性能评估**（用户要求重点确认）：`CopyArtifact` 为标准顺序逐文件拷贝
（`os.Create` + `io.Copy` 32KB 缓冲、无 fsync、目录树单次遍历），本地磁盘拷典型 dist
（几百文件/几十 MB）耗时远小于 1s，**无性能问题**；部署耗时主要在 vite build。
为便于定位，orchestrator 增加了每阶段耗时日志（fetch done in / build done in / stage done in）。

### C2. `deployd status` 对 static 显示 stopped -- 【缺陷，修复：见 B9】

**现象**：static 服务在 `deployd status` 里显示 stopped。

**根因**：`cmd/status.go` 对所有服务只查 **pid 文件**（`process.Manager.Status()`），绕过了
插件的 `Status()`。static 无进程/pid 文件，永远显示 stopped；设计上 static 的状态 =
`deploy.health` 健康检查（单服务命令 `svc <name>` 走插件是对的，聚合命令漏改）。
注：`internal/daemon/commands.go` 的 `daemon.Status` 是同逻辑的死代码（无调用方），未处理。

### D1. static/docker 部署成功后不清工作空间 -- 【设计如此，待用户拍板】

设计文档兼容矩阵明确把"构建后清理工作区"限定在 jvm（"保留为可选，jvm 原地启动时不清理"）。
static/docker 不清的原因：node_modules/构建缓存留在 workspace 是**增量构建的前提**，清掉 =
每次全量 yarn install（重新下几百 MB 依赖）。磁盘 vs 构建速度的权衡，待用户决策
（默认建议维持不清；如需可加 `deploy.clean_workspace` 可选项）。

### D2. `deployd logs -f` CPU 100% -- 【缺陷，修复：见 B10】

**根因**：`daemon.tailFollow` 读到 EOF 后直接 `continue` **无任何等待**，忙等循环空转。
修复：EOF 后 sleep 500ms 再读（轮询模式，同 `tail -f` 的 fallback 行为）。

### D3. 超时只覆盖 fetch/build -- 【修复：见 B10】

Stage（产物拷贝 / migrate / nginx reload）原先无超时；且 python migrate、nginx reload 用
`cmd.Run()` 不响应 ctx。修复：Stage 同样包 `build.timeout`，插件内改用
`build.RunCommandCtx`（超时杀整组）。Stop 本就有 10s 优雅+SIGKILL 升级；Start 是纯启动无需超时。

### D4. 配置模板不完整 -- 【修复：见 B11】

`config.yaml.example` 缺 python `env`、docker `env/volumes/args`、timeout 说明只在 jvm 块。
README 的 timeout 文档（配置表 + 注意事项）在上一轮已补，本轮统一描述为 fetch/build/stage。

### D5. node 服务 status unknown；artifact/dest 无效 -- 【根因：pm2 自守护；字段被静默忽略】

**现象**：node 服务（official）部署"成功"但 status 显示 unknown。

**根因一（主）**：run 写了 `pm2 start .output/server.mjs`。pm2 会 fork 自家守护进程后立刻退出，
`pm2 start` 命令结束 -> deployd 的 sh 包装进程退出 -> pid 失效；服务实际跑在 pm2 守护进程下，
**完全脱离 deployd 管理**（status 异常、Stop 杀不到）。与"run 不能写 nohup/&"是同一类约束：
**run 必须是纯启动命令**，pm2/supervisor/setsid 这类自守护工具都不行。Nuxt 用
`node .output/server.mjs`。
（status 三条分支：进程在=running；进程不在=stopped；`Signal(0)` 返回非 ESRCH 错误如 EPERM
=unknown。排查：`cat ~/.deployd/run/<name>.pid; ps -p <pid>`。）

**根因二**：official 配了 `deploy.artifact`/`dest`，但当时 node 插件只读 run/env（node 设计为
**源码即产物**，同 python），这两个字段被**静默忽略**。同类问题还有 official 的
`timeout: "10m"` 缩进到了服务层（当时应与 build.command 同级，被静默忽略）。

**2026-08-31 决议与落地**：
- node 增加 jvm 式 `artifact`/`dest` 支持（拷 `.output` 到 dest、run 在 dest 执行）——见 B14。
  原地启动（不配 artifact）仍是默认，源码即产物。
- timeout 改为服务层字段（见 D8），所以 official 那个"缩进到服务层"的 `timeout` 现在反而**正确**了。

### D6. python venv 报 ensurepip 不可用 -- 【环境问题：缺系统包，非发行版兼容问题】

Ubuntu 把 ensurepip 拆进独立的 `python3-venv` 包（Debian 系拆包习惯，RHEL 系无此问题）。
`apt install python3.10-venv` 解决（已在 OrbStack ubuntu22 虚拟机执行验证）。工具执行的是
用户配置的构建命令，无法代装系统包。

### D7. 配置字段写错位置/写错模型被静默忽略 -- 【修复：见 B12】

用户实测两次踩坑（D5 的 timeout 层级、node 的 artifact/dest），配置解析无任何提示。
修复：`config.Load` 增加**未知字段警告**（严格解码二次校验，stderr 提示行号与字段名）；
各插件 `deployConfig` 改用 `config.StrictDecodeDeploy` 严格解码 deploy 块，其他模型的专属字段
会打 warning。均为警告不阻断（保持向后兼容）。

### D8. timeout 语义 -- 【2026-08-31 重设计】

`timeout` 位于**服务层**（与 `build:` 同级，不在 build 内），是 fetch+build+stage 三个阶段的**总预算**：
Deploy 起首创建一个 `context.WithTimeout(ctx, timeout)`，三阶段共享该 deadline（任一阶段耗尽剩余
额度，下一阶段即超时）。Stop/Start 不计入（Stop 自有 10s 优雅+SIGKILL，Start 是纯启动）。
默认 `30m`，所有服务类型通用。

> 2026-08-31 修订：原先 `build.timeout` 在 `build:` 块内、三阶段各自独立计时（各拿全额），
> 语义误导（字段只跟 build 同级却管三个阶段）且最坏可达 3× 额度。改为服务级单总预算后更直观。
> 旧配置的 `build.timeout` 会被 `warnLegacyBuildTimeout` 提示迁移到服务层 `timeout`。

---

## 二、修复清单（本轮实现）

| # | 类型 | 内容 | 实现要点 |
|---|---|---|---|
| B1 | 缺陷 | webhook 多服务匹配 | `MatchService` -> `MatchServices` 返回**全部**匹配，逐个入队；合并语义不变（per-service 队列，跨服务互不合并、互不等待）；config 校验新增 **workspace 唯一性**（两个服务共享 workspace 会互相踩，部署锁按服务名加拦不住） |
| B2 | 缺陷 | 产物目录递归拷贝 | 字面量目录 pattern（`dist` / `dist/`，无通配符）= 拷**内容**到 dest 根；通配 pattern（`dist/*`）匹配到目录 = 拷成 `dest/<目录名>/` 子树。jvm/static 共用 |
| B3 | 缺陷 | 日志时间戳 | `logger.Printf` 前缀加时间；子进程输出透传不加时间 |
| B4 | 体验 | daemon 启动同步反馈 | fork 父进程轮询（最长 10s）：子进程写 pid 文件 = 就绪；子进程退出 = 失败，把 `daemon-fork.log` 尾部 30 行打到终端 |
| B5 | 缺陷 | 统一 config 路径解析 | 见下文"配置路径解析" |
| B6 | 改进 | fetch/build/stage 超时 | 服务级 `timeout`（Go duration 语法，默认 30m），是三阶段**总预算**（Deploy 起首一个 `context.WithTimeout`，fetch/build/stage 共享）；命令用 `Setpgid` 独立进程组，超时**杀整组**（防止只杀 sh 包装进程留下 mvn 孤儿——与 Stop 进程组杀同一原理）；config 校验 timeout 格式。2026-08-31 由"build 内每阶段独立"重设计为"服务级总预算"（见 D8/B13） |
| B7 | 改进 | 清理只保留本次产物 | `CopyArtifact` 返回实际拷贝的文件名列表，`cleanWorkspace(workspace, keep)` 只保留 keep + `.git` + `.java-version`；不再按 `*.jar` 后缀保留，历史 jar 不再累积 |
| B8 | 改进 | Linux JAVA_HOME 探测 | `build.FindJavaHome` 顺序：jenv -> `/usr/libexec/java_home`（macOS）-> 扫描 `/usr/lib/jvm/*`（Debian/Ubuntu 约定，兼容 `java-8-openjdk-amd64` 和 RHEL 风格 `java-1.8.0-openjdk`）；`.java-version` 机制在无 jenv 的 Linux 服务器生效；springboot 插件与 build 包的重复实现合并 |
| B9 | 缺陷 | status 走插件 | `deployd status` 对每个服务调用其部署模型的 `Status()`（pid 文件 / 健康检查 / docker ps），不再直查 pid 文件；orchestrator 每阶段输出耗时日志（fetch/build/stage done in） |
| B10 | 缺陷 | logs -f 忙等修复 + stage 超时 | `tailFollow` EOF 后 sleep 500ms；Stage 纳入总预算 `timeout`（不再单独包，2026-08-31 并入 D8）；python migrate / nginx reload 改用 `RunCommandCtx` |
| B11 | 改进 | 配置模板补全 | `config.yaml.example` 补 python env、docker env/volumes/args、node artifact/dest；timeout 在服务层（描述为 fetch/build/stage 总超时） |
| B12 | 改进 | 配置未知字段警告 | `config.Load` 严格解码二次校验（服务层字段写错位置有警告）；插件 deploy 块用 `StrictDecodeDeploy`（其他模型专属字段有警告）；均警告不阻断 |
| B13 | 重设计 | timeout 改服务级总预算 | `timeout` 从 `build:` 内移到服务层（与 build 同级），语义从"fetch/build/stage 各自独立全额"改为"三阶段共享一个总预算"（Deploy 起首一个 `context.WithTimeout`）；旧 `build.timeout` 触发迁移警告 `warnLegacyBuildTimeout`。见 D8 |
| B14 | 改进 | node artifact/dest 支持 | node 插件 `Stage` 配了 `artifact`+`dest` 时 `CopyArtifact`（语义同 jvm/static），`Start` 在 dest 执行；不配则源码即产物原地启动（同 python）。workspace 不清理（保留 node_modules 做增量构建）。解除 D5 根因二的字段静默忽略 |

### 配置路径解析（B5 统一后）

`deployd start` 成功加载并校验配置后，把**绝对路径**记录到 `~/.deployd/config.path`（每次启动覆盖写）。
所有命令的默认解析顺序统一为：

```
1. 当前目录 config.yaml
2. ~/.deployd/config.yaml
3. ~/.deployd/config.path 记录的路径（daemon 上次启动用的配置）
4. ~/config.yaml（旧 daemon 默认，兼容保留）
```

修复点：daemon 与 CLI 走同一个 `config.DefaultConfig()`，不再可能出现"daemon 读 A、status 读 B"。

### 产物 pattern 语义（B2 后）

| pattern | 行为 |
|---|---|
| `target/*.jar` | 扁平文件 glob（jvm 常规用法），跳过 `*.original.jar` |
| `dist` / `dist/` | 目录**内容**递归拷贝到 dest 根（static 前端推荐） |
| `dist/*` | 每个匹配项：文件平铺到 dest；目录拷成 `dest/<目录名>/` 子树 |
| `dist/**` | 不支持（Go 标准库无 doublestar），等价于 `dist/*` |

---

## 三、明确不做 / 维持现状

| 项 | 决策 | 理由 |
|---|---|---|
| 失败后清工作空间 | 不清 | 保留现场便于排查；fetch 已保证覆盖（A1） |
| git clean -fd 清未跟踪残留 | 不做 | `mvn clean` 类命令自清理兜底，用户接受残留 |
| 配置级 env 块 | 暂不做 | 现有手段（启动者环境继承 / source 写法 / .java-version）够用 |
| `switch_java` 做成独立脚本 | 不做 | 用户选择 source 写法（注意 dash 兼容性，见 A2） |
| static/docker/node 成功后清工作空间 | 不清（2026-08-30 拍板；node 含 artifact 归位时亦不清，2026-08-31） | node_modules/构建缓存是增量构建前提，清掉 = 每次全量 install（D1） |
| pm2/supervisor 等 run 写法 | 不支持（文档明确禁止） | 自守护工具使服务脱离 pid 管理（D5） |

## 四、待办（服务器收尾）

- [ ] 全部修复本地验证通过后：编译新二进制上传服务器 -> `deployd stop` -> 替换 -> `deployd start`（启动失败现在会直接打到终端）
- [ ] 手动 kill 服务器上无人跟踪的孤儿 applet java 进程（旧 bug 遗留，新代码也杀不到它）
- [ ] 重新 deploy admin/applet，`jps` 验证单实例
- [ ] 验证多服务匹配：同仓库同分支配两个服务，push 一次两个都触发
- [ ] 验证 `status` 在任意目录可见服务列表（config.path 兜底）

## 五、2026-08-31 修订（timeout 重设计 + node artifact/dest）

两处设计调整（用户拍板），已落地并在 ubuntu22 虚拟机验证：

1. **timeout 重设计**（D8/B6/B10/B11/B13）：原 `build.timeout` 在 `build:` 内、三阶段各自独立计时，
   语义误导（字段只跟 build 同级却管三阶段）且最坏可达 3× 额度。改为**服务层 `timeout`**，fetch+build+stage
   **共享一个总预算**（Deploy 起首单个 `context.WithTimeout`）。旧 `build.timeout` 由
   `warnLegacyBuildTimeout` 提示迁移。
2. **node 增加 artifact/dest**（D5/B14）：node 原为"源码即产物"，`artifact`/`dest` 被静默忽略。
   现支持 jvm 式归位（拷 `.output` 到 dest、run 在 dest 执行），与下次构建互不干扰；不配则原地启动。
   workspace 仍不清（保留 node_modules 做增量构建，同 static/docker）。

README、config.yaml.example、设计文档 2026-08-13 已同步更新。
