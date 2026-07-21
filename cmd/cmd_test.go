package cmd

import (
	"testing"
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
