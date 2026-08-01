# deployd

自动化部署守护进程 — 一个在后台运行的 CLI 工具，接收来自 GitHub / Gitee 的 Webhook 事件，自动完成服务的构建与重启。

## 功能特性

- **后台守护进程** — 在系统后台运行，不受终端关闭影响
- **Webhook 服务器** — 监听 GitHub / Gitee 推送事件，按仓库 URL 匹配对应服务
- **插件化部署器** — 可扩展的 Deployer 接口（Spring Boot 等）
- **邮件通知** — 通过 SMTP 在部署成功/失败时发送 HTML 邮件
- **CLI 管理** — start、stop、status、logs、deploy 等命令
- **交互式配置向导** — `deployd config` 引导完成配置
- **发布自动化** — GitHub Actions 自动构建 macOS / Linux 二进制

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
# 1. 运行交互式配置向导
deployd config

# 2. 启动守护进程
deployd start

# 3. 查看状态
deployd status

# 4. 手动触发部署
deployd deploy <服务名>

# 5. 查看日志
deployd logs              # 守护进程日志
deployd logs <服务名>     # 服务日志

# 6. 停止守护进程
deployd stop
```

### 命令说明

| 命令 | 描述 |
|------|------|
| `deployd start [-c 路径]` | 启动守护进程（默认配置文件：`~/config.yaml`） |
| `deployd stop` | 停止守护进程 |
| `deployd status` | 显示守护进程及所有服务状态 |
| `deployd logs [-f 路径] [服务名]` | 查看守护进程或服务日志 |
| `deployd deploy <名称>` | 手动触发指定服务的部署 |
| `deployd config` | 交互式配置向导 |

### 配置文件

复制示例文件后进行编辑：

```bash
cp config.yaml.example config.yaml
```

## Webhook URL 配置

服务启动后，Webhook 监听地址为：

```
http://<服务器IP>:<端口>/webhook
```

默认端口 `9527`，`server.host` 默认 `0.0.0.0`（监听所有网卡）。

**GitHub 配置步骤：**
1. 仓库 → Settings → Webhooks → Add webhook
2. Payload URL 填入 `http://<你的服务器IP>:9527/webhook`
3. Content type 选择 `application/json`
4. Secret 可填（当前未启用签名验证，可留空）
5. 选择 "Just the push event"
6. 点击 Add webhook

**Gitee 配置步骤：**
1. 仓库 → 管理 → WebHooks → 添加 WebHook
2. URL 填入 `http://<你的服务器IP>:9527/webhook`
3. 选择触发事件：Push 事件
4. 点击确认

> **注意：** 服务器需要有公网 IP 或可通过内网穿透暴露该端口，否则 GitHub/Gitee 无法回调。

## 配置项说明

复制示例文件后进行编辑：

```bash
cp config.yaml.example config.yaml
```

### 配置项说明

