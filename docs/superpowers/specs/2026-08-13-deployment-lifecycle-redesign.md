# 部署生命周期重构设计

**创建日期：** 2026-08-13
**状态：** 设计待评审
**关联分支：** feat/deploy-coalescing-queue（在其基础上演进）
**关联文档：** 原始设计 `2026-07-21`、服务管理 `2026-08-02`、日志隔离 `2026-08-06`、合并队列 `2026-08-09`、邮件通知 `2026-07-24`、偏差记录 `2026-08-02`

---

## 0. 写在前面：本设计的边界与约束

本设计**不删减任何既有功能**。合并队列、flock 部署锁、日志方案A、服务级日志隔离、SMTP/Resend 通知、收件人合并、webhook 解析、Jenkins 风格 fetch、SSH 密钥、Java 版本检测、Linux fork / `--locked`、配置优先级、配置向导、PID 进程管理、`logs -n/-t` 等全部保留。

重构只触及**最内层**：部署生命周期的阶段划分、插件接口、配置结构、命令分层。外层（队列 / 锁 / 日志 / 通知 / webhook）不动。凡与旧设计理念有差异处，第 7 节逐条列出；既有功能在新设计中的处置，第 8 节逐项给出兼容性矩阵。

分层关系（重构范围仅在最内层）：

```
webhook ──► 合并队列 ──► flock 部署锁 ──► orchestrator.Deploy ──► [Build ▸ Stage ▸ Stop ▸ Start]
           (保留)         (保留)            (保留)                 ↑ 重构此处 + 插件 + 配置 + 命令
```

---

## 1. 背景与目标

### 1.1 现状缺陷（简述，详见历史讨论）

| # | 缺陷 | 表现 |
|---|---|---|
| 1 | `run.command` 把"产物归位"和"进程启动"耦合 | 重启时产物已被搬走，`mv` 失败，restart 必坏 |
| 2 | 缺少独立"归位"阶段 | 插件 `moveJarToRoot` + 用户脚本 `mv` 两套归位互相打架 |
| 3 | 命令不经 shell 执行（`SplitCommand`+`exec.Command`） | `>` `&` `&&` `\|` `$VAR` 等 shell 语义失效，复杂命令静默失败 |
| 4 | 自后台脚本（`nohup ... &`）破坏 PID 跟踪 | 记录的是已死脚本 PID，真正的 java 进程成孤儿，stop 不掉 |
| 5 | springboot 插件硬编码 Maven 假设 | 认死 `target/*.jar`、只留 jar、gradle/docker 不适用 |
| 6 | 不支持 Docker | Stop/Start/Status 是 PID 模型，docker 需要 `docker stop/run/ps` |
| 7 | 无插件注册表，6 处重复 `switch svc.Type` | 新增类型要改 6 个文件 |
| 8 | 配置把 JVM 假设当成通用 | `build.command`/`run.command` 顶层必填，static/docker 无法满足 |

### 1.2 目标

在不删减功能的前提下，建立一套**按部署模型抽象**的生命周期，满足：兼容性、扩展性、逻辑严谨性、封装性、性能。支持 `jvm` / `static` / `node` / `python` / `docker` 五类部署模型。

---

## 2. 核心设计原则

**原则一：`type` 按"部署模型"分类，不按"框架"分类。**
框架（Vue/React/FastAPI/Django）是项目自己的事；部署工具只关心"怎么构建、产物什么形态、怎么运行停止"。同一个 Vue 项目，SPA 是 `static`（无进程），Nuxt SSR 是 `node`（Node 进程）——同框架不同模型，证明 `type` 必须描述部署方式而非语言/框架。Spring Boot 应用打 Docker 跑则是 `docker`，不是 `jvm`——同代码不同部署方式，类型不同。

**原则二：配置两层分流，不假设所有模型字段一致。**
通用层（`name/type/repo/workspace/build`）真正做到所有模型通用；模型差异归入 `deploy:` 命名空间，由该模型的插件**拥有并解析**。`artifact`、`run`、`venv`、`image` 都不是通用字段，下沉为插件参数。

**原则三：命令按"能力"分流，不假设所有模型支持全部命令。**
`deploy/status/logs` 通用；`start/stop/restart` 是"具备独立进程"的模型才有的可选能力，`static` 不支持则优雅拒绝。

这三条是同构思想：**按部署模型的能力分流，而不是把 JVM 的假设硬塞给所有类型。**

---

## 3. 部署模型分类

