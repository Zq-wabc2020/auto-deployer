package static

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"time"

	"github.com/auto-deployer/auto-deployer/internal/build"
	"github.com/auto-deployer/auto-deployer/internal/config"
	"github.com/auto-deployer/auto-deployer/internal/deploy"
	"github.com/auto-deployer/auto-deployer/internal/registry"
)

// Plugin implements the static-site deployment model (Vue/React SPA, SSG):
// Build = build.command (npm run build), Stage = copy dist to the web-server
// docroot + nginx reload. There is NO per-service process, so the plugin does
// not implement Start/Stop -- `service start/stop/restart` is rejected by the
// orchestrator's capability check; redeploy with `deploy` instead.
// Status is determined by the configured health check URL.
type Plugin struct {
	output io.Writer
}

// New creates a new static plugin instance.
func New() *Plugin {
	return &Plugin{output: os.Stdout}
}

func init() {
	registry.Register("static", func() deploy.Deployer { return New() })
}

// Type returns the plugin identifier.
func (p *Plugin) Type() string {
	return "static"
}

// SetOutput redirects stdout/stderr to the given writer.
func (p *Plugin) SetOutput(w io.Writer) {
	p.output = w
}

// staticDeployConfig is the strategy-layer config parsed from svc.Deploy.
type staticDeployConfig struct {
	Artifact    config.Command `yaml:"artifact"`     // required: built files glob 或列表(如 ["dist/app.js","dist/index.html"])
	Dest        string         `yaml:"dest"`         // required: web server docroot
	NginxReload bool           `yaml:"nginx_reload"` // run `nginx -s reload` after copy
	// Health 字段已移除：改由中心层解析到 svc.HealthURL，Status 直接读取。
}

// deployConfig parses the service's deploy node into static-specific config.
func (p *Plugin) deployConfig(svc *config.ServiceConfig) staticDeployConfig {
	var dc staticDeployConfig
	if svc.Deploy.Kind != 0 { // zero node = no deploy: block
		if err := config.StrictDecodeDeploy(svc.Deploy, &dc); err != nil {
			fmt.Fprintf(p.output, "[static] warning: deploy 配置存在无法识别的字段(会被忽略,请检查是否用了其他模型的专属字段): %v\n", err)
		}
	}
	return dc
}

// Build executes the universal build command (typically `npm run build`).
func (p *Plugin) Build(ctx context.Context, svc *config.ServiceConfig) error {
	if svc.Build.Command.Empty() {
		return fmt.Errorf("build command is empty")
	}
	return build.ExecuteBuild(ctx, svc.Workspace, svc.Build.Command.String(), p.output)
}

// Stage copies the built static files to the web-server docroot and (optionally)
// reloads nginx. This is the activation itself -- there is no Start phase.
func (p *Plugin) Stage(ctx context.Context, svc *config.ServiceConfig) error {
	dc := p.deployConfig(svc)
	if dc.Artifact.Empty() || dc.Dest == "" {
		return fmt.Errorf("static 服务要求 deploy.artifact 和 deploy.dest（如 artifact: dist/*, dest: /usr/share/nginx/html/app）")
	}
	if _, err := build.CopyArtifact(ctx, svc.Workspace, dc.Artifact.Slice(), dc.Dest, p.output); err != nil {
		return fmt.Errorf("stage: %w", err)
	}
	if dc.NginxReload {
		cmd := exec.Command("sh", "-c", "nginx -s reload")
		cmd.Stdout = p.output
		cmd.Stderr = p.output
		if err := build.RunCommandCtx(ctx, cmd); err != nil {
			return fmt.Errorf("nginx reload failed: %w", err)
		}
		fmt.Fprintln(p.output, "[static] nginx reloaded")
	}
	fmt.Fprintf(p.output, "[static] staged %s to %s\n", dc.Artifact, dc.Dest)
	return nil
}

// Status probes the configured health URL: HTTP < 400 means running (the site
// is being served); without a health URL the status is unknown.
func (p *Plugin) Status(ctx context.Context, svc *config.ServiceConfig) (string, error) {
	health := svc.HealthURL
	if health == "" {
		return "unknown", nil // 不应发生（校验已拦），防御
	}
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(health)
	if err != nil {
		return "stopped", nil
	}
	defer resp.Body.Close()
	if resp.StatusCode < 400 {
		return "running", nil
	}
	return "stopped", nil
}
