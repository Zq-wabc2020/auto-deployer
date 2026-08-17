package springboot

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

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
	if svc.Deploy.Kind != 0 { // zero node = no deploy: block
		if err := svc.Deploy.Decode(&dc); err != nil {
			fmt.Fprintf(p.output, "[springboot] warning: failed to parse deploy config: %v\n", err)
		}
	}
	return dc
}

func pidFileFor(name string) string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".deployd", "run", name+".pid")
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
	if err := build.CopyArtifact(svc.Workspace, dc.Artifact, dest, p.output); err != nil {
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

// Start launches the run command (pure launch, no placement) via the shared
// PID-managed shell starter. The run command must NOT include nohup/& --
// backgrounding is the tool's job.
func (p *Plugin) Start(ctx context.Context, svc *config.ServiceConfig) error {
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

	overrides := map[string]string{}
	if javaVersion := detectJavaVersion(svc.Workspace); javaVersion != "" {
		if javaHome := findJavaHome(javaVersion); javaHome != "" {
			overrides["JAVA_HOME"] = javaHome
			// NOTE: prepend javaHome/BIN (the JDK home itself is not on the
			// executable path) so `java` resolves to the requested version
			// instead of whatever a jenv shim or system default picks.
			overrides["PATH"] = filepath.Join(javaHome, "bin") + string(os.PathListSeparator) + os.Getenv("PATH")
		}
	}
	for k, v := range dc.Env {
		overrides[k] = v
	}

	mgr := process.NewManager(pidFileFor(svc.Name))
	return mgr.StartShell(runDir, dc.Run, overrides, p.output)
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

// cleanWorkspace removes all files except jar files, .java-version and .git
// (.git is kept so the next deploy's Fetch can take the fast path).
func cleanWorkspace(workspace string) error {
	entries, err := os.ReadDir(workspace)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".jar") {
			continue
		}
		if entry.Name() == ".java-version" || entry.Name() == ".git" {
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
