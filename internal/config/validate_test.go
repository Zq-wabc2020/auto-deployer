package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestValidate_ValidConfig(t *testing.T) {
	cfg := &AppConfig{
		Server: ServerConfig{Host: "0.0.0.0", Port: 9527},
		Services: []ServiceConfig{
			{
				Name:      "my-app",
				Type:      "jvm",
				Repo:      RepoConfig{URL: "https://github.com/u/r.git", Branch: "main"},
				Workspace: "/tmp/app",
				Build:     BuildConfig{Command: Command{"mvn package"}},
				HealthURL: "http://localhost:8080/health",
			},
		},
	}

	errs := Validate(cfg)
	if len(errs) != 0 {
		t.Errorf("expected no errors, got %v", errs)
	}
}

func TestValidate_SpringbootAlias(t *testing.T) {
	cfg := &AppConfig{
		Services: []ServiceConfig{{
			Name:      "app",
			Type:      "springboot",
			Repo:      RepoConfig{URL: "https://github.com/u/r.git", Branch: "main"},
			Workspace: "/tmp/app",
			Build:     BuildConfig{Command: Command{"mvn package"}},
		}},
	}
	errs := Validate(cfg)
	for _, e := range errs {
		if strings.Contains(e.Error(), "type") {
			t.Errorf("springboot should be accepted as jvm alias, got: %v", e)
		}
	}
}

func TestValidate_MissingName(t *testing.T) {
	cfg := &AppConfig{
		Services: []ServiceConfig{{Type: "jvm"}},
	}

	errs := Validate(cfg)
	found := false
	for _, e := range errs {
		if strings.Contains(e.Error(), "name") {
			found = true
		}
	}
	if !found {
		t.Error("expected error mentioning 'name'")
	}
}

func TestValidate_UnknownType(t *testing.T) {
	cfg := &AppConfig{
		Services: []ServiceConfig{{Name: "app", Type: "unknown"}},
	}

	errs := Validate(cfg)
	found := false
	for _, e := range errs {
		if strings.Contains(e.Error(), "type") {
			found = true
		}
	}
	if !found {
		t.Error("expected error mentioning 'type'")
	}
}

func TestValidate_MissingRepoURL(t *testing.T) {
	cfg := &AppConfig{
		Services: []ServiceConfig{{Name: "app", Type: "jvm"}},
	}

	errs := Validate(cfg)
	found := false
	for _, e := range errs {
		if strings.Contains(e.Error(), "repo") {
			found = true
		}
	}
	if !found {
		t.Error("expected error mentioning 'repo'")
	}
}

func TestValidate_MissingWorkspace(t *testing.T) {
	cfg := &AppConfig{
		Services: []ServiceConfig{{
			Name: "app",
			Type: "jvm",
			Repo: RepoConfig{URL: "https://github.com/u/r.git"},
		}},
	}

	errs := Validate(cfg)
	found := false
	for _, e := range errs {
		if strings.Contains(e.Error(), "workspace") {
			found = true
		}
	}
	if !found {
		t.Error("expected error mentioning 'workspace'")
	}
}

func TestValidate_MissingBuildCommand(t *testing.T) {
	cfg := &AppConfig{
		Services: []ServiceConfig{{
			Name:      "app",
			Type:      "jvm",
			Repo:      RepoConfig{URL: "https://github.com/u/r.git", Branch: "main"},
			Workspace: "/tmp/app",
		}},
	}

	errs := Validate(cfg)
	found := false
	for _, e := range errs {
		if strings.Contains(e.Error(), "build.command") {
			found = true
		}
	}
	if !found {
		t.Error("expected error mentioning 'build.command'")
	}
}

func TestValidate_RunCommandNotRequired(t *testing.T) {
	// run.command 已下沉为 deploy.run，不再在通用层必填。
	cfg := &AppConfig{
		Services: []ServiceConfig{{
			Name:      "app",
			Type:      "jvm",
			Repo:      RepoConfig{URL: "https://github.com/u/r.git", Branch: "main"},
			Workspace: "/tmp/app",
			Build:     BuildConfig{Command: Command{"mvn package"}},
		}},
	}

	errs := Validate(cfg)
	for _, e := range errs {
		if strings.Contains(e.Error(), "run.command") {
			t.Errorf("run.command should not be required, got: %v", e)
		}
	}
}

func TestValidate_BuildCommandList(t *testing.T) {
	// build.command 支持列表形式。
	cfg := &AppConfig{
		Services: []ServiceConfig{{
			Name:      "app",
			Type:      "jvm",
			Repo:      RepoConfig{URL: "https://github.com/u/r.git", Branch: "main"},
			Workspace: "/tmp/app",
			Build:     BuildConfig{Command: Command{"npm ci", "npm run build"}},
			HealthURL: "http://localhost:8080/health",
		}},
	}
	errs := Validate(cfg)
	if len(errs) != 0 {
		t.Errorf("expected no errors for list-form build command, got %v", errs)
	}
}