| `type` | Build | 产物 | Stage（部署专属） | 进程模型 | 覆盖框架 |
|---|---|---|---|---|---|
| `jvm` | `mvn`/`gradle`（shell） | jar | 拷 jar 到部署目录（**可选**） | 长驻 PID | Spring Boot、Quarkus、任何 `java -jar` |
| `static` | `npm run build`（shell） | dist/build 静态文件 | 拷到 nginx 目录 + reload | **无每服务进程** | Vue/React/Angular/Svelte SPA、SSG |
| `node` | `npm run build`（shell） | server bundle | 拷 bundle 到 dest（可选，同 jvm） | 长驻 PID | Next.js SSR、Nuxt SSR、Express、NestJS |
| `python` | `uv sync`/`pip install`（shell） | 源码即产物 | migrate（可选） | 长驻 PID（ASGI/WSGI） | FastAPI、Flask、Django |
| `docker` | `docker build`（shell） | 镜像 | tag/push（可选） | 容器 | 任何容器化服务 |

`springboot` 作为 `jvm` 的**兼容别名**保留，旧配置无需改名。

---

## 4. 新架构

### 4.1 生命周期接口（通用契约 + 可选能力）

```go
// 通用契约：所有部署模型必须实现
type Deployer interface {
    Build(ctx context.Context, svc *config.ServiceConfig) error
    Stage(ctx context.Context, svc *config.ServiceConfig) error   // 部署专属准备（不随重启执行）
    Status(ctx context.Context, svc *config.ServiceConfig) (string, error)
    SetOutput(w io.Writer)
}

// 可选能力：只有"具备独立进程"的模型实现
type Startable interface { Start(ctx context.Context, svc *config.ServiceConfig) error }
type Stoppable  interface { Stop(ctx context.Context, svc *config.ServiceConfig) error }
```

**`Stage` 的语义是关键**：所有"只在 deploy 时做、restart 时跳过"的动作都归入 Stage。
- jvm：拷 jar（可选）
- static：拷 dist + `nginx -s reload`
- python：`migrate`
- node：`npm ci --omit=dev`（可选）
- docker：tag/push（可选，或 noop）

这正是缺陷 1 的解药：`mv` 归位属于 Stage，**绝不混入 Start**。`Start` 永远是纯启动，重启复用 Start 时产物已在位，安全。

**与旧接口的差异**：旧 `Deployer` = `Build/Start/Stop/Status/SetOutput`（Start 既被 deploy 用又被 restart 用，却允许它承担归位）。新接口拆出独立 `Stage`，且 `Start/Stop` 降为可选能力。此为**理念变更 #1**（见第 7 节）。

### 4.2 编排流程（统一）

```go
// Deploy：fetch ▸ Build ▸ Stage ▸ (Stop) ▸ (Start) ▸ notify
func Deploy(...) {
    fetch...                              // 保留
    deployer.Build(ctx, svc)
    deployer.Stage(ctx, svc)
    if s, ok := deployer.(Stoppable);  ok { _ = s.Stop(ctx, svc) }   // 有进程才停
    if s, ok := deployer.(Startable);  ok { _ = s.Start(ctx, svc) }  // 有进程才启
    sendNotify(...)                       // 保留
}

// Restart：(Stop) ▸ (Start)，跳过 Build/Stage
// Start：(Start)
// Stop：(Stop)
```

各模型落地：

| 模型 | Deploy 展开 |
|---|---|
| jvm | Build(mvn) ▸ Stage(拷jar) ▸ Stop(杀PID) ▸ Start(java -jar) |
| static | Build(npm build) ▸ Stage(拷dist + nginx reload) —— 无 Stop/Start |
| node | Build(npm build) ▸ Stage(拷 bundle 到 dest，可选) ▸ Stop(杀PID) ▸ Start(node server) |
| python | Build(uv sync) ▸ Stage(migrate) ▸ Stop(杀PID) ▸ Start(uvicorn) |
| docker | Build(docker build) ▸ Stage(tag) ▸ Stop(docker stop) ▸ Start(docker run) |

### 4.3 插件注册表（消灭 6 处 switch）

```go
// internal/registry/registry.go
type Factory func() deploy.Deployer
func Register(typeName string, f Factory)   // 各插件 init() 自注册
func Get(typeName string) (deploy.Deployer, error)
func Types() []string
```

CLI 与 webhook 统一 `registry.Get(svc.Type)`，现有 6 处 `switch svc.Type` 全部移除。新增类型 = 新增插件包 + `init()` 注册，**旧代码零改动**。`plugins/registry_init.go`（或 main）以副作用导入所有插件包。

### 4.4 可复用步骤原语（封装性）

各类型相同的动作抽成共享 helper，插件只做组合。放在 `internal/step`（或既有 `internal/build`）与 `internal/process`、新增 `internal/docker`：

