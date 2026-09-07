# deployd

自动化部署守护进程 - 一个在后台运行的 CLI 工具，接收来自 GitHub / Gitee 的 Webhook 事件，自动完成服务的构建、归位与重启。

## 功能特性

- **五种部署模型** - `jvm`（Spring Boot 等 jar 服务）、`docker`（容器）、`static`（Vue/React 静态站点，nginx 托管）、`node`（SSR/Express 常驻进程）、`python`（FastAPI/Flask 等 ASGI/WSGI 服务）
- **后台守护进程** - 在系统后台运行，不受终端关闭影响
- **Webhook 服务器** - 监听 GitHub / Gitee 推送事件，按仓库 URL + 分支匹配对应服务
- **同服务部署合并队列** - 连续推送自动合并为一次部署（只部署最新提交），flock 文件锁跨进程串行化，手动/自动部署互不冲突
- **部署生命周期分阶段** - 构建（Build）→ 归位（Stage，仅部署时执行）→ 停旧 → 启新（纯启动），重启不重复归位/迁移
- **命令经 shell 执行** - `&&`、`|`、`>`、`$VAR` 等语义完整支持；build 支持命令列表
- **git 快路径** - 已有仓库直接 `fetch + reset --hard`，失败自动回退全量克隆
- **邮件通知** - 通过 SMTP 或 Resend API 在部署成功/失败时发送 HTML 邮件（含失败阶段）
- **插件注册表** - 新增服务类型只需实现 Deployer 接口并自注册，无需改动调度代码
- **交互式配置向导** - `deployd config` 按部署模型引导配置
- **发布自动化** - GitHub Actions 自动构建 macOS / Linux 二进制

## 安装

### 安装脚本（推荐）

```bash
curl -sSL https://raw.githubusercontent.com/Zq-wabc2020/auto-deployer/main/install.sh | sh
```

### 从源码编译

```bash
go build -o deployd .
sudo mv deployd /usr/local/bin/
```

### 交叉编译（本地打 Linux 包）

在 Mac 或其他平台直接编译 Linux 可执行文件，无需安装交叉工具链：

```bash
# Linux x86_64（最常见的阿里云 ECS 机型）
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o deployd-linux-amd64 .

# Linux ARM64（阿里云倚天实例等）
GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -o deployd-linux-arm64 .
```

将生成的二进制文件上传到服务器部署：

```bash
scp deployd-linux-amd64 user@your-server:/usr/local/bin/deployd
chmod +x /usr/local/bin/deployd
```

### 从 Release 下载

