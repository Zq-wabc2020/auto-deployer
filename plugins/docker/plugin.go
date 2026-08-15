package docker

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strings"

	"github.com/auto-deployer/auto-deployer/internal/build"
	"github.com/auto-deployer/auto-deployer/internal/config"
	"github.com/auto-deployer/auto-deployer/internal/deploy"
	"github.com/auto-deployer/auto-deployer/internal/registry"
)

// Plugin implements the container deployment model:
// Build = universal build.command (typically `docker build -t img .`),
// Stage = no-op (the image stays in the local docker daemon),
// Start = docker run -d, Stop = docker stop + rm, Status = docker ps.
type Plugin struct {
	output io.Writer
}

// New creates a new docker plugin instance.
func New() *Plugin {
	return &Plugin{output: os.Stdout}
}

func init() {
	registry.Register("docker", func() deploy.Deployer { return New() })
}

// Type returns the plugin identifier.
func (p *Plugin) Type() string {
	return "docker"
}

// SetOutput redirects stdout/stderr to the given writer.
func (p *Plugin) SetOutput(w io.Writer) {
	p.output = w
}

// dockerDeployConfig is the strategy-layer config parsed from svc.Deploy.
type dockerDeployConfig struct {
	Image     string            `yaml:"image"`     // required: image to run
	Container string            `yaml:"container"` // container name; defaults to service name
	Ports     []string          `yaml:"ports"`     // -p mappings, e.g. "8000:8000"
	Env       map[string]string `yaml:"env"`       // -e variables
	Volumes   []string          `yaml:"volumes"`   // -v mounts
	Args      []string          `yaml:"args"`      // extra `docker run` args
}

// deployConfig parses the service's deploy node into docker-specific config.
func (p *Plugin) deployConfig(svc *config.ServiceConfig) dockerDeployConfig {
	var dc dockerDeployConfig
	if svc.Deploy.Kind != 0 { // zero node = no deploy: block
		if err := svc.Deploy.Decode(&dc); err != nil {
			fmt.Fprintf(p.output, "[docker] warning: failed to parse deploy config: %v\n", err)
		}
	}
	return dc
}

func (p *Plugin) containerName(svc *config.ServiceConfig) string {
	if n := p.deployConfig(svc).Container; n != "" {
		return n
	}
	return svc.Name
}

// Build executes the universal build command (typically `docker build`).
func (p *Plugin) Build(ctx context.Context, svc *config.ServiceConfig) error {
	if svc.Build.Command.Empty() {
		return fmt.Errorf("build command is empty")
	}
	return build.ExecuteBuild(svc.Workspace, svc.Build.Command.String(), p.output)
}

// Stage is a no-op: the image built by Build already lives in the local docker
// daemon, which is where `docker run` will find it.
func (p *Plugin) Stage(ctx context.Context, svc *config.ServiceConfig) error {
	return nil
}

// Start runs the container detached.
func (p *Plugin) Start(ctx context.Context, svc *config.ServiceConfig) error {
	dc := p.deployConfig(svc)
	if dc.Image == "" {
		return fmt.Errorf("deploy.image is required")
	}
	name := p.containerName(svc)
	if st, _ := p.Status(ctx, svc); st == "running" {
		return fmt.Errorf("container %s is already running", name)
	}
	// Remove a stale stopped container so the name is free for the new run.
	if _, err := exec.Command("docker", "rm", name).CombinedOutput(); err == nil {
		fmt.Fprintf(p.output, "[docker] removed stale container %s\n", name)
	}

	args := append([]string{"run", "-d", "--name", name}, runArgs(dc)...)
	args = append(args, dc.Image)

	fmt.Fprintf(p.output, "[docker] docker %s\n", strings.Join(args, " "))
	out, err := exec.Command("docker", args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("docker run failed: %w: %s", err, strings.TrimSpace(string(out)))
	}
	fmt.Fprintf(p.output, "[docker] container %s started (%s)\n", name, firstLine(string(out)))
	return nil
}

// runArgs builds the -p/-e/-v flags from config. Env keys are sorted so the
// generated command is deterministic.
func runArgs(dc dockerDeployConfig) []string {
	var args []string
	for _, pt := range dc.Ports {
		args = append(args, "-p", pt)
	}
	keys := make([]string, 0, len(dc.Env))
	for k := range dc.Env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		args = append(args, "-e", k+"="+dc.Env[k])
	}
	for _, v := range dc.Volumes {
		args = append(args, "-v", v)
	}
	args = append(args, dc.Args...)
	return args
}

// Stop stops and removes the container (rm frees the name for the next run).
func (p *Plugin) Stop(ctx context.Context, svc *config.ServiceConfig) error {
	name := p.containerName(svc)
	if st, _ := p.Status(ctx, svc); st != "running" {
		fmt.Fprintf(p.output, "[docker] container %s not running\n", name)
		return nil
	}
	if out, err := exec.Command("docker", "stop", name).CombinedOutput(); err != nil {
		return fmt.Errorf("docker stop failed: %w: %s", err, strings.TrimSpace(string(out)))
	}
	if _, err := exec.Command("docker", "rm", name).CombinedOutput(); err != nil {
		fmt.Fprintf(p.output, "[docker] warning: docker rm failed: %v\n", err)
	}
	fmt.Fprintf(p.output, "[docker] container %s stopped\n", name)
	return nil
}

// Status reports whether the container is running.
func (p *Plugin) Status(ctx context.Context, svc *config.ServiceConfig) (string, error) {
	name := p.containerName(svc)
	out, err := exec.Command("docker", "ps", "--filter", "name=^/"+name+"$",
		"--filter", "status=running", "--format", "{{.Names}}").Output()
	if err != nil {
		return "unknown", err
	}
	if strings.TrimSpace(string(out)) == name {
		return "running", nil
	}
	return "stopped", nil
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return strings.TrimSpace(s)
}