| 原语 | 作用 | 替代/复用 |
|---|---|---|
| `step.Shell(dir, cmd, env, out)` | **经 `sh -c` 执行**，支持 `&&`/`\|`/`>`/`$VAR` | 替代 `ExecuteBuild`+`SplitCommand`（无 shell） |
| `step.CopyArtifact(glob, destDir)` | 按 glob 找构件拷贝 | 替代 `moveJarToRoot`（硬编码 target/） |
| `step.Clean(keep []string)` | 清理 workspace 保留指定模式 | 替代 `cleanWorkspace`（硬编码只留 jar） |
| `process.Manager`（PID） | PID 模型生命周期：Setpgid 后台启动 + SIGTERM 停止 + 存活探测 | 复用既有；去掉 springboot 里重复的 exec 逻辑 |
| `docker.Stop/Run/Status` | 容器模型生命周期 | 新增 |

`jvm`/`node`/`python` 三个 PID 模型共享 `process.Manager`；`docker` 用 `docker.*`；`static` 的 Stop/Start 退化为 reload/noop。封装性由此成立。

---

## 5. 配置设计（两层）

### 5.1 通用层（所有模型都有，放 `config.go`）

```yaml
- name: hello1
  type: jvm
  repo: { url: "git@gitee.com:.../xx.git", branch: main }
  workspace: /opt/app
  timeout: "45m"   # 可选：fetch+build+stage 总超时(默认 30m，所有模型通用)
  build: { command: "mvn clean package -Dmaven.test.skip=true" }   # 通用：每个模型都是一条 shell 命令
```

通用层仅：`name` / `type` / `repo` / `workspace` / `timeout`（可选，fetch+build+stage 总超时）/ `build`。`build.command` 通用（mvn/npm/uv/docker build 都是 shell 命令），**支持命令列表**（比 `&&` 更清晰，依次执行）：

```yaml
build: { command: ["npm ci", "npm run build"] }                       # 列表形式
build: { command: "mvn clean package -Dmaven.test.skip=true" }        # 或单条字符串
```

### 5.2 策略层（`deploy:`，schema 由 `type` 决定，插件自解析）

```yaml
# jvm —— artifact 可选：可原地启动，也可移动产物
- name: hello1
  type: jvm
  build: { command: "mvn clean package -Dmaven.test.skip=true" }
  deploy:
    artifact: "target/*.jar"          # 可选。不填=原地启动，不拷贝不清理
    dest: "/home/service/app1"        # 可选。填了才拷贝产物到该目录
    run: "java -jar hello-world-0.0.1.jar"   # 启动命令（纯启动，无 nohup/&）
    env: { JAVA_OPTS: "-Xms100m -Xmx128m" }

# static —— 无进程，Stage 含 nginx reload
- name: web-vue
  type: static
  build: { command: "npm run build" }
  deploy:
    artifact: "dist/*"
    dest: "/usr/share/nginx/html/vue"
    nginx_reload: true
    health: "https://app.example.com/health"   # status 通过健康检查 URL 判定

# node —— 长驻 Node 进程（源码即产物；可选 artifact/dest 归位，同 jvm）
- name: web-ssr
  type: node
  build: { command: "npm run build" }
  deploy:
    # artifact: ".output"            # 可选：产物目录/glob。不填=源码即产物(原地启动)
    # dest: "/opt/web-ssr-run"        # 可选：归位目录。填了才拷贝，run 也在该目录执行
    run: "node .output/server.mjs"   # 纯启动，不含 nohup/&/pm2
    env: { NODE_ENV: production }

# python —— 源码即产物，Stage 可含 migrate
- name: api
  type: python
  build: { command: "uv sync" }
  deploy:
    venv: ".venv"
    migrate: "uv run alembic upgrade head"   # 可选
    run: "uvicorn main:app --host 0.0.0.0 --port 8000"
    env: { DATABASE_URL: "..." }

# docker —— 容器生命周期
- name: svc-docker
  type: docker
  build: { command: "docker build -t app:latest ." }
  deploy:
    image: app:latest
    container: app
    ports: ["8000:8000"]
    env: [...]
```

**关于 `artifact`（针对用户关切）**：`artifact` 被 `jvm`/`static`/`node` 需要（3/5），不是通用字段，故下沉到 `deploy:` 而非顶层。对 `jvm`/`node` 而言它**也是可选的**——不填则原地启动（`run` 里写产物相对路径），填 `dest` 则 Stage 拷贝到该目录、`run` 在 dest 执行。`python`/`docker` 不需要 `artifact`（源码即产物 / 镜像即产物）。

**`run` 也不是通用字段**：`static`/`docker` 没有 `run`（static 无进程；docker 用 `image/container`）。故 `run` 同样在 `deploy:` 内，不顶层必填。这是**理念变更 #2**：旧设计把 `run.command` 当通用必填项，新设计按模型归属。

