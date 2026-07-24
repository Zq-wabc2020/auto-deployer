package config

import (
	"fmt"
)

var supportedTypes = map[string]bool{
	"springboot": true,
}

func Validate(cfg *AppConfig) []error {
	var errs []error
	for i, svc := range cfg.Services {
		prefix := fmt.Sprintf("services[%d]", i)
		if svc.Name == "" {
			errs = append(errs, fmt.Errorf("%s: name is required", prefix))
		}
		if !supportedTypes[svc.Type] {
			errs = append(errs, fmt.Errorf("%s: unknown type %q (supported: springboot)", prefix, svc.Type))
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
		if svc.Build.Command == "" {
			errs = append(errs, fmt.Errorf("%s: build.command is required", prefix))
		}
		if svc.Run.Command == "" {
			errs = append(errs, fmt.Errorf("%s: run.command is required", prefix))
		}
	}

	// Validate SMTP config when notifications.to is non-empty
	if len(cfg.Notifications.To) > 0 {
		if cfg.SMTP.Host == "" {
			errs = append(errs, fmt.Errorf("smtp.host is required when notifications.to is set"))
		}
		if cfg.SMTP.Port == 0 {
			errs = append(errs, fmt.Errorf("smtp.port is required when notifications.to is set"))
		}
		if cfg.SMTP.Username == "" {
			errs = append(errs, fmt.Errorf("smtp.username is required when notifications.to is set"))
		}
		if cfg.SMTP.Token == "" {
			errs = append(errs, fmt.Errorf("smtp.token is required when notifications.to is set"))
		}
	}

	return errs
}
