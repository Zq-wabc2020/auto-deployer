# Auto Deployer Design Spec

## Overview

一个 Go 编写的 CLI 工具，部署到服务器后作为后台守护进程运行，接收 GitHub/Gitee webhook 触发，自动执行 git pull → mvn package → 重启应用的部署流程。

**核心特性：**
- 去中心化架构：CLI 在哪台机器跑就在哪台部署
- 一对多管理：一个 CLI 实例管理多个服务
- 插件化设计：当前支持 Spring Boot，后续可扩展其他类型
- 交互式配置：首次使用引导用户完成配置
- 本地构建：代码在服务器上实时构建

---

## Architecture

```
┌─────────────────────────────────────────────┐
│              deployd (Go daemon)             │
│                                             │
│  ┌─────────┐  ┌──────────┐  ┌────────────┐ │
│  │ CLI     │  │ Webhook  │  │ Config     │ │
│  │ Commands│  │ HTTP Svc │  │ Manager    │ │
│  └────┬────┘  └────┬─────┘  └─────┬──────┘ │
│       │            │              │         │
│       └────────────┼──────────────┘         │
│                    │                        │
│         ┌──────────▼──────────┐             │
│         │   Dispatcher        │             │
│         │  (路由 + 校验)       │             │
│         └──────────┬──────────┘             │
│                    │                        │
│      ┌─────────────▼─────────────┐          │
│      │    Plugin Registry        │          │
│      │  springboot | nodejs ...  │          │
│      └─────────────┬─────────────┘          │
│                    │                        │
│  ┌─────────────────▼─────────────────┐      │
│  │        Process Manager            │      │
│  │  (start / stop / restart / log)   │      │
│  └───────────────────────────────────┘      │
└─────────────────────────────────────────────┘
```

### Core Components

| 组件 | 职责 |
|------|------|
| **CLI Commands** | 用户交互入口：`start/stop/restart/status/config/logs/deploy` |
| **Webhook HTTP Service** | 监听端口，接收 GitHub/Gitee 回调 |
| **Dispatcher** | 解析 payload，提取仓库 URL+分支，匹配配置，分发给插件 |
| **Plugin Registry** | 按 service type 注册和查找部署插件 |
| **Process Manager** | 管理应用进程生命周期（启动、停止、重启、查日志） |
| **Config Manager** | 读取 YAML 配置，启动时校验必填项，交互式补全 |

---

## Configuration

**文件格式：** YAML (`config.yaml`)

**示例：**

```yaml
server:
  host: "0.0.0.0"
  port: 9527

webhook:
  secret: ""  # 可选，GitHub/Gitee webhook 签名校验用的密钥

services:
  - name: "my-springboot-app"
    type: "springboot"
    repo:
      url: "https://github.com/user/repo.git"
      token: ""  # GitHub Personal Access Token
      branch: "main"
    workspace: "/opt/deployd/apps/my-springboot-app"
    build:
      command: "mvn package -DskipTests"
    run:
      command: "/opt/deployd/apps/my-springboot-app/start.sh"
```

**关键设计点：**
- `type` 字段决定使用哪个插件
- `repo.token` 支持 GitHub / Gitee 两种访问令牌格式
- `workspace` 是独立工作目录，每个服务互不干扰
- `build.command` 和 `run.command` 是可配置的字符串，支持多行
- `run.command` 建议指向启动脚本，便于复杂逻辑封装

---

## Plugin Interface

```go
type Deployer interface {
    Build(ctx context.Context, svc *ServiceConfig) error
    Start(ctx context.Context, svc *ServiceConfig) error
    Stop(ctx context.Context, svc *ServiceConfig) error
    Status(ctx context.Context, svc *ServiceConfig) (*Status, error)
    Type() string
}
```

每种服务类型一个插件目录：`plugins/springboot/`、未来加 `plugins/nodejs/`。

---

## Webhook Flow

