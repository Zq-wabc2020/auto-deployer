# deployd

自动化部署守护进程 - 接收 GitHub / Gitee Webhook 推送，按**工作项流水线（pipeline）**自动执行构建、部署与通知。deployd 只做编排（拉参数、跑节点、超时、互斥、记状态），不再内置任何部署知识——具体部署逻辑由你在流水线里用 **git / shell / email / cleanup 四种组件**自己拼装。

## 功能特性

- **流水线工作项** - 每个工作项是一条由任意数量节点组成的流水线模板；节点以组件方式执行，可自由组合
- **四种正交组件** - `git`（拉代码）/ `shell`（执行命令）/ `email`（通知）/ `cleanup`（清理），无需任何插件开发即可覆盖绝大多数部署场景
- **Webhook 匹配** - 监听 GitHub / Gitee 推送事件，按流水线第一个 git 节点的 `url + branch` 匹配
- **合并队列 + 部署锁** - 连续推送自动合并为最新一次执行；每工作项一把 flock 锁，手动 `exec` 与队列互斥
- **后置节点（when）** - `failure` / `always` 抽象了「失败发邮件 + 无论成败都清理」等收尾逻辑
- **四套参数插值** - `${system.*}` / `${env.*}` / `${args.*}` / `${output.<节点名>.*}`，节点参数、邮件正文全可引用
- **两级超时** - 整条流水线总预算 + 单节点 timeout（子 ctx，超时杀整个进程组）
- **状态与日志** - 状态文件落盘，彩色 `status` 展示；日志按 run / 节点分节，支持 `-n` 截断与实时跟随
- **git 快路径** - 已有仓库 `fetch + reset --hard`，失败自动回退全量克隆；HTTPS 自动转 SSH
- **交互式配置向导** - `deployd config` 生成 pipeline 格式配置
- **发布自动化** - GitHub Actions 自动构建 macOS / Linux 二进制

## 架构

```
GitHub/Gitee Push
      │
      ▼
Webhook 服务器（按首个 git 节点的 url+branch 匹配工作项）
      │
      ▼
每工作项合并队列（连续推送合并为最新一次；flock 锁跨进程串行）
      │
      ▼
流水线执行引擎（生成参数集 → 主流程 → 后置流程 → 状态落盘）
      │
      ├── git 组件：fetch 快路径 / 干净克隆 / HTTPS→SSH
      ├── shell 组件：sh -c 执行，进程组信号，超时杀得干净
      ├── email 组件：SMTP / Resend，subject/body 全参数插值
      └── cleanup 组件：清理 workspace（缺省保 .git）
```

- **执行引擎** - 一次执行（`exec` 前台或 daemon 队列触发，同一套引擎）：生成参数集（system + env + args，output 空）→ 按列表顺序执行主流程节点（fail-fast）→ 执行后置节点（when 匹配）→ 状态落盘
- **组件注册表** - 节点按 `type` 分发；新增组件只需实现 `Run(ctx, params) → (结果变量, error)` 并在 `init()` 自注册

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

```bash
# Linux x86_64（最常见的阿里云 ECS 机型）
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o deployd-linux-amd64 .

# Linux ARM64（阿里云倚天实例等）
GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -o deployd-linux-arm64 .
```

将生成的二进制上传到服务器：

```bash
scp deployd-linux-amd64 user@your-server:/usr/local/bin/deployd
chmod +x /usr/local/bin/deployd
```

### 从 Release 下载

