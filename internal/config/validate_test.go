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
				Type:      "springboot",
				Repo:      RepoConfig{URL: "https://github.com/u/r.git", Branch: "main"},
				Workspace: "/tmp/app",
				Build:     BuildConfig{Command: "mvn package"},
				Run:       RunConfig{Command: "java -jar target/app.jar"},
			},
		},
	}

	errs := Validate(cfg)
	if len(errs) != 0 {
		t.Errorf("expected no errors, got %v", errs)
	}
}

func TestValidate_MissingName(t *testing.T) {
	cfg := &AppConfig{
		Services: []ServiceConfig{{Type: "springboot"}},
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
		Services: []ServiceConfig{{Name: "app", Type: "springboot"}},
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
			Type: "springboot",
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
			Type:      "springboot",
			Repo:      RepoConfig{URL: "https://github.com/u/r.git", Branch: "main"},
			Workspace: "/tmp/app",
			Run:       RunConfig{Command: "java -jar app.jar"},
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

func TestValidate_MissingRunCommand(t *testing.T) {
	cfg := &AppConfig{
		Services: []ServiceConfig{{
			Name:      "app",
			Type:      "springboot",
			Repo:      RepoConfig{URL: "https://github.com/u/r.git", Branch: "main"},
			Workspace: "/tmp/app",
			Build:     BuildConfig{Command: "mvn package"},
		}},
	}

	errs := Validate(cfg)
	found := false
	for _, e := range errs {
		if strings.Contains(e.Error(), "run.command") {
			found = true
		}
	}
	if !found {
		t.Error("expected error mentioning 'run.command'")
	}
}

func TestValidate_MultipleServices(t *testing.T) {
	cfg := &AppConfig{
		Services: []ServiceConfig{
			{
				Name:      "app1",
				Type:      "springboot",
				Repo:      RepoConfig{URL: "https://github.com/u/r1.git", Branch: "main"},
				Workspace: "/tmp/app1",
				Build:     BuildConfig{Command: "mvn package"},
				Run:       RunConfig{Command: "java -jar app1.jar"},
			},
			{
				Name:      "",
				Type:      "unknown",
				Repo:      RepoConfig{},
				Workspace: "",
				Build:     BuildConfig{},
				Run:       RunConfig{},
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
		Services: []ServiceConfig{{Name: "test", Type: "springboot", Repo: RepoConfig{URL: "https://github.com/x/x.git", Branch: "main"}, Workspace: "/tmp", Build: BuildConfig{Command: "true"}, Run: RunConfig{Command: "true"}}},
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
		Services: []ServiceConfig{{Name: "test", Type: "springboot", Repo: RepoConfig{URL: "https://github.com/x/x.git", Branch: "main"}, Workspace: "/tmp", Build: BuildConfig{Command: "true"}, Run: RunConfig{Command: "true"}}},
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
		Services:      []ServiceConfig{{Name: "test", Type: "springboot", Repo: RepoConfig{URL: "https://github.com/x/x.git", Branch: "main"}, Workspace: "/tmp", Build: BuildConfig{Command: "true"}, Run: RunConfig{Command: "true"}}},
		Notifications: NotificationConfig{To: nil},
		SMTP:          SMTPConfig{}, // empty, but to is empty so OK
	}
	errs := Validate(cfg)
	if len(errs) != 0 {
		t.Fatalf("expected 0 errors, got %d: %v", len(errs), errs)
	}
}
