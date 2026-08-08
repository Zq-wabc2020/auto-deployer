# 修复：Webhook 部署日志隔离 + GitHub Webhook 400

## 问题 1：Webhook 触发的部署日志写入 deployd.log，而非服务日志

### 根因
部署流水线的所有输出（orchestrator 的 `fmt.Printf`、`build.Fetch` 的 git 输出、`build.ExecuteBuild` 的 maven 输出、plugin 的 `fmt.Printf`、`process.Manager.Stop`）都写入 `os.Stdout`。daemon 在 `daemon.go:95-96` 把 `os.Stdout`/`os.Stderr` 重定向到 `deployd.log`。手动触发（`deployd deploy`）能用，仅仅是因为 Linux fork 时把子进程的 stdout 重定向到了服务日志文件（`deploy.go:102-103`）——属于"歪打正着"。

服务日志隔离设计文档（`docs/superpowers/specs/2026-08-06-service-log-isolation.md` §2）本就规定 orchestrator 用 `logger.GetServiceLogger(svc.Name)`，但实现没照做，仍用 `fmt.Printf`。这也是为什么 `internal/deploy` 的测试当前**编译失败**（`mockDeployer` 缺 `SetOutput` 方法）。

### 修复方案（对齐设计文档 §2 核心思路："所有 deploy 相关日志统一写入服务日志文件"）
把整条部署流水线的输出都接到服务 logger 上，使手动触发与 webhook 触发都写入 `~/.deployd/services/<name>.log`。`deployd.log` 只保留 daemon 自身的 `[webhook]` 行 + 一行部署结果摘要（用户允许的"少量与服务相关动作信息"）。

具体改动：
1. **`internal/deploy/orchestrator.go`**：在 `Deploy` 顶部取 `log := logger.GetServiceLogger(svc.Name)`；把所有 `fmt.Printf("[deploy] …")` 换成 `log.Printf(…)`（去掉 `[deploy]` 前缀，由 logger 加 `[服务名]` 前缀，与设计示例一致）；`build.Fetch` 调用传入 `log`；`deployer.SetOutput(log)`；`sendNotify` 改用 `log`；在每个失败分支补 `log.Printf("… failed: %v", err)`（失败详情进服务日志）。
2. **`internal/build/git.go`**：`Fetch` 增加 `out io.Writer` 参数，4 个 git 子命令的 `cmd.Stdout/cmd.Stderr` 由 `os.Stdout/os.Stderr` 改为 `out`。（`Clone` 不在部署链路上，保持不动。）
3. **`internal/build/executor.go`**：`ExecuteBuild` 增加 `out io.Writer` 参数，`cmd.Stdout/cmd.Stderr` 改为 `out`，`fmt.Printf/Println` 改为 `fmt.Fprintf/Fprintln(out, …)`。
4. **`plugins/springboot/plugin.go`**：`Build` 调用 `ExecuteBuild(…, p.output)`；`Build`/`Start` 里的 `fmt.Printf/Println` 改写到 `p.output`；`Stop` 里 `mgr.SetOutput(p.output)` 后再 `mgr.Stop()`（让 "stopped process N" 进服务日志）。
5. **`internal/process/manager.go`**：给 `Manager` 加 `out io.Writer` 字段（`NewManager` 默认 `os.Stdout`）+ `SetOutput` 方法；`Start`/`Stop` 的 `fmt.Printf` 改 `fmt.Fprintf(m.out, …)`。向后兼容，现有调用方（daemon stop、service 命令、测试）默认走 `os.Stdout`，不受影响。
6. **`internal/webhook/server.go`**：删掉冗余的 `[deploy] deploy failed / deployed` 两行（orchestrator 已把详情写进服务日志）；保留一行 `[webhook] <svc> deploy <status>` 摘要写 deployd.log。
7. **测试**：`orchestrator_test.go` 给 `mockDeployer` 补 `SetOutput`（修复编译）；`executor_test.go`、`git_test.go` 的 `ExecuteBuild`/`Fetch` 调用补 `io.Discard` 入参。

### 行为说明
- 手动 fork（Linux）与 webhook：部署日志都进服务日志文件（一致）。✓
- macOS 前台 `deployd deploy xxx`：日志改为写入服务日志文件（原来部分打印到终端）。与生产环境一致，可用 `deployd logs <svc> -t` 跟踪。（设计文档 §2 与"所有 deploy 相关日志统一写入服务日志文件"支持此行为；文档里"日志输出到终端"的验证步骤描述的是旧的有缺陷行为。）
- `deployd.log`：只剩 `[webhook] received/matched/parse error` + 一行部署结果摘要。
- 性能：仅透传 `io.Writer` 指针 + 复用单例 logger，无额外分配、无新 goroutine；form-encoded 分支只在对应 Content-Type 时才走 `ParseForm`，JSON 路径不变。

---

## 问题 2：GitHub Webhook 返回 400

### 根因
服务器 `deployd.log` 中有 `[webhook] parse error: invalid character 'p' looking for beginning of value`。`'p'` 即 `payload=` 的首字符——GitHub 用 **form-encoded**（`application/x-www-form-urlencoded`，JSON 放在 `payload=` 字段）投递，而 `ParsePayload` 直接把原始 body 当 JSON 解析。Gitee 用原始 JSON，所以正常。配置里 `hello2` 服务（GitHub 仓库）因此从未生成 `hello2.log`。

### 修复方案
webhook body 读取同时支持两种 Content-Type（标准做法，非补丁）：
1. **`internal/webhook/server.go`**：新增 `readBody(r)`——当 `Content-Type` 含 `application/x-www-form-urlencoded` 时用 `r.ParseForm()` + `r.PostForm.Get("payload")` 取出 JSON；否则 `io.ReadAll(r.Body)`。替换原 `io.ReadAll(r.Body)` 调用。`detectSource` 仍靠 `X-GitHub-Event` 头识别，不受影响。
2. **`internal/webhook/server_test.go`**：新增 form-encoded GitHub push 用例（断言 200）。

---

## 验证步骤
1. `go build ./...` 通过；`go test ./...` 通过（含当前编译失败的 deploy 测试）。
2. 本地用 `httptest` 验证 form-encoded GitHub push 返回 200；JSON push 不回归。
3. 交叉编译 Linux 二进制，上传服务器，重启 daemon，真实 push GitHub `hello2` 仓库 → 生成 `hello2.log`，`deployd.log` 只剩 `[webhook]` 行 + 结果摘要。

> 部署到服务器属对外动作，执行前会再次与你确认。