在 [Releases](https://github.com/Zq-wabc2020/auto-deployer/releases) 页面下载最新二进制并放到 PATH 即可。

## 快速开始

```bash
# 1. 运行交互式配置向导（生成 pipeline 格式配置）
deployd config

# 2. 启动守护进程（接收 webhook；Linux 后台非阻塞，macOS 前台）
deployd start

# 3. 查看所有工作项状态
deployd status

# 4. 手动前台执行一个工作项（实时输出，Ctrl+C 取消；--key=value 进 ${args.*}）
deployd exec my-app --tag=v1.2

# 5. 查看最近一次 run 的日志
deployd logs my-app            # -n N 只显示后 N 行；-t 实时跟随

# 6. 停止守护进程
deployd stop
```

## CLI 全集

| 命令 | 行为 |
|---|---|
| `deployd start [-c 路径]` | 启动 daemon（Linux 后台非阻塞 / macOS 前台） |
| `deployd stop` | 停止 daemon |
| `deployd status [-c 路径]` | 所有工作项最后执行状态（彩色） |
| `deployd exec <工作项名> [--key=value ...]` | 前台执行，实时输出，Ctrl+C=cancel，TryAcquire 抢锁；`--key=value` 进 `${args.*}` |
| `deployd cancel <工作项名>` | 取消执行中的工作项（写 sentinel，执行器轮询命中即取消当前节点） |
| `deployd logs [工作项名] [-n N] [-t] [-c 路径]` | 最近一次 run 的日志；不填工作项名 = daemon 日志 |
| `deployd config` | 交互式配置向导 |

> `logs` 的 `-n N` 显示最后 N 行、`-t/--follow` 实时跟随；`-f` 是手动指定日志文件路径（`--file`）。

### 状态含义

`deployd status` 输出的状态共五种（终端里带颜色，重定向到文件时为纯文本）：

| 状态 | 颜色 | 含义 |
|------|------|------|
| `running` | 蓝色 | 执行中 |
| `success` | 绿色 | 主流程全部节点成功 |
| `failed` | 红色 | 主流程失败（含超时）；`running` 但锁空 = 执行进程已死，就地改写为 `failed` |
| `cancelled` | 黄色 | 被取消（always 节点已跑完） |
| `never` | 灰色 | 从未执行过 |

每行还显示触发方式（manual/webhook）、耗时、commit 短 sha、失败节点。

## Webhook URL 配置

daemon 启动后，Webhook 监听地址为：

```
http://<服务器IP>:<端口>/webhook
```

端口在配置 `server.port` 指定（模板示例用 9527）。**注意：**

- `server.host: "0.0.0.0"` 会把 webhook 暴露到公网。`webhook.secret` 留空 = 不校验签名（端口可达即任何人均可伪造 push 触发构建+部署），**公网部署务必配置密钥**（见下）；配置密钥后对无签名/签名不符的请求一律拒绝（401），配置损坏返回 500
- 服务器需要有公网 IP 或可通过内网穿透暴露该端口，否则 GitHub/Gitee 无法回调

**GitHub 配置步骤：** 仓库 → Settings → Webhooks → Add webhook → Payload URL 填 `http://<你的服务器IP>:<端口>/webhook` → Content type `application/json` → Secret 填与 `webhook.secret` 相同的值 → 选择 "Just the push event"。deployd 校验 `X-Hub-Signature-256`（HMAC-SHA256，含旧版 `X-Hub-Signature` sha1 兼容）。

**Gitee 配置步骤：** 仓库 → 管理 → WebHooks → 添加 WebHook → URL 填 `http://<你的服务器IP>:<端口>/webhook` → 触发事件选 Push → 密钥（密码或签名密钥）填与 `webhook.secret` 相同的值。deployd 两种 Gitee 认证都支持：密码模式校验 `X-Gitee-Token == secret`；**签名密钥（加签）模式**按 Gitee 官方算法校验 `X-Gitee-Token == base64(HMAC-SHA256(secret, timestamp+"\n"+secret))`（兼容 urlencoded），算法细节见 config.yaml.example 注释。

## 配置说明

```bash
cp config.yaml.example config.yaml   # 模板含完整工作项示例与全部组件的用法注释
```

配置由**全局配置**（server / webhook / smtp / resend / notifications）+ **工作项列表**（`pipelines:`）组成。全局配置与旧版一致；工作项由节点编排。

### 工作项结构

```yaml
pipelines:
  - name: "my-app"
    workspace: "/opt/deployd/apps/my-app"   # 代码克隆与工作目录（每个工作项唯一）
    timeout: "45m"                          # 可选：整条流水线总超时（默认 30m）
    env: { JAVA_OPTS: "-Xms100m" }          # 可选：工作项级环境变量（${env.xxx}）
    stages:
      - name: 拉取代码
        type: git                          # 组件类型：git / shell / email / cleanup
        params: { url: "...", branch: ["main"] }   # 组件参数
        output: { commit: "${commit}" }    # 可选：结果变量 → 命名参数
        # skip: "${args.skip_build}"       # 可选：插值后等于 "true" 即跳过；引用未传参数会报错（不静默置空）
        when: ""                           # 可选：""(主流程) / failure / always
        timeout: "10m"                     # 可选：单节点超时
      - name: ...
```

- **触发匹配**：第一个 git 节点的 `url` / `branch` 决定 webhook 匹配，**必须是字面量**（不能写 `${...}` 引用）。没有 git 节点的工作项无法被 webhook 匹配，只能 `exec` 手动触发
- **校验**（配置加载期）：工作项名唯一、workspace 必填且唯一、节点名工作项内唯一、`type` 已注册、`when` 枚举合法、timeout 为合法 duration
- **未定义的参数引用不做加载期全量静态检查**：引用可指向运行期才有的 output/system 值；运行期节点启动时求值失败即该节点失败并报明确错误，不静默当空串

### 四套参数

引用语法 `${前缀.键}`，统一插值器；值可以是参数引用与字面量的拼接（如 `"部署 ${system.name} @ ${output.拉取代码.v}"`）。

| 参数集 | 前缀 | 来源 | 生成时机 |
|---|---|---|---|
| 系统参数 | `${system.*}` | name、trigger（manual/webhook）、commit、branch、author、message、pushers（webhook 合并任务的作者列表）、workspace、default_to（全局 notifications.to + pushers，email to 缺省用）、result（success/failed，仅后置节点有值）、failed_stage、error（失败原因文本，仅后置节点有值） | 运行期，每次执行生成一份 |
| 环境变量参数 | `${env.*}` | 工作项 `env:` 块 | 配置期定义 |
| 命令行参数 | `${args.*}` | `deployd exec xxx --foo=bar` → `args.foo=bar`；webhook 触发时为空集 | 触发时 |
| 节点输出参数 | `${output.<节点名>.<键>}` | 各节点 `output:` 映射结果 | 节点执行后累积 |

**组件结果变量与 output 映射**：组件执行后产生一组**裸名结果变量**，只能在**本节点的 `output:` 值里**用裸名引用（`output: {app_version: "${stdout}"}`），映射后才能被后续节点以 `${output.<节点名>.<键>}` 引用。`output` 值也可以是字面量或四套参数引用（透传）。`stdout` 等大结果存内存**超过 1MB 截断**（防构建日志撑爆参数集；完整日志在日志文件里）。

**skip / when 求值**：`skip` 插值后等于字符串 `true` 即跳过（不做表达式语言）；`when` 枚举 `failure` / `always`，缺省 = 主流程。

### when 语义（后置执行机制）

- **缺省 when** = 主流程节点：按列表顺序执行，任一失败则跳过剩余主流程节点（fail-fast）
- `when: failure` = 仅主流程失败时执行
- `when: always` = 无论成败都执行

主流程结束后（成功或失败），后置节点（when=failure/always）按**列表顺序**执行。成功邮件 = 主流程末尾的普通 email 节点；失败邮件 = `when: failure` 的 email 节点；清理 = `when: always` 的 cleanup 节点。后置节点自身失败只记日志，不递归触发其他后置节点，不影响最终状态。取消（cancelled）时 `always` 节点仍执行（清理语义），`failure` 节点不执行（cancelled ≠ failed）。本机制等价于 GitHub Actions `if: always()/failure()` 的扁平化。

### 四种组件

| 组件 | params | 结果变量 | 说明 |
|---|---|---|---|
| `git` | `url`*、`branch`*（列表）、`workspace`（缺省 `${system.workspace}`） | `commit`、`changed`（bool，无新提交）、`branch` | fetch 快路径（fetch + reset --hard origin/<branch>）+ 失败回退干净克隆；HTTPS 自动转 SSH |
| `shell` | `sh`*（单行或多行 YAML 块）、`cwd`（缺省 workspace）、`env`（本次执行额外环境变量） | `exit_code`、`stdout`、`stderr` | `sh -c` 执行，进程组信号（超时/取消杀得干净）；多行块即多行脚本（Jenkins `sh '''` 对应物）；非零退出码 = 失败。**默认无 `-e`**（Jenkins「Execute shell」是 `sh -xe`）：期望一行失败即停、未定义变量报错，脚本首行写 `set -eu`；管道严格再 `set -o pipefail`（否则 `a \| b` 只看 b 的退出码） |
| `email` | `to`（缺省 = 全局 `notifications.to` + `system.pushers`）、`subject`/`body`（成对可选）、`template`（可选，`default`/`默认模板`） | `error`（发送失败信息） | 复用 SMTP/Resend 发送层；subject/body 全参数插值。**subject/body 都省略 = 内置标准模板**（主题 `[自动部署] ✅/❌ 部署成功/失败: <名>（<手动/自动触发>）`，正文 HTML 表格：服务名/分支/提交记录/状态/时间/变更者，失败邮件追加 失败阶段/错误信息）；`template: "默认模板"` 可显式声明（与省略等价）；**都提供 = 配置显式优先**；只提供一个报错 |
| `cleanup` | `keep`（glob 列表，缺省 `[".git"]`） | `deleted`（清理条目数） | 删 workspace 下不在 keep 内的一切；`.git` 缺省保留是为了下轮 fetch 快路径 |

`*` = 必填（有缺省值者除外）。节点失败定义：组件返回 error，或节点超时。

### 超时、取消、锁与队列

- **两级超时**：整条流水线 `timeout` 是总预算 ctx（默认 30m，罩全程）；节点 `timeout` 是总 ctx 的子 ctx，节点超时 → 该节点 failed（reason=timeout），后置节点照常
- **取消**：`deployd cancel <name>` 写 `~/.deployd/run/<name>.cancel` sentinel，执行器轮询命中即取消当前节点（杀进程组）。取消后执行 `when: always` 节点、不执行 `when: failure` 节点，状态 = cancelled。exec 前台时 Ctrl+C / SIGINT / SIGTERM 等价 cancel
- **锁与队列**：每工作项一把 flock（`~/.deployd/run/<name>.deploy.lock`）。webhook 触发不直接执行，入每工作项队列；队列处理器阻塞抢锁，持锁期间 drain 队列只执行**最新任务**，被合并任务的作者并入 `system.pushers`。`deployd exec` TryAcquire 非阻塞抢锁：队列忙（pending 或 in-flight）直接拒绝。一条 push 匹配多个工作项（monorepo）时各工作项独立队列独立执行

### 配置文件优先级

`-c` 标志 > 当前目录 `config.yaml` > `~/.deployd/config.yaml` > daemon 上次启动用的配置（记录在 `~/.deployd/config.path`，每次 `deployd start` 覆盖写）> `~/config.yaml`（旧默认，兼容保留）

> daemon 与所有 CLI 命令共用同一套解析顺序，不会出现 "daemon 读 A、status 读 B"。只要用 `deployd start` 启动过，在任何目录执行 `status`/`logs` 都能找到配置。

## SSH 密钥认证

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

2. **配置公钥到 Git 平台**：GitHub → Settings → SSH and GPG keys → New SSH key；Gitee → 设置 → 安全设置 → SSH公钥。将公钥粘贴进去。

3. **拉取代码报认证错误时**，deployd 会自动提示公钥配置指引。

## 邮件通知

邮件通知是**流水线里的 email 节点**，由用户自己编排（不配 email 节点就完全不发，旧版全局通知行为消失）。收件人缺省 = 全局 `notifications.to` + 触发者；成功/失败邮件分别用普通 email 节点 / `when: failure` 的 email 节点实现。

支持两种发送方式（**二选一**，优先 Resend）：

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

## 兼容矩阵（旧版 → 新版）

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

## 从旧版本迁移

旧版 `services[]`（type 驱动）与新版 `pipelines[]`（组件编排）**不兼容**，需要手改。每个旧 `services[]` 项改成一条 pipeline：`name/repo/workspace/timeout` 原样搬，`type` 语义拆解成对应的组件节点序列（旧 `deploy.health` 就绪等待 → shell 节点里自己写轮询 curl，旧 `build.command` → shell 节点，旧 artifact 归位 → shell 节点 cp/rsync）。五种旧 type 的完整改写示例见 **`docs/migration-v2.md`**。

## 开发

```bash
# 编译
go build -o deployd .

# 运行测试
go test ./...

# 代码检查
go vet ./...
```

### 新增组件

1. 在 `internal/components/` 新建文件，实现 `Component` 接口（`Run(ctx, Request) (Results, error)`），`init()` 中调用 `components.Register("<type名>", ...)`
2. 在 `internal/config/validate.go` 的 `known` 检查（自动从注册表取）无需手改；节点 `type` 即可使用
3. 更新 `config.yaml.example` 与 `docs/migration-v2.md` 的组件说明

## 许可证

MIT
