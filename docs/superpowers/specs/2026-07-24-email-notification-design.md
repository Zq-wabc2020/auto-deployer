# 邮件通知设计

**日期：** 2026-07-24
**状态：** 已审批

## 概述

为 deployd 添加邮件通知功能。部署失败时立即发送邮件，包含完整的错误信息便于排查；整个部署流程成功后发送一封确认邮件。收件人包括从 webhook payload 中提取的代码变更者 git 邮箱 + 配置的接收邮箱列表。

## 配置

在 `config.yaml` 顶层新增 `smtp` 和 `notifications` 两个块：

```yaml
smtp:
  host: "smtp.qq.com"
  port: 465
  username: "xxx@qq.com"
  token: "授权码"   # SMTP 授权码，非登录密码
  tls: true        # STARTTLS 或 SSL 加密

notifications:
  to:
    - "admin@example.com"
    - "team@example.com"
```

校验规则：当 `notifications.to` 非空时，所有 `smtp.*` 字段变为必填。

## 架构

### 新增包：`internal/notify`

单文件 `email.go`：

| 组件 | 职责 |
|------|------|
| `SMTPConfig` 结构体 | 保存 host、port、username、token、tls 标志 |
| `NotificationConfig` 结构体 | 保存接收邮箱列表 |
| `Notifier` 结构体 | 封装 SMTP 配置 + 接收列表，持有单个 TLS 连接 |
| `New(cfg SMTPConfig, to []string) (*Notifier, error)` | 构造函数，校验配置 |
| `Send(ctx, subject, body string) error` | 底层发送，接受 HTML 正文 |
| `NotifyDeployResult(ctx, svcName, branch, authorEmail, status, errMsg string) error` | 高层接口：组装主题和正文，群发到所有收件人 |

连接方式：端口 465 使用 `crypto/tls` 直连 SSL，其他端口使用 STARTTLS 升级。认证使用 `smtp.PlainAuth`，凭据为 username + token。

### 收件人

- `notifications.to` 配置的所有地址
- 从 webhook payload 提取的 `authorEmail`（如有则追加）
- 不隐藏互相可见的收件人（团队通知场景可接受）

### 邮件内容

**主题格式：**
- 成功：`[deployd] ✅ 部署成功: <服务名>`
- 失败：`[deployd] ❌ 部署失败: <服务名>`

**正文（HTML 表格格式）：**
- 成功邮件：服务名、分支、作者邮箱、时间戳、构建命令、运行命令
- 失败邮件：以上全部 + 失败阶段（git pull / build / restart）、完整错误信息、时间戳

### Webhook payload 扩展

在 `internal/webhook/server.go` 的 `DispatchResult` 中新增字段：

```go
type DispatchResult struct {
    ServiceName  string
    Branch       string
    RepoURL      string
    AuthorEmail  string   // 新增字段
}
```

提取来源：
- **GitHub**：`commits[0].author.email`（fallback 到 `committer.email`，再为空则留空）
- **Gitee**：同上路径 `commits[0].author.email`
- 如果 commits 数组为空或缺少 email，`AuthorEmail` 保持为空 —— 通知仍会发送到配置的 to 列表

### 集成点

1. **Webhook handler**（`internal/webhook/server.go`）：匹配到服务后，将调用 `NotifyDeployResult` 在部署流程成功/失败时触发邮件
2. **TriggerDeploy 命令**（`internal/daemon/commands.go`）：替换 TODO 占位符，在每一步调用 `NotifyDeployResult`，失败时携带错误详情
3. **配置向导**（`internal/config/wizard.go`）：交互提示中增加 SMTP 主机/端口/用户名/授权码和通知接收邮箱列表的输入

## 变更文件清单

| 操作 | 文件 |
|------|------|
| 修改 | `internal/config/config.go` — 新增 SMTPConfig、NotificationConfig |
| 修改 | `internal/config/validate.go` — 新增 SMTP 字段校验 |
| 修改 | `internal/config/wizard.go` — 新增 SMTP/通知配置交互提示 |
| 修改 | `internal/webhook/server.go` — DispatchResult 新增 AuthorEmail，ParsePayload 中提取 |
| 修改 | `internal/daemon/commands.go` — TriggerDeploy 集成 notifier |
| 新增 | `internal/notify/email.go` — 邮件发送核心逻辑 |
| 新增 | `internal/notify/email_test.go` — 单元测试 |
| 修改 | `config.yaml.example` — 添加 SMTP/通知配置示例 |

## 依赖

仅使用标准库：`crypto/tls`、`net/smtp`、`time`。无需第三方包。