```
GitHub/Gitee 推送代码
        │
        ▼
   deployd 接收 webhook
        │
        ▼
   解析 payload → 提取仓库 URL + 分支
        │
        ▼
   匹配配置文件中的 services
        │
        ▼
   分支校验：push 的分支是否在 service 配置的 branch 里？
        │
        ├── 不匹配 → 忽略，返回 200
        │
        └── 匹配 → 继续
                │
                ▼
          下载对应插件（springboot）
                │
                ▼
          git pull --force origin <branch>
                │
                ▼
          执行 build.command（mvn package -DskipTests）
                │
                ├── 失败 → 记录日志，返回 500，不重启
                │
                └── 成功 → 继续
                        │
                        ▼
                  执行 stop（停旧进程）
                        │
                        ▼
                  执行 start（启动新 jar）
                        │
                        ▼
                  返回 200 OK
```

**安全模型：**
- Webhook 接口通过 secret token 校验来源
- HTTPS 是运维层的事，CLI 不操心
- 部署结果记录在日志中，HTTP 返回 200 即可

---

## CLI Commands

```bash
deployd start          # 后台启动 webhook 服务 + 进程管理
deployd stop           # 停止 webhook 服务（不杀已部署的应用）
deployd restart        # 重启 webhook 服务
deployd status         # 查看所有服务和 webhook 服务的状态
deployd config         # 交互式配置向导（首次使用或修改配置）
deployd config --file  # 指定配置文件路径
deployd logs           # 查看 webhook 服务日志
deployd logs <service> # 查看某个应用的构建/部署日志
deployd deploy <name>  # 手动触发某个服务的部署（不依赖 webhook）
```

**启动时检查流程：**

```
deployd start
    │
    ├── 1. 检查配置文件是否存在
    │       ├── 不存在 → 提示运行 deployd config
    │       └── 存在 → 继续
    │
    ├── 2. 校验必填字段
    │       ├── name 缺失 → 报错
    │       ├── type 不支持 → 报错
    │       ├── repo.url 缺失 → 报错
    │       └── build.command / run.command 缺失 → 报错
    │
    ├── 3. 检查环境依赖
    │       ├── git 未安装 → 提示安装
    │       ├── mvn 未安装 → 提示安装
    │       └── java 未安装 → 提示安装
    │
    ├── 4. 检查工作目录是否存在
    │       └── 不存在 → 自动创建
    │
    └── 5. 全部通过 → 启动守护进程
```

---

## Installation & Distribution

**方式：** GitHub Releases + curl 脚本

**安装命令：**

```bash
curl -fsSL https://raw.githubusercontent.com/user/repo/main/install.sh | bash
```

或使用直接下载二进制：

```bash
curl -LO https://github.com/user/repo/releases/latest/download/deployd-linux-amd64.tar.gz
tar -xzf deployd-linux-amd64.tar.gz
sudo mv deployd /usr/local/bin/
```

**支持平台：**
- Linux amd64/arm64
- macOS amd64/arm64

---

## Technology Stack

| 维度 | 选择 |
|------|------|
| 语言 | Go |
| CLI 框架 | cobra |
| 配置解析 | gopkg.in/yaml.v3 |
| HTTP 服务 | net/http (标准库) |
| 进程管理 | os/exec |
| 构建工具 | github.com/go-git/go-git (或 exec git) |
| 日志 | log/slog (标准库) |

---

## Directory Structure

```
auto-deployer/
├── cmd/                    # CLI 命令入口
│   ├── start.go
│   ├── stop.go
│   ├── status.go
│   ├── config.go
│   ├── logs.go
│   └── deploy.go
├── internal/
│   ├── daemon/             # 守护进程管理
│   ├── webhook/            # Webhook HTTP 服务
│   ├── config/             # 配置解析和验证
│   ├── process/            # 子进程管理（Spring Boot）
│   └── build/              # 构建执行器（git/mvn）
├── plugins/
│   └── springboot/         # Spring Boot 部署插件
│       ├── builder.go
│       ├── runner.go
│       └── plugin.go
├── config.yaml.example     # 配置模板
├── go.mod
└── main.go
```

---

## Next Steps

1. 初始化 Go 模块和项目结构
2. 实现 CLI 命令框架（cobra）
3. 实现配置解析和验证
4. 实现 Spring Boot 插件
5. 实现 Webhook HTTP 服务
6. 实现守护进程和进程管理
7. 添加 GitHub Actions 构建和 Release 发布
8. 编写安装脚本和文档
