package springboot

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/auto-deployer/auto-deployer/internal/build"
	"github.com/auto-deployer/auto-deployer/internal/config"
	"github.com/auto-deployer/auto-deployer/internal/deploy"
	"github.com/auto-deployer/auto-deployer/internal/process"
	"github.com/auto-deployer/auto-deployer/internal/registry"
)

// Plugin implements the Deployer interface for Spring Boot / JVM applications.
type Plugin struct {
	output io.Writer
}

// New creates a new Spring Boot plugin instance.
func New() *Plugin {
	return &Plugin{output: os.Stdout}
}

func init() {
	registry.Register("jvm", func() deploy.Deployer { return New() })
	registry.Register("springboot", func() deploy.Deployer { return New() })
}

// Type returns the plugin identifier.
func (p *Plugin) Type() string {
	return "springboot"
}

// SetOutput redirects stdout/stderr to the given writer.
func (p *Plugin) SetOutput(w io.Writer) {
	p.output = w
}

// jvmDeployConfig is the strategy-layer config parsed from svc.Deploy.
type jvmDeployConfig struct {
	Artifact string            `yaml:"artifact"` // 产物 glob，如 target/*.jar；不填=原地启动
	Dest     string            `yaml:"dest"`     // 归位目录；不填=workspace 根
	Run      string            `yaml:"run"`      // 纯启动命令，不含 nohup/&
	Env      map[string]string `yaml:"env"`      // 运行时环境变量
}

// deployConfig parses the service's deploy node into jvm-specific config.
func (p *Plugin) deployConfig(svc *config.ServiceConfig) jvmDeployConfig {
	var dc jvmDeployConfig
	if svc.Deploy != nil {
		_ = svc.Deploy.Decode(&dc)
	}
	return dc
}

// Build executes the configured build command (shell). Artifact placement and
// workspace cleanup are Stage's job, not Build's.
func (p *Plugin) Build(ctx context.Context, svc *config.ServiceConfig) error {
	if svc.Build.Command.Empty() {
		return fmt.Errorf("build command is empty")
	}
	if err := build.ExecuteBuild(svc.Workspace, svc.Build.Command.String(), p.output); err != nil {
		return err
	}
	fmt.Fprintln(p.output, "[springboot] build completed")
	return nil
}

// Stage is deploy-only preparation (skipped on restart): copy the built artifact
// to its deploy directory (if configured) and clean the workspace. With no
// artifact configured, Stage is a no-op (in-place launch).
func (p *Plugin) Stage(ctx context.Context, svc *config.ServiceConfig) error {
	dc := p.deployConfig(svc)
	if dc.Artifact == "" {
		// In-place launch: no placement, no cleanup.
		return nil
	}
	dest := dc.Dest
	if dest == "" {
		dest = svc.Workspace // default: workspace root (preserves old moveJarToRoot behavior)
	}
	if err := copyArtifact(svc.Workspace, dc.Artifact, dest, p.output); err != nil {
		return fmt.Errorf("stage: %w", err)
	}
	if err := cleanWorkspace(svc.Workspace); err != nil {
		fmt.Fprintf(p.output, "[springboot] warning: failed to clean workspace: %v\n", err)
	} else {
		fmt.Fprintf(p.output, "[springboot] cleaned workspace (source code removed)\n")
	}
	fmt.Fprintf(p.output, "[springboot] staged artifact to %s\n", dest)
	return nil
}