### 5.3 插件自解析配置

`ServiceConfig` 增加一个 `Deploy yaml.Node`（或 `map[string]any`）原始字段，由 `registry.Get(type)` 返回的插件各自解析为自己的结构体。**central `config.go` 不感知任何类型特定字段**，加新类型不改 `config.go`。封装性落地。

### 5.4 完整配置模板（带注释）

`config.yaml.example` 与向导（`deployd config`）输出均遵循此模板。向导按所选 `type` 引导填对应 `deploy` 字段（向导改造属 P1）。

```yaml
# deployd 全局配置
server:
  host: "0.0.0.0"          # 0.0.0.0=监听所有网卡(允许外部)；127.0.0.1=仅本机
  port: 9527
webhook:
  secret: ""               # GitHub/Gitee webhook 签名密钥（预留）
smtp:                      # SMTP 与 Resend 二选一，都不配则不发通知
  host: "smtp.qq.com"
  port: 465
  username: "your-email@qq.com"
  token: "your-smtp-authorization-code"   # 授权码，非登录密码
  tls: true
resend:
  api_key: "re_xxxxxxxxxxxxxxxxxxxxx"     # https://resend.com/api-keys
  from: "deployd <onboarding@your-domain.com>"
notifications:
  to:                      # 额外收件人（提交作者始终默认收件）
    - "admin@example.com"

# 服务列表：每个服务按"部署模型"(type)配置，type 决定 deploy: 的结构。
# 通用字段：name / type / repo / workspace / build
# 策略字段：deploy:（结构由 type 决定，插件自解析）
services:
  # ── jvm：Spring Boot 等 java -jar 服务 ──
  - name: "my-springboot-app"
    type: "jvm"                      # 兼容别名 springboot
    repo:
      url: "https://github.com/user/repo.git"   # HTTPS 自动转 SSH
      branch: "main"
    workspace: "/opt/deployd/apps/my-springboot-app"
    build:
      command: "mvn package -DskipTests"        # 通用：构建命令(shell)，也支持列表 ["npm ci","npm run build"]
    deploy:                         # jvm 策略层
      # artifact: "target/*.jar"   # 可选：产物 glob。不填=原地启动(run 写 target/xxx.jar)
      # dest: "/opt/app"           # 可选：归位目录。填了才拷贝(替代旧 moveJarToRoot)
      run: "java -jar target/app.jar"           # 纯启动，不要写 nohup/&(后台化由工具负责)
      # env: { JAVA_OPTS: "-Xms100m -Xmx128m" } # 可选：运行时环境变量

  # ── static：Vue/React 等静态站点(无独立进程,nginx 托管) ──
  # - name: "web-vue"
  #   type: "static"
  #   build: { command: "npm run build" }
  #   deploy:
  #     artifact: "dist/*"                       # 构建产物
  #     dest: "/usr/share/nginx/html/vue"        # 拷到 nginx 目录
  #     nginx_reload: true                       # 部署后 reload nginx
  #     health: "https://app.example.com/health" # status 用健康检查 URL 判定

  # ── python：FastAPI/Flask 等 ASGI/WSGI 服务 ──
  # - name: "api"
  #   type: "python"
  #   build:
  #     command: ["python3.11 -m venv .venv", ".venv/bin/pip install ."]
  #   deploy:
  #     venv: ".venv"
  #     migrate: ".venv/bin/alembic upgrade head"  # 可选：迁移(部署专属,不随重启)
  #     run: ".venv/bin/uvicorn main:app --host 0.0.0.0 --port 8000"

  # ── docker：容器化服务 ──
  # - name: "svc-docker"
  #   type: "docker"
  #   build: { command: "docker build -t app:latest ." }
  #   deploy:
  #     image: "app:latest"
  #     container: "app"
  #     ports: ["8000:8000"]
```

---

## 6. 命令设计

### 6.1 通用 vs 能力门控

| 命令 | 语义 | jvm | static | node | python | docker |
|---|---|---|---|---|---|---|
| `deploy <name>` | 构建+归位+激活 | ✅ | ✅ | ✅ | ✅ | ✅ |
| `status [name]` | 查状态 | ✅ | ✅ | ✅ | ✅ | ✅ |
| `logs <name>` | 部署日志 | ✅ | ✅ | ✅ | ✅ | ✅ |
| `start <name>` | 启动进程 | ✅ | ❌ | ✅ | ✅ | ✅ |
| `stop <name>` | 停止进程 | ✅ | ❌ | ✅ | ✅ | ✅ |
| `restart <name>` | 重启 | ✅ | ❌ | ✅ | ✅ | ✅ |

