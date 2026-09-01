package node

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/auto-deployer/auto-deployer/internal/build"
	"github.com/auto-deployer/auto-deployer/internal/config"
	"github.com/auto-deployer/auto-deployer/internal/deploy"
	"github.com/auto-deployer/auto-deployer/internal/process"
	"github.com/auto-deployer/auto-deployer/internal/registry"
)

// Plugin implements the long-running Node process deployment model
// (Next.js SSR, Nuxt SSR, Express, NestJS): Build = build.command,
// Stage = no-op (the bundle runs in place), Start/Stop/Status = PID-managed.
type Plugin struct {
	output io.Writer
}

// New creates a new node plugin instance.
func New() *Plugin {
	return &Plugin{output: os.Stdout}
}

func init() {
	registry.Register("node", func() deploy.Deployer { return New() })
}

// Type returns the plugin identifier.
func (p *Plugin) Type() string {
	return "node"
}

// SetOutput redirects stdout/stderr to the given writer.
func (p *Plugin) SetOutput(w io.Writer) {
	p.output = w
}

// nodeDeployConfig is the strategy-layer config parsed from svc.Deploy.
type nodeDeployConfig struct {
	Artifact string            `yaml:"artifact"` // 可选：产物目录/glob(如 .output)。不填=源码即产物(原地启动)
	Dest     string            `yaml:"dest"`     // 可选：归位目录。填了才拷贝，run 也在该目录执行
	Run      string            `yaml:"run"`      // 纯启动命令，如 node server.js，不含 nohup/&
	Env      map[string]string `yaml:"env"`      // 运行时环境变量
}

// deployConfig parses the service's deploy node into node-specific config.
func (p *Plugin) deployConfig(svc *config.ServiceConfig) nodeDeployConfig {
	var dc nodeDeployConfig
	if svc.Deploy.Kind != 0 { // zero node = no deploy: block
		if err := config.StrictDecodeDeploy(svc.Deploy, &dc); err != nil {
			fmt.Fprintf(p.output, "[node] warning: deploy 配置存在无法识别的字段(会被忽略,请检查是否用了其他模型的专属字段): %v\n", err)
		}
	}
	return dc
}

func pidFileFor(name string) string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".deployd", "run", name+".pid")
}

// Build executes the universal build command (typically `npm run build`).
func (p *Plugin) Build(ctx context.Context, svc *config.ServiceConfig) error {
	if svc.Build.Command.Empty() {
		return fmt.Errorf("build command is empty")
	}
	return build.ExecuteBuild(ctx, svc.Workspace, svc.Build.Command.String(), p.output)
}

// Stage copies the built bundle to its deploy directory when artifact/dest are
// configured (run then executes from dest, decoupling the running server from
// the next build's workspace churn). Without artifact it is a no-op: the
// bundle runs in place (source-is-artifact, same as python). The workspace is
// NOT cleaned -- node_modules must survive for incremental rebuilds.
func (p *Plugin) Stage(ctx context.Context, svc *config.ServiceConfig) error {
	dc := p.deployConfig(svc)
	if dc.Artifact == "" {
		return nil // source-is-artifact: run in place
	}
	dest := dc.Dest
	if dest == "" {
		dest = svc.Workspace
	}
	if _, err := build.CopyArtifact(svc.Workspace, dc.Artifact, dest, p.output); err != nil {
		return fmt.Errorf("stage: %w", err)
	}
	fmt.Fprintf(p.output, "[node] staged artifact to %s\n", dest)
	return nil
}

// Start launches the run command via the shared PID-managed shell starter.
// It runs from dest when artifact+dest were staged, else from the workspace.
func (p *Plugin) Start(ctx context.Context, svc *config.ServiceConfig) error {
	dc := p.deployConfig(svc)
	if dc.Run == "" {
		return fmt.Errorf("run command is empty (configure deploy.run)")
	}
	runDir := svc.Workspace
	if dc.Artifact != "" && dc.Dest != "" {
		runDir = dc.Dest
	}
	mgr := process.NewManager(pidFileFor(svc.Name))
	return mgr.StartShell(runDir, dc.Run, dc.Env, p.output)
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
