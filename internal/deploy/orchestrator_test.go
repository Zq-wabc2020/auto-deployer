package deploy

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/auto-deployer/auto-deployer/internal/config"
	"github.com/auto-deployer/auto-deployer/internal/deploylock"
	"github.com/auto-deployer/auto-deployer/internal/logger"
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
	t.Setenv("HOME", t.TempDir())
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))
	defer srv.Close()
	m := &mockDeployer{status: "running"}
	svc := &config.ServiceConfig{Name: "test", Type: "springboot", Workspace: "/tmp/test",
		HealthURL: srv.URL, HealthInterval: "10ms"}
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
	t.Setenv("HOME", t.TempDir())
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
	t.Setenv("HOME", t.TempDir())
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))
	defer srv.Close()
	m := &mockDeployer{status: "running"}
	svc := &config.ServiceConfig{Name: "test", Type: "springboot", Workspace: "/tmp/test",
		HealthURL: srv.URL, HealthInterval: "10ms"}
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

// fakeStartableDeployer 实现 Deployer+Startable+Stoppable 全接口：
// Build/Stage no-op；Start 置 started；Status 由 started 派生（running/stopped）。
// 供 Deploy 就绪门控与 readinessGate 单测复用（T7/T8 同名同结构）。
type fakeStartableDeployer struct {
	started bool
	status string // 预留：T7/T8 复用；当前 Status 由 started 派生
}

func (f *fakeStartableDeployer) Build(context.Context, *config.ServiceConfig) error  { return nil }
func (f *fakeStartableDeployer) Stage(context.Context, *config.ServiceConfig) error  { return nil }
func (f *fakeStartableDeployer) Start(context.Context, *config.ServiceConfig) error  { f.started = true; return nil }
func (f *fakeStartableDeployer) Stop(context.Context, *config.ServiceConfig) error   { f.started = false; return nil }
func (f *fakeStartableDeployer) Status(context.Context, *config.ServiceConfig) (string, error) {
	if f.started {
		return "running", nil
	}
	return "stopped", nil
}
func (f *fakeStartableDeployer) SetOutput(io.Writer) {}

// setupBareRepo 构造一个含单次提交的本地 bare git 仓库（branch=main），
// 供 Deploy 的真实 fetch 路径使用（隔离外部网络）。复用 build/git_test.go 模式。
func setupBareRepo(t *testing.T) string {
	t.Helper()
	setupDir := t.TempDir()
	runGit(t, setupDir, "init", "-b", "main")
	runGit(t, setupDir, "config", "user.email", "test@test.com")
	runGit(t, setupDir, "config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(setupDir, "README.md"), []byte("hello"), 0644); err != nil {
		t.Fatal(err)
	}
	runGit(t, setupDir, "add", ".")
	runGit(t, setupDir, "commit", "-m", "init")
	bareDir := t.TempDir()
	runGit(t, setupDir, "clone", "--bare", setupDir, bareDir)
	return bareDir
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v in %s failed: %v\n%s", args, dir, err, out)
	}
}

// TestDeployFailsWhenHealthNeverPasses 验证：health 持续 500 时，readinessGate
// 轮询至 deployCtx 超时 → handleErr 走失败路径（start_failed + 停半起 + 邮件）。
func TestDeployFailsWhenHealthNeverPasses(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
	}))
	defer srv.Close()

	svc := &config.ServiceConfig{
		Name: "s1", Type: "jvm", Workspace: t.TempDir(),
		Repo:           config.RepoConfig{URL: setupBareRepo(t), Branch: "main"},
		HealthURL:      srv.URL,
		HealthInterval: "20ms",
		Timeout:        "200ms", // 快速超时，使 readiness 必然失败
		Build:          config.BuildConfig{Command: config.Command{"true"}},
	}
	d := &fakeStartableDeployer{started: true, status: "running"}
	res, err := Deploy(context.Background(), svc, &config.AppConfig{}, d, nil)
	if err == nil || res.Status != "failed" {
		t.Fatalf("expected failed, got %+v err=%v", res, err)
	}
	st, _ := servstate.Read("s1")
	if st.Status != "start_failed" {
		t.Fatalf("state should be start_failed, got %q", st.Status)
	}
}

