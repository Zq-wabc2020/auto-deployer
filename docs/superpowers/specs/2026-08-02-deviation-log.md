# 与原始设计偏离记录

**创建日期：** 2026-08-02
**状态：** 开发中

---

## 偏离列表

### 1. git pull 未使用 --force（已修复）

- **设计文档要求：** `git pull --force origin <branch>`
- **原始实现：** `git pull origin <branch>`（无 force）
- **原因：** 初始实现忽略了 force 选项
- **修复方式：** 改为 `git pull --force`，并增加 `git reset --hard` 和 `git clean -fd` 确保工作目录与远端完全一致
- **影响范围：** `internal/build/git.go` 的 `Pull` 函数

---

### 2. 构建后未移动 jar 到 workspace 根目录（已修复）

- **设计文档隐含要求：** 构建产物应便于运行（`run.command` 里写相对路径）
- **原始实现：** 无移动 jar 的逻辑，jar 留在 `target/` 目录下
- **原因：** 最初认为 run.command 会写绝对路径，但实际配置使用相对路径
- **修复方式：** 在 `Build` 完成后扫描 `target/` 目录找到 .jar 文件，复制到 workspace 根目录
- **影响范围：** `plugins/springboot/plugin.go` 新增 `moveJarToRoot` 和 `copyFile` 函数

---

### 3. 邮件通知未包含 git 提交人（已修复）

- **设计文档要求：** 收件人包括 `notifications.to` 配置的所有地址 + 从 webhook payload 提取的 `authorEmail`
- **原始实现：** 只在 `notifications.to` 非空时才发送邮件，`authorEmail` 有值但 `to` 为空时不通知
- **原因：** `hasNotifications` 函数只检查 `len(cfg.Notifications.To) > 0`，未考虑 authorEmail
- **修复方式：** `buildNotifier` 现在同时检查 SMTP/Resend 配置，并将 `authorEmail` 追加到收件人列表；`hasNotifications` 已移除
- **影响范围：** `internal/webhook/server.go`、`internal/daemon/commands.go`

---

### 4. logs 命令缺少 -n 和 -t 参数（已修复）

- **设计文档要求：** `deployd logs` 查看日志，设计预期有类似 `tail -n` 和 `tail -f` 的功能
- **原始实现：** 只支持完整输出日志文件，无行数限制和 follow 模式
- **修复方式：**
  - `-n` / `--tail`：只显示末尾 N 行
  - `-t` / `--follow`：实时跟随日志输出（类似 `tail -f`）
  - 组合使用 `-nt` 或 `-n 50 -t` 显示末尾 50 行并持续跟踪
- **影响范围：** `cmd/logs.go`、`internal/daemon/commands.go`

---

### 5. 启动方式：从阻塞式改为后台 fork（已修复）

- **设计文档要求：** `deployd start` 后台启动守护进程
- **原始实现（问题状态）：** 阻塞式启动，占用终端
- **用户反馈：** 设计本就是后台启动，不应阻塞终端
- **修复方式：**
  - Linux：`deployd start` 默认 fork 子进程到后台（setsid），父进程立即退出
  - macOS：保持阻塞模式（方便开发调试）
  - 新增 `--no-fork` 参数强制前台运行
- **影响范围：** `cmd/start.go`

---

## 设计符合项确认

以下功能与设计文档一致，无偏离：

| 功能 | 状态 |
|------|------|
| Webhook 接收 GitHub/Gitee push 事件 | ✅ 符合 |
| 分支匹配和 URL 规范化（HTTPS→SSH） | ✅ 符合 |
| SSH 密钥自动生成和引导 | ✅ 符合 |
| Spring Boot 插件化架构 | ✅ 符合 |
| 进程管理（PID 文件） | ✅ 符合 |
| Resend SMTP 支持 | ✅ 符合 |
| 交互式配置向导 | ✅ 符合 |
| 日志重定向到文件 | ✅ 符合 |
| SIGHUP 忽略（SSH 断开不终止） | ✅ 符合 |

---

## 待确认项

1. **jar 清理策略：** 当前在每次构建后移动 jar 到根目录。是否需要在启动服务后删除 `target/` 目录和源码？（设计文档提到"完成后清除拉取的代码"）
2. **logs follow 实现：** 当前用纯 Go 实现 tail -f，性能不如系统 `tail -f` 命令。是否需要在 Linux 上调用系统 `tail` 命令？

---

## 配置文件变更说明

| 配置项 | 变化 |
|--------|------|
| `notifications.to` | 必填（至少有一个邮箱），否则不发送邮件；`authorEmail` 会自动追加 |
| `run.command` | 建议写 `java -jar target/app.jar` 或构建后移动到根目录后写 `java -jar app.jar` |