`start/stop/restart` 执行前类型断言：插件未实现 `Startable`/`Stoppable` 则返回明确错误，如"`static` 类型无独立进程，不支持 stop，请用 `deploy` 重新发布"。这是**理念变更 #3**：旧设计假设所有类型都能 start/stop。

`status` 对 `static` 的语义：通过 `deploy.health` 配置的**健康检查 URL** 判定（HTTP 2xx/3xx = running，否则 stopped）。`health` 字段对其他类型可选（配了就用健康检查替代 PID/docker-ps 探测，更准确反映"应用就绪"而非"进程存活"）。

### 6.2 缩写（短旗标）

约束：顶层 `start/stop/restart` 是守护进程命令（已上线，不破坏），服务级操作用**短旗标**置于 `svc` 命名空间下，避开冲突。

| 操作 | 命令 | 旗标 |
|---|---|---|
| 全量发布 | `deployd deploy <name>`（别名 `dep`） | — |
| 启动服务 | `deployd svc <name> -s` | `-s` start |
| 停止服务 | `deployd svc <name> -t` | `-t` terminate(stop) |
| 重启服务 | `deployd svc <name> -r` | `-r` restart |
| 查看状态 | `deployd svc <name>` | 无旗标（默认 status） |

> `-t` 在 `logs` 命令中表示 follow，在 `svc` 下表示 stop；命令不同、上下文区分，可接受。守护进程命令（`deployd start/stop/restart/status`）维持现状不动。

---

## 7. 设计理念变更（与旧设计的不同，显式声明）

| # | 变更点 | 旧设计 | 新设计 | 影响与处置 |
|---|---|---|---|---|
| 1 | 生命周期阶段 | `Build/Start/Stop/Status`，Start 兼归位+启动 | `Build/Stage/Status` + 可选 `Start/Stop`，Stage 独立 | 接口变更；修复 restart 必坏的核心 bug |
| 2 | `run.command` 定位 | 顶层通用必填，承担归位+启动 | 下沉到 `deploy:`，纯启动，不归位 | 旧配置迁移见第 9 节 |
| 3 | 命令执行方式 | `SplitCommand`+`exec.Command`（无 shell） | `sh -c` | shell 语义生效；旧"碰巧能用"的 `>` `&` 行为变化见第 9 节 |
| 4 | 后台化归属 | 用户在 `run.command` 写 `nohup ... &`（破坏 PID） | 插件经 `process.Manager`（Setpgid）后台化，记录真实 PID | `run.command` 不应再含 `nohup/&`；见第 9 节 |
| 5 | 产物归位 | 插件 `moveJarToRoot` 硬编码 target/→workspace根 | 可选 `Stage` 步骤，`dest` 可配；jvm 可原地启动 | jvm 行为可选化 |
| 6 | `type` 语义 | 偏框架（springboot） | 部署模型（jvm/static/node/python/docker） | `springboot` 作别名保留 |
| 7 | 插件分发 | 6 处 `switch svc.Type` | 注册表 `registry.Get` | 新增类型零改旧代码 |
| 8 | `start/stop` 通用性 | 假设所有类型支持 | 能力门控，static 不支持 | static 优雅拒绝 |
| 9 | 配置结构 | 扁平 `build.command`/`run.command` 通用必填 | 通用层 + `deploy:` 策略层 | 见第 9 节迁移 |

---

## 8. 既有功能兼容性矩阵（逐项）