func TestValidate_MultipleServices(t *testing.T) {
	cfg := &AppConfig{
		Services: []ServiceConfig{
			{
				Name:      "app1",
				Type:      "jvm",
				Repo:      RepoConfig{URL: "https://github.com/u/r1.git", Branch: "main"},
				Workspace: "/tmp/app1",
				Build:     BuildConfig{Command: Command{"mvn package"}},
			},
			{
				Name:      "",
				Type:      "unknown",
				Repo:      RepoConfig{},
				Workspace: "",
				Build:     BuildConfig{},
			},
		},
	}

	errs := Validate(cfg)
	if len(errs) < 4 {
		t.Errorf("expected at least 4 errors for second service, got %d: %v", len(errs), errs)
	}
}

func TestValidate_SMTPMissing(t *testing.T) {
	cfg := &AppConfig{
		Server:   ServerConfig{Host: "0.0.0.0", Port: 9527},
		Services: []ServiceConfig{{Name: "test", Type: "jvm", Repo: RepoConfig{URL: "https://github.com/x/x.git", Branch: "main"}, Workspace: "/tmp", Build: BuildConfig{Command: Command{"true"}}, HealthURL: "http://localhost:8080/health"}},
		Notifications: NotificationConfig{To: []string{"a@b.com"}},
		SMTP:       SMTPConfig{}, // empty
	}
	errs := Validate(cfg)
	if len(errs) != 1 {
		t.Fatalf("expected 1 error, got %d: %v", len(errs), errs)
	}
	if errs[0].Error() != "either smtp.host or resend.api_key is required when notifications.to is set" {
		t.Fatalf("unexpected error: %v", errs[0])
	}
}

func TestValidate_SMTPComplete(t *testing.T) {
	cfg := &AppConfig{
		Server:   ServerConfig{Host: "0.0.0.0", Port: 9527},
		Services: []ServiceConfig{{Name: "test", Type: "jvm", Repo: RepoConfig{URL: "https://github.com/x/x.git", Branch: "main"}, Workspace: "/tmp", Build: BuildConfig{Command: Command{"true"}}, HealthURL: "http://localhost:8080/health"}},
		Notifications: NotificationConfig{To: []string{"a@b.com"}},
		SMTP:       SMTPConfig{Host: "smtp.qq.com", Port: 465, Username: "x@qq.com", Token: "abc"},
	}
	errs := Validate(cfg)
	if len(errs) != 0 {
		t.Fatalf("expected 0 errors, got %d: %v", len(errs), errs)
	}
}

func TestValidate_SMTPNotRequiredWhenNoTo(t *testing.T) {
	cfg := &AppConfig{
		Server:        ServerConfig{Host: "0.0.0.0", Port: 9527},
		Services:      []ServiceConfig{{Name: "test", Type: "jvm", Repo: RepoConfig{URL: "https://github.com/x/x.git", Branch: "main"}, Workspace: "/tmp", Build: BuildConfig{Command: Command{"true"}}, HealthURL: "http://localhost:8080/health"}},
		Notifications: NotificationConfig{To: nil},
		SMTP:          SMTPConfig{}, // empty, but to is empty so OK
	}
	errs := Validate(cfg)
	if len(errs) != 0 {
		t.Fatalf("expected 0 errors, got %d: %v", len(errs), errs)
	}
}

func TestValidateRequiresHealth(t *testing.T) {
	cfg := loadStr(t, `
services:
  - name: s1
    type: jvm
    workspace: /tmp/s1
    build: { command: "true" }
    deploy: { run: "java -jar x" }   # 缺 health
`)
	errs := Validate(cfg)
	found := false
	for _, e := range errs {
		if strings.Contains(e.Error(), "health") {
			found = true
		}
	}
	if !found {
		t.Fatalf("缺 deploy.health 应校验失败, got: %v", errs)
	}
}

func TestValidate_DuplicateWorkspace(t *testing.T) {
	svc := func(name, ws string) ServiceConfig {
		return ServiceConfig{
			Name:      name,
			Type:      "jvm",
			Repo:      RepoConfig{URL: "https://github.com/u/r.git", Branch: "main"},
			Workspace: ws,
			Build:     BuildConfig{Command: Command{"mvn package"}},
		}
	}
	cfg := &AppConfig{
		Services: []ServiceConfig{svc("a", "/tmp/ws"), svc("b", "/tmp/ws")},
	}

	errs := Validate(cfg)
	found := false
	for _, e := range errs {
		if strings.Contains(e.Error(), "workspace") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected duplicate workspace error, got %v", errs)
	}
}

