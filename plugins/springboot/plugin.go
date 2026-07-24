package springboot

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/auto-deployer/auto-deployer/internal/build"
	"github.com/auto-deployer/auto-deployer/internal/config"
	"github.com/auto-deployer/auto-deployer/internal/process"
)

// Plugin implements the Deployer interface for Spring Boot applications.
type Plugin struct{}

// New creates a new Spring Boot plugin instance.
func New() *Plugin {
	return &Plugin{}
}

// Type returns the plugin identifier.
func (p *Plugin) Type() string {
	return "springboot"
}

// Build executes git pull followed by the configured build command.
func (p *Plugin) Build(ctx context.Context, svc *config.ServiceConfig) error {
	if svc.Build.Command == "" {
		return fmt.Errorf("build command is empty")
	}

	if err := build.Pull(svc.Workspace, svc.Repo.Branch); err != nil {
		return fmt.Errorf("git pull failed: %w", err)
	}

	if err := build.ExecuteBuild(svc.Workspace, svc.Build.Command); err != nil {
		return err
	}

	fmt.Println("[springboot] build completed")
	return nil
}

// Start launches the configured run command and records its PID.
func (p *Plugin) Start(ctx context.Context, svc *config.ServiceConfig) error {
	pidFile := filepath.Join(daemonDir(), svc.Name+".pid")
	mgr := process.NewManager(pidFile)

	if mgr.Status() == "running" {
		return fmt.Errorf("service %s is already running", svc.Name)
	}

	parts := build.SplitCommand(svc.Run.Command)
	if len(parts) == 0 {
		return fmt.Errorf("run command is empty")
	}

	return mgr.Start(parts[0], parts[1:]...)
}

// Stop terminates the managed process.
func (p *Plugin) Stop(ctx context.Context, svc *config.ServiceConfig) error {
	pidFile := filepath.Join(daemonDir(), svc.Name+".pid")
	mgr := process.NewManager(pidFile)
	return mgr.Stop()
}

// Status returns the current status of the service.
func (p *Plugin) Status(ctx context.Context, svc *config.ServiceConfig) (string, error) {
	pidFile := filepath.Join(daemonDir(), svc.Name+".pid")
	mgr := process.NewManager(pidFile)
	return mgr.Status(), nil
}

func daemonDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".deployd", "run")
}
