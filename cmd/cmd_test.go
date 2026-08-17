package cmd

import (
	"os"
	"path/filepath"
	"testing"

	_ "github.com/auto-deployer/auto-deployer/plugins" // register built-in plugins for registry.Get
)

func TestRootCommand(t *testing.T) {
	if rootCmd.Use != "deployd" {
		t.Errorf("expected 'deployd', got %q", rootCmd.Use)
	}
	if rootCmd.Short == "" {
		t.Error("root command should have a Short description")
	}
}

func TestAllCommandsRegistered(t *testing.T) {
	expected := []string{"start", "stop", "status", "config", "logs", "deploy"}
	registered := make(map[string]bool)
	for _, c := range rootCmd.Commands() {
		registered[c.Name()] = true
	}
	for _, name := range expected {
		if !registered[name] {
			t.Errorf("expected command %q to be registered", name)
		}
	}
}

func TestAliases(t *testing.T) {
	deploy, _, err := rootCmd.Find([]string{"dep"})
	if err != nil || deploy.Name() != "deploy" {
		t.Errorf("alias `dep` should resolve to deploy, got %v err=%v", deploy, err)
	}
	svc, _, err := rootCmd.Find([]string{"svc"})
	if err != nil || svc.Name() != "service" {
		t.Errorf("alias `svc` should resolve to service, got %v err=%v", svc, err)
	}
}

func TestSvcShortFlags_Status(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	content := "services:\n  - name: \"flag-t1\"\n    type: jvm\n    repo: {url: \"x\", branch: main}\n    workspace: \"/tmp/flag-t1\"\n    build: {command: \"true\"}\n    deploy:\n      run: \"true\"\n"
	if err := os.WriteFile(cfgPath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	old := configFile
	configFile = cfgPath
	t.Cleanup(func() { configFile = old })

	rootCmd.SetArgs([]string{"svc", "flag-t1"})
	t.Cleanup(func() { rootCmd.SetArgs(nil) })
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("svc <name> status form failed: %v", err)
	}
}

func TestSvcShortFlags_UnknownService(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(cfgPath, []byte("services: []\n"), 0644); err != nil {
		t.Fatal(err)
	}
	old := configFile
	configFile = cfgPath
	t.Cleanup(func() { configFile = old })

	rootCmd.SetArgs([]string{"svc", "no-such-svc"})
	t.Cleanup(func() { rootCmd.SetArgs(nil) })
	if err := rootCmd.Execute(); err == nil {
		t.Fatal("expected error for unknown service")
	}
}
