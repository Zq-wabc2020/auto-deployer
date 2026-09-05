package deploy

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/auto-deployer/auto-deployer/internal/config"
	"github.com/auto-deployer/auto-deployer/internal/deploylock"
	"github.com/auto-deployer/auto-deployer/internal/servstate"
)

type mockDeployer struct {
	built    bool
	staged   bool
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

func (m *mockDeployer) Stage(ctx context.Context, svc *config.ServiceConfig) error {
	m.staged = true
	return nil
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
func (m *mockDeployer) SetOutput(w io.Writer) {}

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

func TestGetServiceStatusRichStarting(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	svc := &config.ServiceConfig{Name: "s1", Type: "jvm"}
	// 占锁 + 写 starting
	lock, _ := deploylock.Acquire("s1")
	defer lock.Release()
	_ = servstate.WriteStarting("s1", "build")
	got, _ := GetServiceStatusRich(context.Background(), svc, &fakeDeployer{})
	if !strings.HasSuffix(got, "starting") && !strings.Contains(got, "starting") {
		t.Fatalf("expected starting, got %q", got)
	}
}

func TestGetServiceStatusRichStaleStartingBecomesFailed(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	_ = servstate.WriteStarting("s1", "build") // 但不持锁 → 陈旧
	got, _ := GetServiceStatusRich(context.Background(), &config.ServiceConfig{Name: "s1"}, &fakeDeployer{})
	if !strings.Contains(got, "start_failed") {
		t.Fatalf("stale starting should recover to start_failed, got %q", got)
	}
	// 且应就地重写为 start_failed
	st, _ := servstate.Read("s1")
	if st.Status != "start_failed" {
		t.Fatalf("stale should be rewritten to start_failed, got %q", st.Status)
	}
}

func TestGetServiceStatusRichRunningFallbackToProbe(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	// 无 .state → 走插件 Status
	d := &fakeDeployer{status: "running"}
	got, _ := GetServiceStatusRich(context.Background(), &config.ServiceConfig{Name: "s1"}, d)
	if !strings.Contains(got, "running") {
		t.Fatalf("expected running from probe, got %q", got)
	}
}

// TestGetServiceStatusRichStartFailedSticky 验证 §4.4 不变式：当 .state=start_failed 时，
// 即便实时探测本应返回 running，也必须返回粘性的 start_failed，且根本不查探测。
func TestGetServiceStatusRichStartFailedSticky(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	_ = servstate.WriteFailed("s1")
	d := &fakeDeployer{status: "running"}
	got, err := GetServiceStatusRich(context.Background(), &config.ServiceConfig{Name: "s1"}, d)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "start_failed") {
		t.Fatalf("sticky start_failed must win over a running probe, got %q", got)
	}
	if d.probed {
		t.Fatal("Status() must not be called when .state=start_failed (sticky)")
	}
}

// fakeDeployer 实现 Deployer，Status 可控。probed 标记 Status 是否被调用过，
// 供粘性不变式测试断言“start_failed 时不查实时探测”。
type fakeDeployer struct {
	status    string
	probed    bool
	startable bool
	started   bool
}

func (f *fakeDeployer) Build(context.Context, *config.ServiceConfig) error { return nil }
func (f *fakeDeployer) Stage(context.Context, *config.ServiceConfig) error { return nil }
func (f *fakeDeployer) Status(context.Context, *config.ServiceConfig) (string, error) {
	f.probed = true
	if f.status == "" {
		return "stopped", nil
	}
	return f.status, nil
}
func (f *fakeDeployer) SetOutput(io.Writer) {}
