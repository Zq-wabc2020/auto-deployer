# 迁移指南：services → pipelines（v1 → v2）

旧版 `services[]` 由「服务类型（type）+ 策略字段（deploy:）」驱动，deployd 负责进程生命周期；新版改为 `pipelines[]`，由用户用 **git / shell / email / cleanup 四种组件**自由编排，deployd 只做编排。

**核心语义变化，迁移前必读：**

1. **deployd 不再拥有进程**：旧版自动后台化启动命令、记录 PID、就绪门控、`svc -s/-t/-r` 启停、僵尸回收——**全部删除**。新版这些事由你的 shell 节点自己写：后台启进程用 `nohup ... &`（旧版禁止，**新版必须**），重启杀旧进程自己写，想纳入系统管理就用 systemd 单元。
2. **健康检查没有内置替代**：旧 `deploy.health` 就绪门控删除。需要就绪等待就在 shell 节点里自己写 curl 轮询循环（示例见下）。
3. **通知从框架行为变为节点**：不配 email 节点就完全不发邮件。
4. **取消语义变化**：`deployd cancel` 从「停服务」变为「停当前节点 + 跑 `when: always` 清理节点」，不执行 `when: failure` 节点。
5. **命令面变化**：旧 `deployd deploy <name>` → 新 `deployd exec <name> [--key=value]`；旧 `svc -s/-t/-r` 删除（重启语义用 restart 流水线或 systemd）。

## 字段对照

| 旧字段 | 新去向 |
|---|---|
| `services[].name` | `pipelines[].name` |
| `services[].repo.url` / `.branch` | 第一个 git 节点的 `params.url` / `params.branch`（**字面量**，决定 webhook 匹配） |
| `services[].workspace` | `pipelines[].workspace` |
| `services[].timeout` | `pipelines[].timeout`（总预算语义保留）+ 可加节点级 `timeout` |
| `services[].build.command` | shell 节点的 `params.sh` |
| `services[].deploy.health` | shell 节点里自己写 `curl` 轮询循环 |
| `services[].deploy.env` | shell 节点 `params.env` 或脚本内 `export` |
| `services[].deploy.artifact` / `.dest` | shell 节点 `cp` / `rsync` |
| `services[].deploy.nginx_reload` | shell 节点 `nginx -s reload` |
| `services[].deploy.migrate` | shell 节点（部署流程里自己执行） |
| `services[].deploy.run` | shell 节点 `nohup ... &`（后台化自理） |
| `services[].type` | **删除**，由组件组合表达 |

## jvm（Spring Boot 等 `java -jar` 服务）

旧：

```yaml
services:
  - name: my-app
    type: jvm
    repo: { url: "https://github.com/user/repo.git", branch: "main" }
    workspace: "/opt/deployd/apps/my-app"
    timeout: "45m"
    build: { command: "mvn package -DskipTests" }
    deploy:
      artifact: "target/*.jar"
      dest: "/opt/my-app"
      run: "java -jar my-app.jar"
      health: "http://localhost:8080/health"
```

新：

```yaml
pipelines:
  - name: my-app
    workspace: "/opt/deployd/apps/my-app"
    timeout: "45m"
    stages:
      - name: 拉取代码
        type: git
        params:
          url: "https://github.com/user/repo.git"
          branch: ["main"]
      - name: 构建
        type: shell
        params:
          sh: "mvn package -DskipTests"
      - name: 部署（归位 + 重启 + 就绪轮询）
        type: shell
        params:
          sh: |
            mkdir -p /opt/my-app
            cp target/my-app.jar /opt/my-app/
            pkill -f "java -jar /opt/my-app/my-app.jar" 2>/dev/null || true   # 停旧进程
            nohup java -jar /opt/my-app/my-app.jar >/var/log/my-app.log 2>&1 & # 后台启新（新版必须自己 &）
            for i in $(seq 1 45); do                                            # 就绪轮询（替代旧 deploy.health）
              curl -sf http://localhost:8080/health && exit 0
              sleep 2
            done
            exit 1
      - name: 成功通知
        type: email
        params:
          subject: "部署 ${system.name} 成功"
          body: "commit ${output.拉取代码.commit}"
      - name: 失败通知
        type: email
        when: failure
        params:
          subject: "部署 ${system.name} 失败于 ${system.failed_stage}"
          body: "${system.failed_stage}: ${system.message}"
      - name: 清理工作空间
        type: cleanup
        when: always
        params:
          keep: [".git", "target"]   # target 保留可增量编译；缺省仅 .git
```

