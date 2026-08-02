package deploy

import (
	"context"
	"testing"

	"github.com/auto-deployer/auto-deployer/internal/config"
)

type mockDeployer struct {
	built    bool
	started  bool
	stopped  bool
	status   string
	buildErr error
	startErr error
}

func (m *mockDeployer) Build(ctx context.Context, svc *config.ServiceConfig) error {
	m.built = true
	return m.buildErr
}
func (m *mockDeployer) Start(ctx context.Context, svc *config.ServiceConfig) error {
	m.started = true
	return m.startErr
}
func (m *mockDeployer) Stop(ctx context.Context, svc *config.ServiceConfig) error {
	m.stopped = true
	return nil
}
func (m *mockDeployer) Status(ctx context.Context, svc *config.ServiceConfig) (string, error) {
	return m.status, nil
}

func TestServiceStart(t *testing.T) {
	m := &mockDeployer{}
	svc := &config.ServiceConfig{Name: "test", Type: "springboot", Workspace: "/tmp/test"}
	err := ServiceStart(context.Background(), svc, m)
	if err != nil {
		t.Fatal(err)
	}
	if !m.started {
		t.Error("expected Start to be called")
	}
	if m.built || m.stopped {
		t.Error("expected Build/Stop NOT to be called")
	}
}

func TestServiceStop(t *testing.T) {
	m := &mockDeployer{}
	svc := &config.ServiceConfig{Name: "test", Type: "springboot", Workspace: "/tmp/test"}
	err := ServiceStop(context.Background(), svc, m)
	if err != nil {
		t.Fatal(err)
	}
	if !m.stopped {
		t.Error("expected Stop to be called")
	}
	if m.built || m.started {
		t.Error("expected Build/Start NOT to be called")
	}
}

func TestServiceRestart(t *testing.T) {
	m := &mockDeployer{}
	svc := &config.ServiceConfig{Name: "test", Type: "springboot", Workspace: "/tmp/test"}
	err := ServiceRestart(context.Background(), svc, m)
	if err != nil {
		t.Fatal(err)
	}
	if !m.stopped {
		t.Error("expected Stop to be called")
	}
	if !m.started {
		t.Error("expected Start to be called")
	}
	if m.built {
		t.Error("expected Build NOT to be called")
	}
}
