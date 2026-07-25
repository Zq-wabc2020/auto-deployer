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
curl -sSL https://raw.githubusercontent.com/auto-deployer/auto-deployer/main/install.sh | sh
```

### 从源码编译

```bash
go build -o deployd .
sudo mv deployd /usr/local/bin/
```

### 从 Release 下载

在 [Releases](https://github.com/auto-deployer/auto-deployer/releases) 页面下载最新二进制文件并放到 PATH 中即可。

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

`config.yaml` 主要配置项：

```yaml
server:
  host: "0.0.0.0"
  port: 9527

webhook:
  secret: ""  # 可选，用于 GitHub/Gitee Webhook 签名验证

smtp:
  host: "smtp.qq.com"
  port: 465
  username: "your-email@qq.com"
  token: "your-smtp-authorization-code"
  tls: true

notifications:
  to:
    - "admin@example.com"

services:
  - name: "my-app"
    type: "springboot"
    repo:
      url: "https://github.com/user/repo.git"
      branch: "main"
    workspace: "/opt/deployd/apps/my-app"
    build:
      command: "mvn package -DskipTests"
    run:
      command: "/opt/deployd/apps/my-app/start.sh"
```

#### 邮件通知

当配置了 `notifications.to` 后，每次部署完成后 deployd 会发送 HTML 邮件：

- **收件人**：配置的 `to` 列表 + Webhook Payload 中的 Git 提交者邮箱
- **成功主题**：`[deployd] ✅ 部署成功: <服务名>`
- **失败主题**：`[deployd] ❌ 部署失败: <服务名>`
- 失败邮件包含错误信息和失败阶段，方便排查问题

SMTP 端口 `465` 使用 SSL 连接，端口 `587` 使用 STARTTLS。

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