// Start launches the run command (pure launch, no placement) via `sh -c` in a
// detached process group and records its PID. The run command must NOT include
// nohup/& -- backgrounding is the tool's job (process.Manager / Setpgid).
func (p *Plugin) Start(ctx context.Context, svc *config.ServiceConfig) error {
	pidFile := filepath.Join(daemonDir(), svc.Name+".pid")
	mgr := process.NewManager(pidFile)

	if mgr.Status() == "running" {
		return fmt.Errorf("service %s is already running", svc.Name)
	}

	dc := p.deployConfig(svc)
	if dc.Run == "" {
		return fmt.Errorf("run command is empty (configure deploy.run)")
	}

	// Run from the deploy directory when the artifact was placed there, else
	// from the workspace (in-place launch).
	runDir := svc.Workspace
	if dc.Artifact != "" && dc.Dest != "" {
		runDir = dc.Dest
	}

	cmd := exec.Command("sh", "-c", dc.Run)
	cmd.Dir = runDir
	// Option A: app runtime stdout/stderr discarded; the service log holds deploy
	// pipeline logs only. The app must log to its own file (e.g. logback).
	cmd.Stdout = nil
	cmd.Stderr = nil
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	env := os.Environ()
	if javaVersion := detectJavaVersion(svc.Workspace); javaVersion != "" {
		if javaHome := findJavaHome(javaVersion); javaHome != "" {
			env = append(env,
				"JAVA_HOME="+javaHome,
				"PATH="+javaHome+string(os.PathListSeparator)+os.Getenv("PATH"),
			)
		}
	}
	for k, v := range dc.Env {
		env = append(env, k+"="+v)
	}
	cmd.Env = env

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start process: %w", err)
	}
	if err := mgr.WritePID(cmd.Process.Pid); err != nil {
		return err
	}
	fmt.Fprintf(p.output, "started %s with pid %d\n", svc.Name, cmd.Process.Pid)
	return nil
}

// Stop terminates the managed process.
func (p *Plugin) Stop(ctx context.Context, svc *config.ServiceConfig) error {
	pidFile := filepath.Join(daemonDir(), svc.Name+".pid")
	mgr := process.NewManager(pidFile)
	mgr.SetOutput(p.output)
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

// detectJavaVersion reads .java-version file from workspace.
func detectJavaVersion(workspace string) string {
	versionFile := filepath.Join(workspace, ".java-version")
	data, err := os.ReadFile(versionFile)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// findJavaHome finds the JDK home for a given version.
// Tries jenv first, then system Java locations.
func findJavaHome(version string) string {
	if jenvPath, err := exec.Command("jenv", "prefix", version).Output(); err == nil {
		return strings.TrimSpace(string(jenvPath))
	}
	if out, err := exec.Command("/usr/libexec/java_home", "-v", version).Output(); err == nil {
		return strings.TrimSpace(string(out))
	}
	return ""
}

// copyArtifact copies files matching pattern (glob, relative to workspace) to dest.
func copyArtifact(workspace, pattern, dest string, out io.Writer) error {
	matches, err := filepath.Glob(filepath.Join(workspace, pattern))
	if err != nil {
		return err
	}
	if len(matches) == 0 {
		return fmt.Errorf("no artifact matching %s", pattern)
	}
	if err := os.MkdirAll(dest, 0755); err != nil {
		return err
	}
	copied := 0
	for _, src := range matches {
		if strings.HasSuffix(src, ".original.jar") {
			continue
		}
		dst := filepath.Join(dest, filepath.Base(src))
		if err := copyFile(src, dst); err != nil {
			return fmt.Errorf("failed to copy %s: %w", filepath.Base(src), err)
		}
		fmt.Fprintf(out, "[springboot] copied %s to %s\n", filepath.Base(src), dest)
		copied++
	}
	if copied == 0 {
		return fmt.Errorf("no artifact copied from %s", pattern)
	}
	return nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}

// cleanWorkspace removes all files except jar files and .java-version.
func cleanWorkspace(workspace string) error {
	entries, err := os.ReadDir(workspace)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".jar") {
			continue
		}
		if entry.Name() == ".java-version" {
			continue
		}
		path := filepath.Join(workspace, entry.Name())
		if entry.IsDir() {
			if err := os.RemoveAll(path); err != nil {
				return err
			}
		} else {
			if err := os.Remove(path); err != nil {
				return err
			}
		}
	}
	return nil
}
