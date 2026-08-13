# P1 实施计划：部署生命周期重构（核心）

**分支：** feat/deployment-lifecycle-redesign
**关联设计：** docs/superpowers/specs/2026-08-13-deployment-lifecycle-redesign.md
**目标：** 让新设计可运行——生命周期接口(Stage+能力分离) + 插件注册表 + 配置重构(deploy:层) + sh -c 执行 + 旧字段检测 + fetch 优化 + jvm 插件适配 + 向导新格式。完成后 jvm 类型按新配置完整跑通，6 处 switch 消除。

---

## 关键发现（影响实施）

- 实际 `config.yaml` 是空壳（name/url/run.command 全空），无真实服务在跑 → 配置重构零迁移风险。
- `Deployer` 接口在 `orchestrator.go` 与 `webhook/server.go` 重复定义 → 合并到 `internal/deploy`。
- `Run`/`RunConfig` 仅 5 处非测试引用（plugin/wizard/config/validate/commands）→ 移除可控。
- `daemon.go:70` 的 `springboot.New()` 是空调用（无注册）→ 由 registry 取代。
- `validate.go` 硬编码 `supportedTypes={springboot}` 且强制 `run.command` 必填 → 重写。

## 约束（来自设计文档）

- 不删减外层功能：合并队列、flock 锁、日志方案A、服务日志隔离、SMTP/Resend 通知、收件人合并、webhook 解析、SSH 密钥、Java 版本检测、Linux fork/`--locked`、配置优先级、`logs -n/-t` 全部保留。
- 不保留旧配置兼容（单一新格式）；启动检测旧字段仅警告。
- `run` 下沉到 `deploy:`，纯启动，不含 nohup/&；后台化由 `process.Manager`(Setpgid) 负责。

---

## 实施步骤（有序，每步可编译可测，独立提交）

### Step 1 — 配置结构重构
**文件：** `internal/config/config.go`, `internal/config/validate.go`, `internal/config/config_test.go`, `internal/config/validate_test.go`

- `ServiceConfig`：保留 `name/type/repo/workspace/build`；新增 `Deploy yaml.Node`（策略层原始节点，插件自解析）；移除 `Run RunConfig`。
- 新增自定义 `Command` 类型（替代 `BuildConfig.Command string`）：`UnmarshalYAML` 同时接受 `command: "x"` 与 `command: ["a","b"]`，内部存 `[]string`，提供 `String()`/`Slice()`。
- `validate.go`：`build.command` 仍必填；移除 `run.command` 必填；`type` 校验改为查 registry（registry 未就绪前临时硬编码 `jvm`+`springboot` 别名，Step 2 后切换）。
- 旧字段检测：`Load` 后用 `map[string]any` 二次解码，若服务层存在 `run` 键，`fmt.Println` 迁移警告（不阻断、不报错）。
- **验证：** `go test ./internal/config/...`；`config.yaml.example` 能被 `Load` 解析。

### Step 2 — 生命周期接口 + 插件注册表
**文件：** `internal/deploy/orchestrator.go`, `internal/registry/registry.go`(新), `internal/registry/registry_test.go`(新), `internal/webhook/server.go`, `cmd/deploy.go`, `cmd/service_start.go`, `cmd/service_stop.go`, `cmd/service_restart.go`, `internal/daemon/daemon.go`, `plugins/springboot/plugin.go`, `internal/deploy/orchestrator_test.go`

- 合并 `Deployer` 接口到 `internal/deploy`：`Build/Stage/Status/SetOutput`；新增可选 `Startable{Start}` / `Stoppable{Stop}`。删除 `webhook/server.go` 内重复接口定义（改 import `deploy.Deployer`）。
- `internal/registry`：`Register(name, Factory)` / `Get(name) (Deployer, error)` / `Types() []string`。
- `plugins/springboot` 的 `init()` 自注册 `"jvm"` 与 `"springboot"` 别名。
- 新增 `plugins/all.go`：blank-import 各插件包触发 init；`cmd/root.go`（或 main）import 它。
- 替换 6 处 `switch svc.Type` 为 `registry.Get(svc.Type)`（cmd/deploy, service_start/stop/restart, webhook ExecuteDeploy + Handle 的类型校验）。
- `daemon.go:70` 的空 `springboot.New()` 删除（注册由 init 完成）。
- `orchestrator.Deploy`：`Build ▸ Stage ▸ (Stop if Stoppable) ▸ (Start if Startable) ▸ notify`。`ServiceStart/Stop/Restart` 改为能力类型断言，不支持返回 `"<type> 不支持 <op>，请用 deploy"`。
- **验证：** `go build ./...`；registry 单测；orchestrator 单测（mock 加 `Stage`，验证 Deploy 调用顺序、Restart 跳过 Build/Stage）。