## static（Vue/React 静态站点，nginx 托管）

旧：

```yaml
services:
  - name: web-vue
    type: static
    repo: { url: "https://github.com/user/vue-app.git", branch: "main" }
    workspace: "/opt/web-vue"
    build: { command: "npm run build" }
    deploy:
      artifact: "dist"
      dest: "/usr/share/nginx/html/vue"
      nginx_reload: true
      health: "https://app.example.com/health"
```

新：

```yaml
pipelines:
  - name: web-vue
    workspace: "/opt/web-vue"
    stages:
      - name: 拉取代码
        type: git
        params:
          url: "https://github.com/user/vue-app.git"
          branch: ["main"]
      - name: 构建
        type: shell
        params:
          sh: "npm ci && npm run build"
      - name: 发布（rsync 到 nginx 目录 + reload）
        type: shell
        params:
          sh: |
            rsync -a --delete dist/ /usr/share/nginx/html/vue/
            nginx -s reload
      - name: 成功通知
        type: email
        params:
          subject: "发布 ${system.name} 成功"
          body: "commit ${output.拉取代码.commit}"
      - name: 失败通知
        type: email
        when: failure
        params:
          subject: "发布 ${system.name} 失败"
          body: "${system.failed_stage}: ${system.message}"
      - name: 清理工作空间
        type: cleanup
        when: always
        params:
          keep: [".git", "node_modules"]   # 保留依赖做增量构建（旧版 workspace 不清理的等价物）
```

> static 无独立进程，无需重启/就绪轮询；rsync 目录写权限与 `nginx -s reload` 权限需自行保证（可能需要 sudo 或对 nginx 属主）。

## node（Next.js SSR / Express 等常驻进程）

旧：

```yaml
services:
  - name: web-ssr
    type: node
    repo: { url: "https://github.com/user/ssr-app.git", branch: "main" }
    workspace: "/opt/web-ssr"
    build: { command: "npm run build" }
    deploy:
      run: "node .output/server.mjs"
      health: "http://localhost:3000/health"
```

新：

```yaml
pipelines:
  - name: web-ssr
    workspace: "/opt/web-ssr"
    stages:
      - name: 拉取代码
        type: git
        params:
          url: "https://github.com/user/ssr-app.git"
          branch: ["main"]
      - name: 构建
        type: shell
        params:
          sh: "npm ci && npm run build"
      - name: 部署（重启 + 就绪轮询）
        type: shell
        params:
          sh: |
            pkill -f "node .output/server.mjs" 2>/dev/null || true
            nohup node .output/server.mjs >/var/log/web-ssr.log 2>&1 &
            for i in $(seq 1 30); do
              curl -sf http://localhost:3000/health && exit 0
              sleep 2
            done
            exit 1
      - name: 成功通知
        type: email
        params:
          subject: "部署 ${system.name} 成功"
          body: "commit ${output.拉取代码.commit}"
      - name: 失败通知
        type: email
        when: failure
        params:
          subject: "部署 ${system.name} 失败"
          body: "${system.failed_stage}: ${system.message}"
      - name: 清理工作空间
        type: cleanup
        when: always
        params:
          keep: [".git", "node_modules", ".output"]
```

## python（FastAPI/Flask/Django 等 ASGI/WSGI 服务）

旧：

```yaml
services:
  - name: api-py
    type: python
    repo: { url: "https://github.com/user/py-app.git", branch: "main" }
    workspace: "/opt/api-py"
    build: { command: ["python3.11 -m venv .venv", ".venv/bin/pip install ."] }
    deploy:
      venv: ".venv"
      migrate: ".venv/bin/alembic upgrade head"
      run: ".venv/bin/uvicorn main:app --host 0.0.0.0 --port 8000"
      health: "http://localhost:8000/health"
```

