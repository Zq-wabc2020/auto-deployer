package registry

import (
	"context"
	"io"
	"testing"

	"github.com/auto-deployer/auto-deployer/internal/config"
	"github.com/auto-deployer/auto-deployer/internal/deploy"
)

type fakeDeployer struct{}

func (fakeDeployer) Build(context.Context, *config.ServiceConfig) error            { return nil }
func (fakeDeployer) Stage(context.Context, *config.ServiceConfig) error            { return nil }
func (fakeDeployer) Status(context.Context, *config.ServiceConfig) (string, error) { return "stopped", nil }
func (fakeDeployer) SetOutput(io.Writer)                                           {}

func TestRegisterAndGet(t *testing.T) {
	Register("test-model", func() deploy.Deployer { return fakeDeployer{} })

	d, err := Get("test-model")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if d == nil {
		t.Fatal("expected non-nil deployer")
	}
}

func TestGetUnknown(t *testing.T) {
	if _, err := Get("does-not-exist"); err == nil {
		t.Fatal("expected error for unknown type")
	}
}

func TestTypesIncludesRegistered(t *testing.T) {
	Register("test-model-2", func() deploy.Deployer { return fakeDeployer{} })
	found := false
	for _, n := range Types() {
		if n == "test-model-2" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected test-model-2 in %v", Types())
	}
}
