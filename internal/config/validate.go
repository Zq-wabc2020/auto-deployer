package config

import (
	"fmt"
)

var supportedTypes = map[string]bool{
	"jvm":        true,
	"springboot": true, // 兼容别名
	"docker":     true,
	"static":     true,
	"node":       true,
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
		// run.command 不再必填：已下沉为 deploy.run，由对应插件解析校验
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