// TestDeploySucceedsWhenHealthPasses 验证：health 返回 200 时 readinessGate 即刻通过，
// Deploy 成功并清除 .state，且确实探测过 health 端点（证明门控被执行）。
func TestDeploySucceedsWhenHealthPasses(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.WriteHeader(200)
	}))
	defer srv.Close()

	svc := &config.ServiceConfig{
		Name: "s2", Type: "jvm", Workspace: t.TempDir(),
		Repo:           config.RepoConfig{URL: setupBareRepo(t), Branch: "main"},
		HealthURL:      srv.URL,
		HealthInterval: "10ms",
		Build:          config.BuildConfig{Command: config.Command{"true"}},
	}
	d := &fakeStartableDeployer{started: true, status: "running"}
	res, err := Deploy(context.Background(), svc, &config.AppConfig{}, d, nil)
	if err != nil || res.Status != "success" {
		t.Fatalf("expected success, got %+v err=%v", res, err)
	}
	if atomic.LoadInt32(&hits) == 0 {
		t.Fatal("readiness gate should have probed the health endpoint")
	}
	if _, ok := servstate.Read("s2"); ok {
		t.Fatal("success should clear .state")
	}
}

// TestReadinessGateFastFailOnStoppedProcess 验证 readinessGate 的进程存活快速失败：
// deployer.Status 返回 stopped 时，无需等待 health 轮询即返回错误（health 端点不可达亦不触达）。
func TestReadinessGateFastFailOnStoppedProcess(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	svc := &config.ServiceConfig{
		Name: "rf", Type: "jvm",
		HealthURL:      "http://127.0.0.1:0/health", // 不可达；fast-fail 先行返回，不触达
		HealthInterval: "10ms",
	}
	d := &fakeStartableDeployer{started: false} // Status → "stopped"
	err := readinessGate(context.Background(), svc, d, logger.GetServiceLogger("rf"))
	if err == nil || !strings.Contains(err.Error(), "立即退出") {
		t.Fatalf("expected fast-fail stopped error, got %v", err)
	}
}

// TestServiceStartWritesStartingThenClearsOnHealth 验证 ServiceStart 在 health 通过后
// 清除 starting 状态（成功后无 .state）。
func TestServiceStartWritesStartingThenClearsOnHealth(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))
	defer srv.Close()
	svc := &config.ServiceConfig{Name: "ss", Type: "jvm", HealthURL: srv.URL, HealthInterval: "10ms"}
	d := &fakeStartableDeployer{}
	if err := ServiceStart(context.Background(), svc, d); err != nil {
		t.Fatalf("ServiceStart: %v", err)
	}
	if _, ok := servstate.Read("ss"); ok {
		t.Fatal("health pass 后应清除 starting state")
	}
}

// TestServiceStartFailedWritesStartFailed 验证 health 持续失败时 ServiceStart 写 start_failed。
func TestServiceStartFailedWritesStartFailed(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
	}))
	defer srv.Close()
	svc := &config.ServiceConfig{Name: "sf", Type: "jvm", HealthURL: srv.URL, HealthInterval: "10ms", Timeout: "50ms"}
	d := &fakeStartableDeployer{started: true, status: "running"}
	err := ServiceStart(context.Background(), svc, d)
	if err == nil {
		t.Fatal("expected failure")
	}
	st, _ := servstate.Read("sf")
	if st.Status != "start_failed" {
		t.Fatalf("got %q", st.Status)
	}
}

// TestServiceStopClearsState 验证 ServiceStop 清除粘性的 start_failed → stopped。
func TestServiceStopClearsState(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	_ = servstate.WriteFailed("sx")
	if err := ServiceStop(context.Background(), &config.ServiceConfig{Name: "sx"}, &fakeStartableDeployer{}); err != nil {
		t.Fatalf("ServiceStop: %v", err)
	}
	if _, ok := servstate.Read("sx"); ok {
		t.Fatal("stop 应清除 start_failed")
	}
}