### Step 3 — sh -c 执行 + process.Manager 统一启动 + jvm Stage
**文件：** `internal/build/executor.go`(或新 `internal/step/shell.go`), `plugins/springboot/plugin.go`, `internal/process/manager.go`, `plugins/springboot/plugin_test.go`

- 新增 `Shell(dir, command, env, out)` helper：经 `sh -c` 执行，支持 `&&`/`|`/`>`/`$VAR`；`build.command`（含列表形式，列表则依次 sh -c）与 `run` 都用它。
- `springboot.Start`：用 `sh -c` 执行 `deploy.run`；经 `process.Manager`（`Setpgid`）后台化并记录**真实进程 PID**；`run` 不含 nohup/&。Start 纯启动，不归位。
- `springboot.Stage`：解析 `deploy`（`artifact` glob + `dest`）；有 `artifact`(+`dest`) 则 `CopyArtifact`（dest 缺省=workspace 根），可选 `clean`(keep)；无 `artifact` 则 Stage noop（原地启动）。
- `springboot.Build`：只跑 build 命令（移除内联的 moveJarToRoot/cleanWorkspace，归位交给 Stage）。
- Java 版本检测逻辑保留在 jvm 插件内（build 与 run 均注入 JAVA_HOME）。
- **验证：** `plugin_test`；手动：jvm deploy + `service restart`（PID 正确、stop 能杀到真实进程、restart 不再报 mv 失败）。

### Step 4 — fetch 优化
**文件：** `internal/build/git.go`, `internal/build/git_test.go`

- `Fetch`：若 `.git` 存在且 remote origin 正确，先尝试 `git fetch --force origin <branch>` + `git checkout -f <branch>` + `git reset --hard FETCH_HEAD`；失败或非 git 仓库回退现有 clean 流程（init+remote+fetch+checkout）。
- 保留 Jenkins 风格 clean 作为回退，保证抗 force-push 健壮性。
- **验证：** `git_test`；手动 force-push 场景验证回退。

### Step 5 — 向导新格式 + 状态显示
**文件：** `internal/config/wizard.go`, `internal/config/wizard_test.go`, `internal/daemon/commands.go`

- `wizard`：询问 `type`（jvm/static/python/docker，默认 jvm）；按 type 引导填 `deploy` 字段（jvm：artifact?/dest?/run；其他类型先基础字段）。生成新格式（`build` + `deploy:`）。
- `commands.go`：状态/部署信息显示改用 `build.command` + 解析后的 `deploy.run`；移除 `svc.Run.Command` 引用。
- **验证：** `wizard_test`；`deployd config` 生成的新配置可被 `Load`+`Validate` 通过。

### Step 6 — 测试对齐与全量验证
- 更新 `orchestrator_test`, `config_test`, `validate_test`, `plugin_test`, `scheduler_test`, `webhook server_test` 适配新接口/配置。
- `go build ./... && go test ./... && go vet ./...` 全绿。
- `config.yaml.example` 已是新格式（本分支已完成）。

---

## 不在 P1 内（后续阶段，勿提前做）

- `docker` / `static` / `node` / `python` 插件实现（P3–P5）。
- 命令短旗标 `-s`/`-t`/`-r`（P6）。
- python 多进程（arq worker）支持。

## 风险与回滚

- 风险低：实际 config.yaml 是空壳，无在跑服务；外层（队列/锁/日志/通知/webhook/fork）不动。
- 回滚：每步独立提交，可逐回退。