func TestValidatePipelines(t *testing.T) {
	gitStage := func(name string, params map[string]any) StageConfig {
		return StageConfig{Name: name, Type: "git", Params: params}
	}
	base := func(stages ...StageConfig) *AppConfig {
		return &AppConfig{Pipelines: []PipelineConfig{{
			Name: "app", Workspace: "/tmp/ws",
			Stages: append([]StageConfig{gitStage("拉取", map[string]any{
				"url": "https://github.com/u/r.git", "branch": []any{"main"},
			})}, stages...)}}}
	}

	if errs := Validate(base()); len(errs) != 0 {
		t.Errorf("合法配置不应报错: %v", errs)
	}

	// stage 重名
	cfg := base(StageConfig{Name: "拉取", Type: "shell", Params: map[string]any{"sh": "ls"}})
	if errs := Validate(cfg); len(errs) == 0 {
		t.Error("stage 重名必须报错")
	}

	// 未知组件类型
	cfg = base(StageConfig{Name: "构建", Type: "nope", Params: map[string]any{}})
	if errs := Validate(cfg); len(errs) == 0 {
		t.Error("未知 type 必须报错")
	}

	// when 枚举
	cfg = base(StageConfig{Name: "通知", Type: "email", When: "sometimes", Params: map[string]any{"subject": "s", "body": "b"}})
	if errs := Validate(cfg); len(errs) == 0 {
		t.Error("非法 when 必须报错")
	}

	// 缺 name/workspace
	cfg = base()
	cfg.Pipelines[0].Name = ""
	if errs := Validate(cfg); len(errs) == 0 {
		t.Error("缺 name 必须报错")
	}
	cfg = base()
	cfg.Pipelines[0].Stages = nil
	if errs := Validate(cfg); len(errs) == 0 {
		t.Error("无 stages 必须报错")
	}
}

func TestValidatePipelineWorkspaceUnique(t *testing.T) {
	cfg := &AppConfig{Pipelines: []PipelineConfig{
		{Name: "a", Workspace: "/tmp/ws", Stages: []StageConfig{{Name: "s", Type: "shell", Params: map[string]any{"sh": "true"}}}},
		{Name: "b", Workspace: "/tmp/ws", Stages: []StageConfig{{Name: "s", Type: "shell", Params: map[string]any{"sh": "true"}}}},
	}}
	if errs := Validate(cfg); len(errs) == 0 {
		t.Error("workspace 共享必须报错（独立锁看不见彼此，会互相清场）")
	}
}

func TestExtractTrigger(t *testing.T) {
	p := &PipelineConfig{Stages: []StageConfig{
		{Name: "拉取", Type: "git", Params: map[string]any{
			"url": "https://github.com/u/r.git", "branch": []any{"main", "release/*"}}},
		{Name: "构建", Type: "shell", Params: map[string]any{"sh": "make"}},
	}}
	if err := p.ExtractTrigger(); err != nil {
		t.Fatal(err)
	}
	if p.TriggerURL != "https://github.com/u/r.git" || len(p.TriggerBranches) != 2 {
		t.Fatalf("trigger 提取错误: %+v", p)
	}
	// 引用必须拒绝（webhook 匹配需要字面量）
	bad := &PipelineConfig{Stages: []StageConfig{
		{Name: "拉取", Type: "git", Params: map[string]any{"url": "${env.url}", "branch": "main"}}}}
	if err := bad.ExtractTrigger(); err == nil {
		t.Fatal("url 写引用必须报错")
	}
	// 无 git 节点：合法（仅手动 exec），TriggerURL 为空
	nogit := &PipelineConfig{Stages: []StageConfig{
		{Name: "构建", Type: "shell", Params: map[string]any{"sh": "make"}}}}
	if err := nogit.ExtractTrigger(); err != nil {
		t.Fatal(err)
	}
	if nogit.TriggerURL != "" {
		t.Fatal("无 git 节点不应有 TriggerURL")
	}
}

func TestLoadPipelinesYaml(t *testing.T) {
	yml := `
pipelines:
  - name: app
    workspace: /tmp/ws
    timeout: 45m
    env:
      JAVA_OPTS: -Xmx512m
    stages:
      - name: 拉取代码
        type: git
        params:
          url: "https://github.com/u/r.git"
          branch: ["main"]
      - name: 构建
        type: shell
        params:
          sh: |
            mvn package
        output:
          v: "${stdout}"
        skip: "${env.skipBuild}"
        when: failure
        timeout: 10m
`
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(yml), 0644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	p := cfg.Pipelines[0]
	if p.Name != "app" || len(p.Stages) != 2 || p.Env["JAVA_OPTS"] != "-Xmx512m" {
		t.Fatalf("解析错误: %+v", p)
	}
	if p.TriggerURL != "https://github.com/u/r.git" || p.TriggerBranches[0] != "main" {
		t.Fatalf("trigger 未提取: %+v", p)
	}
	st := p.Stages[1]
	if st.Params["sh"] != "mvn package\n" || st.Output["v"] != "${stdout}" || st.When != "failure" || st.Skip != "${env.skipBuild}" {
		t.Fatalf("stage 解析错误: %+v", st)
	}
	if d, err := st.TimeoutDuration(); err != nil || d != 10*time.Minute {
		t.Fatalf("stage timeout: %v %v", d, err)
	}
}