| 既有功能 | 现状位置 | 新设计中 | 处置 |
|---|---|---|---|
| 同服务部署合并队列 | `deployqueue/scheduler.go` | 队列→锁→Deploy，**位于 orchestrator 之上** | **完全保留**。队列只调 `Deploy`，不感知内部阶段；合并/丢弃/收件人收集逻辑不变 |
| per-service flock 部署锁 | `deploylock/lock.go` | 锁包住整个 Deploy | **完全保留**。try-lock(手动)/阻塞(webhook)/fd 继承(fork) 不变 |
| 日志方案A（服务日志=流水线，应用 stdout→/dev/null） | `springboot/plugin.go` Start | 各 PID 插件 Start 均 `cmd.Stdout=nil` | **保留并泛化**。static 无进程自然成立；docker `run -d` 日志交 docker |
| 服务级日志隔离 | `logger/logger.go` | `logger.GetServiceLogger(name)` | **完全保留**。任何 `ServiceConfig.Name` 自动隔离 |
| daemon 自身日志 | `~/.deployd/deployd.log` | 不变 | **保留** |
| SMTP 邮件通知 | `notify/email.go` | `sendNotify` 在 Deploy 末尾调用 | **完全保留** |
| Resend 邮件通知 | `notify/email.go` | 同上 | **完全保留** |
| 通知收件人合并 | orchestrator `buildNotifier` | operators(合并去重)+`notifications.to`，跳过空，手动回退 authorEmail | **完全保留** |
| 邮件只发一次（成功/失败） | orchestrator | Deploy 末尾一次 | **保留**；`service restart` 维持现状不发通知（既有行为） |
| webhook GitHub/Gitee 解析 | `webhook/server.go` | 不变 | **完全保留** |
| 分支匹配 + URL HTTPS→SSH 规范化 | `webhook/server.go` `MatchService` | 不变 | **完全保留** |
| webhook secret（预留） | `webhook/server.go` | 不变 | **保留** |
| Jenkins 风格 git fetch（clean state） | `build/git.go` `Fetch` | 仍在 Deploy 起首 | **保留 + 优化**：`fetch+reset --hard` 失败回退 clean（已纳入 P1，见第 12 节） |
| SSH 密钥自动生成 + 引导 | `build/ssh.go` | 不变 | **完全保留** |
| Java 版本检测（.java-version + jenv/java_home） | springboot 插件 | 迁入 `jvm` 插件 | **保留**，归属 jvm |
| Linux fork 后台 + setsid | `cmd/deploy.go` `forkDeploy` | 不变 | **完全保留** |
| macOS 前台 | `cmd/deploy.go` | 不变 | **保留** |
| `--no-fork` / `--locked` 标志 | `cmd/deploy.go` | 不变 | **完全保留** |
| 配置加载优先级（cwd > ~/.deployd） | `config.DefaultConfig` | 不变 | **完全保留** |
| daemon 配置缓存 | `daemon.SetConfigPath` | 不变 | **保留** |
| 交互式配置向导 | `config/wizard.go` | 增加按模型引导（选模型→填对应字段） | **保留并扩展**（向导可问框架填默认值，但 type 仍是模型） |
| PID 进程管理 | `process/manager.go` | 泛化为 PID 模型插件共享 helper | **保留**，去掉 springboot 内重复 exec |
| `logs -n/-t` | `cmd/logs.go` | 不变 | **完全保留** |
| 构建后清理工作区 | `cleanWorkspace` | jvm 的 `step.Clean(keep)`，可配 | **保留为可选**（jvm 原地启动时不清理） |

**结论：零功能删减。** 队列/锁/日志/通知/webhook/fork 等外层全部原样保留；重构只把最内层的阶段、接口、配置、命令按部署模型重排。

---

## 9. 配置迁移与行为变化

### 9.1 不保留旧配置兼容（单一新格式）

经复杂度/稳定性/可用性权衡，**不保留遗留模式**，统一使用第 5 节的新配置格式。理由：

| 维度 | 保留遗留模式 | 去掉遗留模式 |
|---|---|---|
| 复杂度 | jvm 双路径、配置探测 `deploy:`、混合字段优先级歧义、双倍测试 | 单一路径，配置统一，代码/测试/文档减半 |
| 稳定性 | 双路径更多 bug 面；遗留模式要么保留无 shell 缺陷、要么改 `sh -c` 致旧 `nohup/&` 立即坏，两难；混合字段优先级未定义 | 单一明确行为，无歧义 |
| 可用性 | 老用户零迁移；但需文档解释两套配置、何时用哪个 | 一套配置，文档清晰；老用户一次性迁移（本项目服务少，成本低） |

结论：去掉。唯一代价是旧 `config.yaml` 一次性迁移，远小于长期维护双路径的负担。配套兜底：启动时若检测到顶层旧字段（`run.command`/`build.command`），打印迁移提示警告（不自动转换）。

### 9.2 迁移映射（旧 -> 新）

| 旧字段 | 新字段 | 说明 |
|---|---|---|
| `type: springboot` | `type: jvm` | springboot 作兼容别名仍接受 |
| `build.command`（顶层） | `build.command`（通用层） | 位置不变；新增支持列表形式 |
| `run.command`（顶层） | `deploy.run`（策略层） | 下沉到 deploy；纯启动，去掉 nohup/& |
| 隐式 moveJarToRoot+clean | `deploy.artifact` + `deploy.dest` | 显式归位；或原地启动（不填 artifact，run 写 target/xxx.jar） |

迁移示例：

```yaml
# 旧
- name: hello1
  type: springboot
  build: { command: "mvn package -DskipTests" }
  run: { command: "nohup java -jar hello-world-0.0.1.jar > /dev/null 2>&1 &" }

# 新（原地启动）
- name: hello1
  type: jvm
  build: { command: "mvn package -DskipTests" }
  deploy:
    run: "java -jar target/hello-world-0.0.1.jar"

# 新（归位到指定目录）
- name: hello1
  type: jvm
  build: { command: "mvn package -DskipTests" }
  deploy:
    artifact: "target/*.jar"
    dest: "/home/service/app1"
    run: "java -jar hello-world-0.0.1.jar"
```