新：

```yaml
pipelines:
  - name: api-py
    workspace: "/opt/api-py"
    stages:
      - name: 拉取代码
        type: git
        params:
          url: "https://github.com/user/py-app.git"
          branch: ["main"]
      - name: 构建（venv + 依赖）
        type: shell
        params:
          sh: "python3.11 -m venv .venv && .venv/bin/pip install ."
      - name: 迁移 + 部署（重启 + 就绪轮询）
        type: shell
        params:
          sh: |
            export PATH="/opt/api-py/.venv/bin:$PATH"   # 旧版 deploy.venv 字段 → 脚本内前置 PATH
            .venv/bin/alembic upgrade head               # 旧版 deploy.migrate
            pkill -f "uvicorn main:app" 2>/dev/null || true
            nohup .venv/bin/uvicorn main:app --host 0.0.0.0 --port 8000 >/var/log/api-py.log 2>&1 &
            for i in $(seq 1 30); do
              curl -sf http://localhost:8000/health && exit 0
              sleep 2
            done
            exit 1
      - name: 成功通知
        type: email
        params:
          subject: "部署 ${system.name} 成功"
          body: "commit ${output.拉取代码.commit}"
      - name: 失败通知
        type: email
        when: failure
        params:
          subject: "部署 ${system.name} 失败"
          body: "${system.failed_stage}: ${system.message}"
      - name: 清理工作空间
        type: cleanup
        when: always
        params:
          keep: [".git", ".venv", "node_modules"]   # .venv 保留做增量 install
```

> 注意：shell 节点的 `params.env` 值是字面量，不会做 shell 展开——需要在脚本里用 `export PATH=...:$PATH`（如上），不要把 `$PATH` 写进 `params.env`。

## docker（容器）

旧：

```yaml
services:
  - name: app-docker
    type: docker
    repo: { url: "https://github.com/user/docker-app.git", branch: "main" }
    workspace: "/opt/app-docker"
    build: { command: "docker build -t app:latest ." }
    deploy:
      image: "app:latest"
      container: "app"
      ports: ["8000:8000"]
      env: { FOO: "bar" }
      health: "http://localhost:8000/health"
```

新：

```yaml
pipelines:
  - name: app-docker
    workspace: "/opt/app-docker"
    stages:
      - name: 拉取代码
        type: git
        params:
          url: "https://github.com/user/docker-app.git"
          branch: ["main"]
      - name: 构建镜像
        type: shell
        params:
          sh: "docker build -t app:latest ."
      - name: 部署（滚动重启容器 + 就绪轮询）
        type: shell
        params:
          sh: |
            docker rm -f app 2>/dev/null || true
            docker run -d --name app --restart unless-stopped \
              -p 8000:8000 -e FOO=bar app:latest
            for i in $(seq 1 30); do
              curl -sf http://localhost:8000/health && exit 0
              sleep 2
            done
            exit 1
      - name: 成功通知
        type: email
        params:
          subject: "部署 ${system.name} 成功"
          body: "commit ${output.拉取代码.commit}"
      - name: 失败通知
        type: email
        when: failure
        params:
          subject: "部署 ${system.name} 失败"
          body: "${system.failed_stage}: ${system.message}"
      - name: 清理工作空间
        type: cleanup
        when: always
```

> 容器生命周期自理：`--restart unless-stopped` 让 docker 自己拉起崩溃的容器（等价旧版 deployd 的容器管理）。如使用 docker compose，把 `docker run` 换成 `docker compose up -d --build` 即可。

## 迁移步骤

1. `git log` 记录当前 HEAD；把旧配置另存备份
2. 对照上面五种 type 示例，把每个 `services[]` 项改写为一条 `pipelines[]`
3. 没有 git 节点的纯脚本工作项（如备份任务）：省略 git 节点即可，`deployd exec` 手动触发
4. 用 `deployd config` 向导重新生成，或 `deployd status` / `deployd exec <name>` 验证新配置可解析可执行
5. 旧命令替换：`deploy deploy <name>` → `exec <name>`；`svc -s/-t/-r` → 自写 restart 流水线或 systemd
