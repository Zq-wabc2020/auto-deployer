package python

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/auto-deployer/auto-deployer/internal/build"
	"github.com/auto-deployer/auto-deployer/internal/config"
	"github.com/auto-deployer/auto-deployer/internal/deploy"
	"github.com/auto-deployer/auto-deployer/internal/process"
	"github.com/auto-deployer/auto-deployer/internal/registry"
)

// Plugin implements the Python long-running service model (FastAPI/Flask/
// Django via ASGI/WSGI): Build = build.command (create venv + install deps),
// Stage = optional migrate command (deploy-only, never on restart),
// Start/Stop/Status = PID-managed. The source tree is the artifact, so the
// workspace is NOT cleaned between deploys (keeps .venv for incremental
// installs -- see design doc §13.3).
type Plugin struct {
	output io.Writer
}

// New creates a new python plugin instance.
func New() *Plugin {
	return &Plugin{output: os.Stdout}
}

func init() {
	registry.Register("python", func() deploy.Deployer { return New() })
}

// Type returns the plugin identifier.
func (p *Plugin) Type() string {
	return "python"
}

// SetOutput redirects stdout/stderr to the given writer.
func (p *Plugin) SetOutput(w io.Writer) {
	p.output = w
}

// pythonDeployConfig is the strategy-layer config parsed from svc.Deploy.
type pythonDeployConfig struct {
	Venv    string            `yaml:"venv"`    // 可选：虚拟环境目录(如 .venv)，其 bin 前置到 PATH
	Migrate string            `yaml:"migrate"` // 可选：部署专属迁移命令(如 alembic upgrade head)
	Run     string            `yaml:"run"`     // 纯启动命令，不含 nohup/&
	Env     map[string]string `yaml:"env"`     // 运行时环境变量
}

// deployConfig parses the service's deploy node into python-specific config.
func (p *Plugin) deployConfig(svc *config.ServiceConfig) pythonDeployConfig {
	var dc pythonDeployConfig
	if svc.Deploy.Kind != 0 { // zero node = no deploy: block
		if err := config.StrictDecodeDeploy(svc.Deploy, &dc); err != nil {
			fmt.Fprintf(p.output, "[python] warning: deploy 配置存在无法识别的字段(会被忽略,请检查是否用了其他模型的专属字段): %v\n", err)
		}
	}
	return dc
}

func pidFileFor(name string) string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".deployd", "run", name+".pid")
}

// Build executes the universal build command (typically venv creation +
// dependency install, e.g. ["python3.11 -m venv .venv", ".venv/bin/pip install ."]).
func (p *Plugin) Build(ctx context.Context, svc *config.ServiceConfig) error {
	if svc.Build.Command.Empty() {
		return fmt.Errorf("build command is empty")
	}
	return build.ExecuteBuild(ctx, svc.Workspace, svc.Build.Command.String(), p.output)
}

// Stage runs the deploy-only migrate command if configured. Skipped on
// restart by the orchestrator -- this is exactly what keeps migrations from
// re-running on `service restart`.
func (p *Plugin) Stage(ctx context.Context, svc *config.ServiceConfig) error {
	dc := p.deployConfig(svc)
	if dc.Migrate == "" {
		return nil
	}
	cmd := exec.Command("sh", "-c", dc.Migrate)
	cmd.Dir = svc.Workspace
	cmd.Stdout = p.output
	cmd.Stderr = p.output
	if len(dc.Env) > 0 {
		cmd.Env = build.MergeEnv(os.Environ(), dc.Env)
	}
	fmt.Fprintf(p.output, "[python] migrate: %s\n", dc.Migrate)
	if err := build.RunCommandCtx(ctx, cmd); err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("migrate aborted: %w", ctx.Err())
		}
		return fmt.Errorf("migrate failed: %w", err)
	}
	fmt.Fprintln(p.output, "[python] migrate completed")
	return nil
}

// Start launches the run command via the shared PID-managed shell starter.
// When venv is configured its bin/ is prepended to PATH so the run command
// can use bare names (`uvicorn ...` instead of `.venv/bin/uvicorn ...`).
func (p *Plugin) Start(ctx context.Context, svc *config.ServiceConfig) error {
	dc := p.deployConfig(svc)
	if dc.Run == "" {
		return fmt.Errorf("run command is empty (configure deploy.run)")
	}
	overrides := map[string]string{}
	if dc.Venv != "" {
		venvBin := filepath.Join(svc.Workspace, dc.Venv, "bin")
		overrides["PATH"] = venvBin + string(os.PathListSeparator) + os.Getenv("PATH")
		overrides["VIRTUAL_ENV"] = filepath.Join(svc.Workspace, dc.Venv)
	}
	for k, v := range dc.Env {
		overrides[k] = v
	}
	mgr := process.NewManager(pidFileFor(svc.Name))
	return mgr.StartShell(svc.Workspace, dc.Run, overrides, p.output)
}

// Stop terminates the managed process.
func (p *Plugin) Stop(ctx context.Context, svc *config.ServiceConfig) error {
	mgr := process.NewManager(pidFileFor(svc.Name))
	mgr.SetOutput(p.output)
	return mgr.Stop()
}

// Status returns the current status of the service.
func (p *Plugin) Status(ctx context.Context, svc *config.ServiceConfig) (string, error) {
	mgr := process.NewManager(pidFileFor(svc.Name))
	return mgr.Status(), nil
}