| 配置路径 | 说明 | 示例 |
|----------|------|------|
| `server.host` | 监听地址。`0.0.0.0` 监听所有网卡（允许外部访问），`127.0.0.1` 仅本机可访问 | `"0.0.0.0"` |
| `server.port` | Webhook 监听端口 | `9527` |
| `webhook.secret` | Webhook 签名验证密钥（预留字段，暂未启用） | `""` |
| `smtp.host` | SMTP 邮件服务器地址（Resend 方式时不需要） | `"smtp.qq.com"` |
| `smtp.port` | SMTP 端口，`465` 为 SSL，`587` 为 STARTTLS | `465` |
| `smtp.username` | 邮箱地址 | `"user@qq.com"` |
| `smtp.token` | 邮箱授权码（非登录密码，需在邮箱设置中生成） | `"xxxxxxxx"` |
| `smtp.tls` | 是否启用 TLS/SSL 连接 | `true` |
| `resend.api_key` | Resend API Key（[获取地址](https://resend.com/api-keys)） | `"re_xxx"` |
| `resend.from` | 发件人地址 | `"deployd <onboarding@example.com>"` |
| `notifications.to` | 部署通知邮件收件人列表 | `["admin@example.com"]` |
| `services[].name` | 服务名称，用于日志和管理命令 | `"my-app"` |
| `services[].type` | 服务类型，目前支持 `springboot` | `"springboot"` |
| `services[].repo.url` | Git 仓库地址（HTTPS 格式，会自动转为 SSH） | `"https://github.com/user/repo.git"` |
| `services[].repo.branch` | 部署分支 | `"main"` |
| `services[].workspace` | 代码克隆和工作目录 | `"/opt/deployd/apps/my-app"` |
| `services[].build.command` | 构建命令 | `"mvn package -DskipTests"` |
| `services[].run.command` | 启动命令 | `"/opt/deployd/apps/my-app/start.sh"` |

### 命令执行注意事项

- **不经过 shell 执行**，`&&`、`||`、`|`、`;` 等 shell 操作符无效
- **build 命令**在 `workspace` 目录下执行
- **run 命令**在 `workspace` 目录下执行（即 `java -jar xxx.jar` 会在 workspace 下找 jar 文件）
- 需要多步操作时，编写 shell 脚本并传入脚本路径：

```yaml
services:
  - name: "my-app"
    workspace: "/opt/deployd/apps/my-app"
    run:
      command: "/opt/deployd/apps/my-app/start.sh"
```

```bash
# start.sh
#!/bin/bash
cd /opt/deployd/apps/my-app
java -jar my-app.jar --spring.profiles.active=prod > logs/app.log 2>&1 &
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
   - **GitHub**：Settings → SSH and GPG keys → New SSH key
   - **Gitee**：设置 → 安全设置 → SSH公钥
   - 将上面输出的公钥粘贴进去

3. **如果拉取代码报认证错误**，deployd 会自动提示：
   ```
   [warn] SSH authentication failed. Public key to configure on your Git platform:
     ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI... deployd@auto-generated
   
   1. Add the above public key to your GitHub/Gitee account:
      GitHub: Settings → SSH and GPG keys → New SSH key
      Gitee:  设置 → 安全设置 → SSH公钥
   2. Key file: /home/user/.ssh/id_ed25519
   3. Re-run: deployd deploy <服务名>
   ```


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

邮件内容：
- **收件人**：配置的 `to` 列表 + Webhook Payload 中的 Git 提交者邮箱
- **成功主题**：`[deployd] ✅ 部署成功: <服务名>`
- **失败主题**：`[deployd] ❌ 部署失败: <服务名>`
- 失败邮件包含错误信息和失败阶段，方便排查问题

### 完整配置示例

```yaml
# deployd global configuration
server:
  host: "0.0.0.0"    # 0.0.0.0 = 监听所有网卡；127.0.0.1 = 仅本机可访问
  port: 9527         # Webhook 监听端口

webhook:
  secret: ""         # 预留字段，暂未启用签名验证

# SMTP 邮件通知配置（Resend 方式时不需要）
smtp:
  host: "smtp.qq.com"
  port: 465
  username: "your-email@qq.com"
  token: "your-smtp-authorization-code"  # 邮箱授权码，非登录密码
  tls: true

# Resend API 配置（替代 SMTP，发送邮件通过 Resend HTTP API）
# 获取 API Key: https://resend.com/api-keys
# 配置发件域名: https://resend.com/domains
resend:
  api_key: ""
  from: "deployd <onboarding@your-domain.com>"

# 部署通知收件人
notifications:
  to:
    - "admin@example.com"

# 服务列表
services:
  - name: "my-springboot-app"
    type: "springboot"
    repo:
      url: "https://github.com/user/repo.git"
      branch: "main"
    workspace: "/opt/deployd/apps/my-springboot-app"  # 代码克隆和工作目录
    build:
      command: "mvn package -DskipTests"   # 构建命令
    run:
      command: "/opt/deployd/apps/my-springboot-app/start.sh"  # 启动命令
```

## 架构

```
┌──────────────┐     ┌──────────────┐     ┌──────────────┐
│  GitHub/Gitee│────▶│  Webhook     │────▶│  Plugin      │
│  推送事件     │     │  服务器       │     │  部署器       │
└──────────────┘     └──────────────┘     └──────────────┘
                                         ┌──────────────┐
                                         │  进程管理      │
                                         └──────────────┘
                                         ┌──────────────┐
                                         │  SMTP        │
                                         │  邮件通知      │
                                         └──────────────┘
```

- **Webhook 服务器** — 解析推送事件，按仓库 URL 匹配已配置的服务
- **部署器插件** — 按服务类型执行构建和重启逻辑
- **进程管理器** — 跟踪运行中的进程，管理服务生命周期
- **邮件通知器** — 通过 SMTP 异步发送部署结果邮件

## 开发

```bash
# 编译
go build -o deployd .

# 运行测试
go test ./...

# 代码检查（需安装 golangci-lint）
golangci-lint run
```

## 许可证

MIT
