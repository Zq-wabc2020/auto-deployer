package config

import (
	"strings"
	"testing"
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
		Services: []ServiceConfig{{Name: "test", Type: "jvm", Repo: RepoConfig{URL: "https://github.com/x/x.git", Branch: "main"}, Workspace: "/tmp", Build: BuildConfig{Command: Command{"true"}}}},
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
		Services: []ServiceConfig{{Name: "test", Type: "jvm", Repo: RepoConfig{URL: "https://github.com/x/x.git", Branch: "main"}, Workspace: "/tmp", Build: BuildConfig{Command: Command{"true"}}}},
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
		Services:      []ServiceConfig{{Name: "test", Type: "jvm", Repo: RepoConfig{URL: "https://github.com/x/x.git", Branch: "main"}, Workspace: "/tmp", Build: BuildConfig{Command: Command{"true"}}}},
		Notifications: NotificationConfig{To: nil},
		SMTP:          SMTPConfig{}, // empty, but to is empty so OK
	}
	errs := Validate(cfg)
	if len(errs) != 0 {
		t.Fatalf("expected 0 errors, got %d: %v", len(errs), errs)
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