### 9.3 需知晓的行为变化

| 变化 | 说明 |
|---|---|
| 命令改经 `sh -c` | `&&`/`\|`/`>`/`$VAR` 等 shell 语义生效；旧"碰巧能用"的 `>` `&` 字面参数行为不再成立 |
| `run` 不含 nohup/& | 后台化由工具 `process.Manager`(Setpgid) 负责，PID 正确；`run` 写纯启动命令 |
| `run` 不再归位 | 归位归 Stage（`artifact`+`dest`）；restart 复用纯启动 Start，产物已在位，不再坏 |

---

## 10. 分阶段落地

| 阶段 | 内容 | 验证 |
|---|---|---|
| P1 | `Stage` 接口 + 注册表 + `sh -c` 执行 + 旧字段检测警告 + **fetch 优化** | 新配置可用；restart 不再报 mv 失败；fetch 省去重复 init/remote add |
| P2 | 重构 `jvm` 插件：`artifact` 可选 + `process.Manager` 统一启动（去 nohup/&，PID 正确） | `go test ./...`；stop 能杀到真实进程 |
| P3 | `docker` 插件 | `docker build/run/stop/ps` 全流程 |
| P4 | `static`、`node` 插件 | vue 构建→nginx 部署；node SSR |
| P5 | `python` 插件（含 `ai_qa_assistant` 具体映射，见第 13 节） | uv/venv/uvicorn/migrate 全流程 |
| P6 | 命令缩写（`svc`/`dep`/`up`/`down`） + 向导按模型引导 | 缩写可用；static 的 start/stop 优雅拒绝 |

每阶段独立可验证、可回滚，且不破坏外层（队列/锁/日志/通知）。

---

## 11. 权衡

| 决策 | 选择 | 代价 | 理由 |
|---|---|---|---|
| 插件制 vs 全声明式步骤引擎 | 插件制 + `sh -c` 逃生舱 | 新类型写少量 Go | 全引擎对本体量过度设计；插件类型安全可测，shell 步骤覆盖任意复杂命令 |
| 命令经 `sh -c` | 是 | 配置不可信时有注入面 | 配置由运维掌控、非外部输入，可接受；换来 shell 语义完整 |
| 独立 `Stage` 阶段 | 加接口方法 | 接口+mock+测试要改 | 修复 restart 必坏的最小正确解，语义清晰 |
| 遗留模式兼容 | 否（单一新格式） | 旧配置需一次性迁移 | 复杂度↓稳定性↑可用性↑；服务少迁移成本低（见第 9 节） |
| Jenkins 风格 fetch | 保留 + 优化 | 优化路径失败时仍有回退开销 | 健壮抗 force-push；`fetch+reset --hard` 失败回退 clean |
| `artifact` 下沉且可选 | 是 | jvm 有三种用法 | 诚实反映 2/5 需求；用户可原地启动或移动 |

---

## 12. 性能

- **合并队列**：in-memory channel，每服务一 goroutine，无新 IO，保留。
- **flock**：内核态微秒级，仅部署边界获取/释放，保留。
- **fetch 优化（已纳入 P1）**：现状每次 `rm .git; init; remote add; fetch; checkout`。优化为先 `git fetch + reset --hard`，失败再回退 clean，省去重复 init/remote add。
- **Stage/Build 输出**：流式写服务日志，无额外开销。
- 无引入新进程或常驻组件。

---

## 13. Python 类型具体映射（参考项目 `ai_qa_assistant`）

**调研结论**：`ai_qa_assistant` 是 **FastAPI + uvicorn**（ASGI）；依赖管理 **pyproject.toml（PEP 621，无 lock 文件，`pip install .`）**；venv 为 **`.venv`**（Python 3.11+，Dockerfile 用 3.11-slim）；迁移 **alembic**（`alembic upgrade head`，非自动，需独立执行）；可选 **arq worker** 副进程；外部依赖 **PostgreSQL(pgvector)+Redis+LLM/Embedding API**；配置走 **`.env`**（pydantic-settings）；**无前端构建、无 collectstatic**（仅一个静态 HTML 挂在 `/ui`）。

### 13.1 完整配置示例

```yaml
- name: ai-qa
  type: python
  repo: { url: "git@.../ai_qa_assistant.git", branch: main }
  workspace: /opt/ai_qa_assistant
  build:
    command:
      - "python3.11 -m venv .venv"            # 创建 venv（跨部署保留 .venv 可加速，见 13.3）
      - ".venv/bin/pip install ."             # 装依赖（pyproject.toml，无 lock）
  deploy:
    venv: ".venv"
    migrate: ".venv/bin/alembic upgrade head"  # Stage：部署专属，不随重启执行
    run: ".venv/bin/uvicorn app.main:create_app --factory --host 0.0.0.0 --port 8000"
    # health: "http://localhost:8000/health"   # 可选：健康检查 URL 替代 PID 探测
    env:
      ARQ_WORKER_MODE: inline                  # inline=单进程（web 内嵌 worker）
```

