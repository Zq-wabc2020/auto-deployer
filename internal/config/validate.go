package config

import (
	"fmt"

	"github.com/auto-deployer/auto-deployer/internal/components"
)

var supportedTypes = map[string]bool{
	"jvm":        true,
	"springboot": true, // 兼容别名
	"docker":     true,
	"static":     true,
	"node":       true,
	"python":     true,
}

func Validate(cfg *AppConfig) []error {
	var errs []error
	for i, svc := range cfg.Services {
		prefix := fmt.Sprintf("services[%d]", i)
		if svc.Name == "" {
			errs = append(errs, fmt.Errorf("%s: name is required", prefix))
		}
		if !supportedTypes[svc.Type] {
			errs = append(errs, fmt.Errorf("%s: unknown type %q (supported: jvm)", prefix, svc.Type))
		}
		if svc.Repo.URL == "" {
			errs = append(errs, fmt.Errorf("%s: repo.url is required", prefix))
		}
		if svc.Repo.Branch == "" {
			errs = append(errs, fmt.Errorf("%s: repo.branch is required", prefix))
		}
		if svc.Workspace == "" {
			errs = append(errs, fmt.Errorf("%s: workspace is required", prefix))
		}
		if svc.Build.Command.Empty() {
			errs = append(errs, fmt.Errorf("%s: build.command is required", prefix))
		}
		if _, err := svc.TimeoutDuration(); err != nil {
			errs = append(errs, fmt.Errorf("%s: %v (e.g. \"45m\", \"90s\")", prefix, err))
		}
		// health 为通用必填字段（中心解析到 svc.HealthURL）：所有部署模型都需
		// HTTP 健康检查 URL 供 orchestrator 就绪轮询与 status 判定。
		if svc.HealthURL == "" {
			errs = append(errs, fmt.Errorf("%s %s: 缺少 deploy.health（就绪判定必需，请配置 HTTP 健康检查 URL）", prefix, svc.Name))
		}
		if _, err := svc.HealthIntervalDuration(); err != nil {
			errs = append(errs, fmt.Errorf("%s %s: %v", prefix, svc.Name, err))
		}
		// run.command 不再必填：已下沉为 deploy.run，由对应插件解析校验
	}

	// A workspace is a per-service checkout/build dir. Sharing one between
	// services would let their deploys (serialized by per-service locks, which
	// don't see each other) clobber each other, so duplicates are rejected.
	seenWorkspace := make(map[string]string) // workspace -> first service name
	for i, svc := range cfg.Services {
		if svc.Workspace == "" {
			continue
		}
		if prev, dup := seenWorkspace[svc.Workspace]; dup {
			errs = append(errs, fmt.Errorf("services[%d]: workspace %q is already used by service %q",
				i, svc.Workspace, prev))
		} else {
			seenWorkspace[svc.Workspace] = svc.Name
		}
	}

	known := components.Known()
	seenPipelineName := map[string]bool{}
	seenWs := map[string]string{}
	for i, p := range cfg.Pipelines {
		prefix := fmt.Sprintf("pipelines[%d]", i)
		if p.Name == "" {
			errs = append(errs, fmt.Errorf("%s: name is required", prefix))
		}
		if seenPipelineName[p.Name] {
			errs = append(errs, fmt.Errorf("%s: 工作项名 %q 重复", prefix, p.Name))
		}
		seenPipelineName[p.Name] = true
		if p.Workspace == "" {
			errs = append(errs, fmt.Errorf("%s: workspace is required", prefix))
		} else if prev, dup := seenWs[p.Workspace]; dup {
			// workspace 共享 = 两个独立锁看不见彼此的执行，会互相清场
			errs = append(errs, fmt.Errorf("%s: workspace %q 已被工作项 %q 使用", prefix, p.Workspace, prev))
		} else {
			seenWs[p.Workspace] = p.Name
		}
		if _, err := p.TimeoutDuration(); err != nil {
			errs = append(errs, fmt.Errorf("%s: %v (e.g. \"45m\")", prefix, err))
		}
		if len(p.Stages) == 0 {
			errs = append(errs, fmt.Errorf("%s: 至少需要一个 stage", prefix))
		}
		seenStage := map[string]bool{}
		for j, st := range p.Stages {
			sprefix := fmt.Sprintf("%s.stages[%d]", prefix, j)
			if st.Name == "" {
				errs = append(errs, fmt.Errorf("%s: name is required", sprefix))
			}
			if seenStage[st.Name] {
				errs = append(errs, fmt.Errorf("%s: stage 名 %q 重复（output 引用的命名空间）", sprefix, st.Name))
			}
			seenStage[st.Name] = true
			if !known[st.Type] {
				errs = append(errs, fmt.Errorf("%s: 未知组件类型 %q (支持: git/shell/email/cleanup)", sprefix, st.Type))
			}
			if st.When != "" && st.When != "failure" && st.When != "always" {
				errs = append(errs, fmt.Errorf("%s: when 只能是 failure/always，当前 %q", sprefix, st.When))
			}
			if _, err := st.TimeoutDuration(); err != nil {
				errs = append(errs, fmt.Errorf("%s: %v", sprefix, err))
			}
		}
	}

	// Validate notification config: need either SMTP or Resend
	if len(cfg.Notifications.To) > 0 {
		hasSMTP := cfg.SMTP.Host != ""
		hasResend := cfg.Resend.APIKey != ""
		if !hasSMTP && !hasResend {
			errs = append(errs, fmt.Errorf("either smtp.host or resend.api_key is required when notifications.to is set"))
		}
		if hasSMTP {
			if cfg.SMTP.Port == 0 {
				errs = append(errs, fmt.Errorf("smtp.port is required when smtp.host is set"))
			}
			if cfg.SMTP.Username == "" {
				errs = append(errs, fmt.Errorf("smtp.username is required when smtp.host is set"))
			}
			if cfg.SMTP.Token == "" {
				errs = append(errs, fmt.Errorf("smtp.token is required when smtp.host is set"))
			}
		}
		if hasResend && cfg.Resend.From == "" {
			errs = append(errs, fmt.Errorf("resend.from is required when resend.api_key is set"))
		}
	}

	return errs
}
