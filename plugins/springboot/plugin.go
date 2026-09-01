package springboot

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
		if err := config.StrictDecodeDeploy(svc.Deploy, &dc); err != nil {
			fmt.Fprintf(p.output, "[springboot] warning: deploy 配置存在无法识别的字段(会被忽略,请检查是否用了其他模型的专属字段): %v\n", err)
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
	if err := build.ExecuteBuild(ctx, svc.Workspace, svc.Build.Command.String(), p.output); err != nil {
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
	keep, err := build.CopyArtifact(svc.Workspace, dc.Artifact, dest, p.output)
	if err != nil {
		return fmt.Errorf("stage: %w", err)
	}
	if err := cleanWorkspace(svc.Workspace, keep); err != nil {
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
	if javaVersion := build.DetectJavaVersion(svc.Workspace); javaVersion != "" {
		if javaHome := build.FindJavaHome(javaVersion); javaHome != "" {
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

// cleanWorkspace removes all workspace entries except the files just staged
// (keep), .java-version and .git (.git is kept so the next deploy's Fetch can
// take the fast path). Keeping only the actually-staged names -- instead of
// every *.jar -- means old versioned jars no longer accumulate in the
// workspace when dest is the workspace root.
func cleanWorkspace(workspace string, keep []string) error {
	keepSet := make(map[string]bool, len(keep)+2)
	for _, name := range keep {
		keepSet[name] = true
	}
	entries, err := os.ReadDir(workspace)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if keepSet[entry.Name()] {
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