### 13.2 阶段映射

| 阶段 | 命令 | 说明 |
|---|---|---|
| Build | `python3.11 -m venv .venv` + `.venv/bin/pip install .` | 装依赖到 venv；venv 是部署单元的一部分 |
| Stage | `.venv/bin/alembic upgrade head`（可选） | 迁移，部署专属；需 `.env` 的 `DATABASE_URL` 指向就绪的 pg |
| Start | `.venv/bin/uvicorn app.main:create_app --factory --host 0.0.0.0 --port 8000` | 纯启动，PID 管理，不含 nohup/& |
| Stop | 杀 PID | `process.Manager` |
| Status | PID 探测（或 `health` URL） | 默认 PID |

### 13.3 实现要点与注意事项

1. **`.env` / 密钥不来自 git**：项目用 `.env` 读 `DATABASE_URL`/`REDIS_URL`/`LLM_API_KEY`/`EMBEDDING_API_KEY`/`ADMIN_API_KEY` 等，`.env` 不入库。部署前需由运维在 workspace 放好 `.env`（或工具支持引用 workspace 外的 env 文件）。`deploy.env` 仅补充非敏感变量，密钥仍走 `.env`。
2. **venv 跨部署保留（性能）**：Jenkins 风格 fetch 不覆盖非 git 文件，python 插件不清理 `.venv`，使 `pip install .` 增量加速；首部署自动创建。注意 `ADMIN_API_KEY` 为默认值时应用 fail-fast 拒启，属配置问题非部署问题。
3. **多进程（arq worker）- 已知限制**：`ARQ_WORKER_MODE=process` 时需 web + worker 两个进程。当前单 PID 模型只覆盖 inline（单进程）。多进程作为后续项（可让 worker 作为独立 service，或扩展进程组管理）；默认 inline 规避。
4. **外部依赖就绪**：PostgreSQL(pgvector)+Redis 须先就绪，否则 Start 后应用 fail-fast。部署工具不负责拉起外部服务，由运维/compose 保证。
5. **与 Spring Boot(jar) 的本质区别**：① 依赖不打包进产物，venv 是部署单元需单独管理；② 构建≈拷代码+装依赖，无编译、无单一可分发二进制；③ 迁移显式分离（alembic 不自动）；④ 可能多进程；⑤ 运行时绑定解释器版本（3.11+）且 venv 与之一一绑定。

---

## 14. 开放项决议（2026-08-13）

1. **`service restart` 不通知** — 维持现状（restart 仅 Stop+Start，不发邮件）；只有 `deploy` 才发通知。
2. **fetch 优化纳入 P1** — `git fetch + reset --hard`，失败回退 clean（见第 10、12 节）。
3. **命令缩写用短旗标** — 服务操作 `-s`(start)`-t`(stop/terminate)`-r`(restart)，置于 `svc` 命名空间下（见 6.2）。
4. **`build.command` 升级为命令列表** — 支持列表形式，比 `&&` 清晰（见 5.1）。
5. **`static` 的 status 用健康检查 URL** — `deploy.health` 配置 URL，HTTP 2xx/3xx = running（见 6.1）。`health` 对其他类型可选。

## 15. 落地后修订（2026-08-22）

重设计在 Ubuntu 22 服务器实测后的问题审计与第二轮修复（多服务 webhook 匹配、产物目录递归拷贝、
日志时间戳、daemon 启动反馈、配置路径统一、fetch/build 超时、清理只保留本次产物、Linux JAVA_HOME
探测），见 [2026-08-22-issue-audit-and-fixes.md](2026-08-22-issue-audit-and-fixes.md)。
本文档未涉及的行为以该审计文档为准。

### 15.1 2026-08-31 再次修订

1. **`timeout` 重设计为服务级总预算**：原 `build.timeout`（在 `build:` 内、三阶段各自独立计时）
   改为服务层 `timeout`（与 `build:` 同级），fetch+build+stage **共享一个总预算**（Deploy 起首单个
   `context.WithTimeout`）。通用层（§5.1）新增 `timeout` 字段。见审计文档 D8/B6/B13。
2. **`node` 增加 `artifact`/`dest`**：node 不再仅"源码即产物"，可选归位（同 jvm：拷 `.output` 到
   dest、`run` 在 dest 执行，与下次构建互不干扰；不配则原地启动）。见审计文档 D5/B14，及上方 §3/§4.2/§5.2 的 node 行。
