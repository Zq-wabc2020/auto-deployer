package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestValidate_SMTPMissing(t *testing.T) {
	cfg := validPipeline()
	cfg.Notifications = NotificationConfig{To: []string{"a@b.com"}}
	cfg.SMTP = SMTPConfig{} // empty
	errs := Validate(cfg)
	if len(errs) != 1 {
		t.Fatalf("expected 1 error, got %d: %v", len(errs), errs)
	}
	if errs[0].Error() != "either smtp.host or resend.api_key is required when notifications.to is set" {
		t.Fatalf("unexpected error: %v", errs[0])
	}
}

func TestValidate_SMTPComplete(t *testing.T) {
	cfg := validPipeline()
	cfg.Notifications = NotificationConfig{To: []string{"a@b.com"}}
	cfg.SMTP = SMTPConfig{Host: "smtp.qq.com", Port: 465, Username: "x@qq.com", Token: "abc"}
	errs := Validate(cfg)
	if len(errs) != 0 {
		t.Fatalf("expected 0 errors, got %d: %v", len(errs), errs)
	}
}

func TestValidate_SMTPNotRequiredWhenNoTo(t *testing.T) {
	cfg := validPipeline()
	cfg.Notifications = NotificationConfig{To: nil}
	cfg.SMTP = SMTPConfig{} // empty, but to is empty so OK
	errs := Validate(cfg)
	if len(errs) != 0 {
		t.Fatalf("expected 0 errors, got %d: %v", len(errs), errs)
	}
}

// validPipeline 返回一个能通过基本校验的最小 pipeline 配置，供通知配置段测试复用。
func validPipeline() *AppConfig {
	return &AppConfig{
		Pipelines: []PipelineConfig{{
			Name:      "test",
			Workspace: "/tmp",
			Stages:    []StageConfig{{Name: "构建", Type: "shell", Params: map[string]any{"sh": "true"}}},
		}},
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