在 [Releases](https://github.com/Zq-wabc2020/auto-deployer/releases) 页面下载最新二进制文件并放到 PATH 中即可。

## 使用方法

### 快速开始

```bash
# 1. 运行交互式配置向导（按部署模型引导）
deployd config

# 2. 启动守护进程
deployd start

# 3. 查看状态（守护进程 + 所有服务）
deployd status

# 4. 手动触发完整部署（拉取 -> 构建 -> 归位 -> 重启 -> 就绪 -> 通知）
deployd deploy <服务名>          # 可用缩写 deployd dep <服务名>

# 5. 取消进行中的部署/启动（卡在启动中时释放资源）
deployd cancel <服务名>

# 6. 服务生命周期（不重新构建）
deployd svc <服务名>             # 查看服务状态
deployd svc <服务名> -s          # 启动（后台执行，用 deployd status 查进度）
deployd svc <服务名> -t          # 停止
deployd svc <服务名> -r          # 重启（后台执行，用 deployd status 查进度）

# 7. 查看日志
deployd logs              # 守护进程日志
deployd logs <服务名>     # 服务日志
deployd logs <服务名> -f  # 实时跟踪

# 8. 停止守护进程
deployd stop
```

### 命令说明

#### 守护进程命令

| 命令 | 描述 |
|------|------|
| `deployd start [-c 路径]` | 启动守护进程 |
| `deployd stop` | 停止守护进程 |
| `deployd restart [-c 路径]` | 重启守护进程 |
| `deployd status` | 显示守护进程及所有服务状态 |
| `deployd logs [服务名] [-f]` | 查看日志，加 `-f` 实时跟踪 |
| `deployd deploy <名称> [-c 路径]` | 手动触发指定服务的完整部署流程（别名 `dep`） |
| `deployd cancel <名称>` | 取消该服务进行中的部署/启动（`deploy`/webhook/`svc -s`/`svc -r` 均可取消） |
| `deployd config` | 交互式配置向导 |

#### 服务生命周期命令

| 命令 | 描述 |
|------|------|
| `deployd svc <名称> -s` | 启动服务（不重新构建，**后台执行**） |
| `deployd svc <名称> -t` | 停止服务 |
| `deployd svc <名称> -r` | 重启服务（不重新构建，不重复归位/迁移，**后台执行**） |
| `deployd svc <名称>` | 查看服务状态 |
| `deployd service start/stop/restart <名称>` | 长形式，等价于上面的短旗标 |

> 服务生命周期命令只做进程操作，不触发构建；完整流程用 `deploy`。
> **static 类型没有独立进程**，`-s/-t/-r` 会被明确拒绝，重新发布请用 `deploy`。
> `-s`/`-r` **非阻塞**：命令立即返回，启动在后台进行（期间状态为"启动中"，health 通过才转"运行"）。用 `deployd status` 或 `deployd svc <名称>` 查进度；卡住可用 `deployd cancel <名称>` 取消。

#### 服务状态与颜色

`deployd status` / `deployd svc <名称>` 输出的服务状态共五种（终端里带颜色，重定向到文件时为纯文本）：

| 状态 | 颜色 | 含义 |
|------|------|------|
| `starting`（启动中） | 蓝色 | 部署/启动/重启正在就绪门控阶段（health 轮询中） |
| `running`（运行中） | 绿色 | health 探测通过（或进程存活） |
| `stopped`（已停止） | 灰色 | 进程/容器不存在，或 health 探测失败 |
| `start_failed`（启动失败） | 红色 | 上次启动未通过就绪门控（超时/进程早退），**粘性**：直到下次 deploy/start/restart 成功才转绿 |
| `unknown` | 暗黄色 | 探测异常（配置缺失、命令失败等） |

> 同一服务同一时刻只允许一个部署/启动操作（文件锁）；`starting` 期间再发 deploy/svc start 会被拒绝，避免并发写状态。
> `svc -t` 停止成功后状态回到 `stopped`（清除 start_failed）。

#### 配置文件优先级

`-c` 标志 > 当前目录 `config.yaml` > `~/.deployd/config.yaml` > daemon 上次启动用的配置（记录在 `~/.deployd/config.path`，每次 `deployd start` 覆盖写）> `~/config.yaml`（旧默认，兼容保留）

> daemon 与所有 CLI 命令共用同一套解析顺序，不会出现"daemon 读 A、status 读 B"。只要用 `deployd start` 启动过，在任何目录执行 `status`/`logs` 都能找到配置。

## Webhook URL 配置

服务启动后，Webhook 监听地址为：

```
http://<服务器IP>:<端口>/webhook
```

默认端口 `9527`，`server.host` 默认 `0.0.0.0`（监听所有网卡）。

**GitHub 配置步骤：**
1. 仓库 -> Settings -> Webhooks -> Add webhook
2. Payload URL 填入 `http://<你的服务器IP>:9527/webhook`
3. Content type 选择 `application/json`
4. Secret 可填（当前未启用签名验证，可留空）
5. 选择 "Just the push event"
6. 点击 Add webhook

**Gitee 配置步骤：**
1. 仓库 -> 管理 -> WebHooks -> 添加 WebHook
2. URL 填入 `http://<你的服务器IP>:9527/webhook`
3. 选择触发事件：Push 事件
4. 点击确认

> **注意：** 服务器需要有公网 IP 或可通过内网穿透暴露该端口，否则 GitHub/Gitee 无法回调。

## 配置说明

配置分两层：**通用层**（所有模型一致：`name/type/repo/workspace/build`）和**策略层**（`deploy:`，结构由 `type` 决定）。命令均经 `sh -c` 执行。

```bash
cp config.yaml.example config.yaml   # 模板内含全部五种模型的带注释示例
```

### 通用配置项

| 配置路径 | 说明 | 示例 |
|----------|------|------|
| `server.host` / `server.port` | 监听地址 / 端口 | `"0.0.0.0"` / `9527` |
| `smtp.*` / `resend.*` | 邮件通知（二选一，详见下方） | |
| `notifications.to` | 部署通知收件人列表（提交作者始终默认收件） | `["admin@example.com"]` |
| `services[].name` | 服务名称，用于日志和管理命令 | `"my-app"` |
| `services[].type` | 部署模型：`jvm` / `docker` / `static` / `node` / `python`（`springboot` 为 `jvm` 别名） | `"jvm"` |
| `services[].repo.url` / `.branch` | Git 仓库地址（HTTPS 自动转 SSH）/ 分支 | `"main"` |
| `services[].workspace` | 代码克隆和工作目录 | `"/opt/deployd/apps/my-app"` |
| `services[].build.command` | 构建命令，支持单条字符串或命令列表 | `"mvn package -DskipTests"` |
| `services[].timeout` | fetch + build + stage + **就绪等待**的**总超时**（全流程共享一个计时，非每阶段各 30m；Go duration 语法），默认 `30m` | `"45m"` |
| `services[].deploy.health` | **所有模型必填**：HTTP 健康检查 URL。既是部署成功的就绪判定（轮询直到 2xx/3xx），也是 `status` 的实时探测依据 | `"http://localhost:8080/health"` |
| `services[].deploy.health_interval` | 就绪轮询间隔，默认 `10s`（Go duration 语法）。无需单独的健康检查超时--就绪等待共享 `timeout` 总预算 | `"5s"` |

### 各模型 `deploy:` 策略配置

**jvm**（Spring Boot 等 `java -jar` 服务）

```yaml
deploy:
  # artifact: "target/*.jar"   # 可选：产物 glob 或列表。不填=原地启动(run 写 target/xxx.jar)
  # dest: "/opt/app"           # 可选：归位目录。填了才拷贝；run 也会在该目录下执行
  run: "java -jar hello-world-0.0.1.jar"   # 纯启动，不要写 nohup/&（后台化由工具负责）
  health: "http://localhost:8080/health"   # 必填：就绪判定与 status 探测共用
  # health_interval: "10s"                 # 可选：就绪轮询间隔(默认 10s)
  env: { JAVA_OPTS: "-Xms100m" }           # 可选：运行时环境变量
```

> workspace 里有 `.java-version` 时自动选用对应 JDK（探测顺序：jenv -> macOS java_home -> `/usr/lib/jvm/*` 扫描，Linux 无 jenv 也可用）。

**static**（Vue/React SPA、SSG，无独立进程）

```yaml
deploy:
  artifact: "dist"                         # 构建产物（目录或 glob，见下）
  dest: "/usr/share/nginx/html/app"        # 拷贝到 nginx 目录
  nginx_reload: true                       # 拷贝后执行 nginx -s reload
  health: "https://app.example.com/health" # 必填：就绪判定与 status 探测共用
  # health_interval: "10s"                 # 可选：就绪轮询间隔(默认 10s)
```

**artifact 语义**（jvm/static/node 通用，嵌套目录会递归拷贝）：

| 写法 | 行为 |
|------|------|
| `dist` / `dist/` | 目录**内容**整体拷贝到 dest 根（static 前端推荐） |
| `dist/*` | 每个匹配项拷到 dest 根：文件平铺，子目录保持 `dest/<目录名>/` |
| `target/*.jar` | 文件 glob（jvm 常规用法），自动跳过 `*.original.jar` |
| `["hello1.txt", "test.json"]` | **列表写法**：多个具体文件/多个 glob，各项独立解析后统一平铺到 dest 根 |

> 不支持 `**`（Go 标准库无 doublestar 语义）。

**node**（Next.js SSR / Express 等常驻进程；源码即产物，也可归位）

```yaml
deploy:
  run: "node server.js"            # 源码即产物(默认)：不配 artifact，在 workspace 启动
  health: "http://localhost:3000/health"   # 必填：就绪判定与 status 探测共用
  env: { NODE_ENV: "production" }
  # 归位模式(可选)：把构建产物拷到 dest，run 在 dest 执行，与下次构建互不干扰
  # artifact: ".output"          # 产物目录/glob(如 Nuxt 的 .output)，语义同 jvm/static
  # dest: "/opt/app-run"           # 填了才拷贝；workspace 不清理，保留 node_modules 做增量构建
```

**python**（FastAPI/Flask/Django，源码即产物）

```yaml
build:
  command: ["python3.11 -m venv .venv", ".venv/bin/pip install ."]
deploy:
  venv: ".venv"                              # 可选：其 bin 自动前置 PATH
  migrate: ".venv/bin/alembic upgrade head"  # 可选：迁移(部署专属,重启不执行)
  run: ".venv/bin/uvicorn main:app --host 0.0.0.0 --port 8000"
  health: "http://localhost:8000/health"    # 必填：就绪判定与 status 探测共用
  env: { DATABASE_URL: "..." }
```

**docker**（容器）

```yaml
deploy:
  image: "app:latest"
  container: "app"              # 默认取服务名
  ports: ["8000:8000"]
  env: { FOO: "bar" }
  volumes: ["/data:/data"]
  # args: ["--memory=512m"]     # 额外 docker run 参数
  health: "http://localhost:8000/health"    # 必填：就绪判定与 status 探测共用
```

### 命令执行注意事项

- **命令经 `sh -c` 执行**，`&&`、`||`、`|`、`>`、`$VAR` 等 shell 语义均可使用
- **命令环境继承自 daemon 启动者**（非登录非交互 shell，不加载 `~/.bash_profile`）：谁启动 `deployd`，构建环境就是谁的。需要自定义函数/别名时在命令列表里显式加载：`[". ~/.bash_profile", "your_func ..."]`（用 `.` 而非 `source`，后者在 Ubuntu 的 dash 下不可用）
- **fetch/build/stage/就绪等待有超时保护**：`timeout` 是全流程**总超时**（共享一个计时，默认 30m，非每阶段各 30m），任一阶段超时即杀整个进程组（不会留下 mvn 孤儿），部署失败会发邮件
- **部署成功以就绪为准**：启动命令执行后还要轮询 `deploy.health`（间隔 `health_interval`，默认 10s）直到返回 2xx/3xx 才算部署成功、才发成功邮件；就绪等待计入 `timeout` 总预算，超时未就绪则记 `start_failed` 并发失败邮件
- **build** 支持三种写法；在 `workspace` 下执行：

```yaml
build:
  # 写法1：命令列表（推荐，任一步失败即停）
  command: ["mvn clean package -Dmaven.test.skip=true", "cp README.md target/"]

  # 写法2：多行字符串（每行都执行，但某行失败不会中断后续行）
  # command: |
  #   echo "step 1"
  #   mvn clean package -Dmaven.test.skip=true

  # 写法3：单行 && 连接（等价写法1的语义）
  # command: "mvn clean package -Dmaven.test.skip=true && cp README.md target/"
```

- **run 是纯启动命令**：不要写 `nohup`/`&`/重定向，**也不要用 pm2/supervisor 等自守护工具**（`pm2 start` 会 fork 自家守护进程后立刻退出，服务脱离 deployd 管理：status 失效、Stop 杀不到）。Nuxt 直接 `node .output/server.mjs` 即可--后台化、PID 记录、停止都由 deployd 负责（记录真实进程 PID，`svc -t` 能准确杀掉）
- run 需要多步操作时，长驻命令必须放**最后一行**（shell 会 exec 替换，保证 PID 正确）；更推荐把准备动作放进 build：

```yaml
deploy:
  run: |
    echo "preparing..."
    cp backup/app.jar . 2>/dev/null || true
    java -jar hello-world-0.0.1.jar   # 长驻命令，必须放最后一行
```

### 从旧版本迁移

旧配置的顶层 `run:` 字段已移除，启动时检测到会打印迁移提示：

```yaml
# 旧                                # 新
type: springboot                    type: jvm
run: { command: "java -jar a.jar" } deploy: { run: "java -jar a.jar" }
# 在 run.command 里自己 mv jar 的写法 → deploy: { artifact: "target/*.jar", dest: "/目标目录" }
```

### SSH 密钥认证

deployd 使用 SSH 密钥认证访问 Git 仓库。启动时会自动检测 `~/.ssh/` 下是否有可用密钥（按 `id_ed25519`、`id_rsa` 等顺序查找），如果没有则自动生成。

**使用流程：**

1. **启动时自动生成密钥**（或直接使用已有的）：
   ```bash
   deployd start
   ```
   启动日志会输出公钥内容，例如：
   ```
   [daemon] SSH key ready: /home/user/.ssh/id_ed25519
   [daemon] Public key: ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI... deployd@auto-generated
   ```

2. **配置公钥到 Git 平台**：
   - **GitHub**：Settings -> SSH and GPG keys -> New SSH key
   - **Gitee**：设置 -> 安全设置 -> SSH公钥
   - 将上面输出的公钥粘贴进去

3. **如果拉取代码报认证错误**，deployd 会自动提示公钥配置指引。

### 邮件通知

当配置了 `notifications.to` 后，每次部署完成后 deployd 会发送 HTML 邮件。

支持两种发送方式：

**方式一：SMTP（兼容 QQ邮箱、网易邮箱、自建邮件服务器等）**

```yaml
smtp:
  host: "smtp.qq.com"
  port: 465
  username: "your-email@qq.com"
  token: "your-smtp-authorization-code"  # 邮箱授权码，非登录密码
  tls: true
```

SMTP 端口 `465` 使用 SSL 连接，端口 `587` 使用 STARTTLS。

**方式二：Resend API（推荐，无需配置 SMTP）**

```yaml
resend:
  api_key: "re_xxxxxxxxxxxxxxxxxxxxx"   # 在 https://resend.com/api-keys 获取
  from: "deployd <onboarding@your-domain.com>"  # 需要先配置发件域名
```

> 两种方式二选一，优先使用 Resend。

邮件通知规则：
- **收件人**：配置的 `notifications.to` 列表 + Webhook Payload 或 Git 日志中的提交者邮箱
- **成功主题**：`[deployd] ✅ 部署成功: <服务名>`
- **失败主题**：`[deployd] ❌ 部署失败: <服务名>`，正文含失败阶段（fetch/build/stage/start）和完整错误信息

## 部署流程

### Webhook 自动部署

```
GitHub/Gitee Push
      │
      ▼
Webhook 服务器（按 仓库URL+分支 匹配服务）
      │
      ▼
同服务合并队列（连续推送合并为最新一次；flock 锁跨进程串行）
      │
      ▼
拉取代码（快路径 fetch+reset，失败回退全量克隆）
      ▼
Build（执行 build.command）
      ▼
Stage（部署专属：拷贝产物到 dest / 数据库迁移 / nginx reload；重启时跳过）
      ▼
停旧进程（如有）──▶ 启动新进程（记录真实 PID）──▶ 发送邮件通知
```

`deployd deploy <名称>` 走同一条流水线，只是不经过队列（部署中会直接提示"请勿重复操作"）。

## 架构

```
┌──────────────┐     ┌──────────────┐     ┌──────────────┐
│  GitHub/Gitee│────▶│  Webhook     │────▶│  合并队列+锁  │
│  推送事件     │     │  服务器       │     │  同服务串行化 │
└──────────────┘     └──────────────┘     └──────┬───────┘
                                                 │
                                                 ▼
                                          ┌──────────────┐
                     ┌───────────────────▶│  Orchestrator │
                     │                    │  编排层       │
                     │                    └──────┬───────┘
                     │ registry.Get(type)        │ Build ▸ Stage ▸ Stop? ▸ Start?
                     │                           ▼
        ┌────────────────────────────────────────────────────┐
        │              Plugin Registry（插件注册表）             │
        │   jvm    docker    static    node    python         │
        └────────────────────────────────────────────────────┘
              │        │                    │         │
           PID 管理  容器生命周期      无进程(nginx)  PID 管理
```

- **Webhook 服务器** - 解析推送事件，按仓库 URL + 分支匹配已配置的服务
- **合并队列 + 部署锁** - 同服务 webhook 触发合并去重、串行执行；手动部署与队列互斥
- **部署编排层** - 统一处理拉取、通知等跨类型逻辑；按部署模型能力决定是否执行停/启（static 无进程则跳过）
- **插件注册表 + 五种模型插件** - 按服务类型分发；新增类型只需实现 Deployer 接口并在 `init()` 自注册
- **进程管理器** - PID 文件跟踪运行中的进程，管理服务生命周期
- **邮件通知器** - 通过 SMTP 或 Resend API 发送部署结果邮件

更多设计细节见 `docs/superpowers/specs/`（生命周期重构设计、合并队列设计等）。

## 开发

```bash
# 编译
go build -o deployd .

# 运行测试
go test ./...

# 代码检查
go vet ./...
```

### 新增部署模型

1. 在 `plugins/<type>/` 实现 Deployer 接口（`Build/Stage/Status`；有独立进程再实现 `Start/Stop`），`init()` 中调用 `registry.Register`
2. 在 `plugins/plugins.go` 加 blank import，在 `internal/config/validate.go` 的 `supportedTypes` 加类型名
3. （可选）更新 `config.yaml.example` 与配置向导模板

## 许可证

MIT
