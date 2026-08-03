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

// Build executes the configured build command.
// Git fetch is handled by the orchestrator before calling this method.
func (p *Plugin) Build(ctx context.Context, svc *config.ServiceConfig) error {
	if svc.Build.Command == "" {
		return fmt.Errorf("build command is empty")
	}

	if err := build.ExecuteBuild(svc.Workspace, svc.Build.Command); err != nil {
		return err
	}

	// Move built jar to workspace root
	if err := moveJarToRoot(svc.Workspace); err != nil {
		fmt.Printf("[springboot] warning: failed to move jar: %v\n", err)
	}

	// Clean up everything except jar file (source code removed after build)
	if err := cleanWorkspace(svc.Workspace); err != nil {
		fmt.Printf("[springboot] warning: failed to clean workspace: %v\n", err)
	} else {
		fmt.Printf("[springboot] cleaned workspace (source code removed)\n")
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

	// Set workspace as working directory
	cmd := exec.Command(parts[0], parts[1:]...)
	cmd.Dir = svc.Workspace
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	// Auto-detect Java version from .java-version file
	if javaVersion := detectJavaVersion(svc.Workspace); javaVersion != "" {
		if javaHome := findJavaHome(javaVersion); javaHome != "" {
			cmd.Env = append(os.Environ(), "JAVA_HOME="+javaHome)
			cmd.Env = append(cmd.Env, "PATH="+javaHome+string(os.PathListSeparator)+os.Getenv("PATH"))
		}
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start process: %w", err)
	}

	if err := mgr.WritePID(cmd.Process.Pid); err != nil {
		return err
	}

	fmt.Printf("started %s with pid %d\n", parts[0], cmd.Process.Pid)
	return nil
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
	// Try jenv
	if jenvPath, err := exec.Command("jenv", "prefix", version).Output(); err == nil {
		return strings.TrimSpace(string(jenvPath))
	}
	// Try system java_home
	if out, err := exec.Command("/usr/libexec/java_home", "-v", version).Output(); err == nil {
		return strings.TrimSpace(string(out))
	}
	return ""
}

// moveJarToRoot finds the built jar in workspace/target/ and copies it to workspace root.
func moveJarToRoot(workspace string) error {
	targetDir := filepath.Join(workspace, "target")
	entries, err := os.ReadDir(targetDir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".jar") && !strings.HasSuffix(entry.Name(), "original.jar") {
			src := filepath.Join(targetDir, entry.Name())
			dst := filepath.Join(workspace, entry.Name())
			if err := copyFile(src, dst); err != nil {
				return fmt.Errorf("failed to copy jar %s: %w", entry.Name(), err)
			}
			fmt.Printf("[springboot] copied %s to %s\n", entry.Name(), workspace)
			return nil
		}
	}
	return fmt.Errorf("no jar found in %s/target", workspace)
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

// cleanWorkspace removes all files except jar files.
func cleanWorkspace(workspace string) error {
	entries, err := os.ReadDir(workspace)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		// Skip jar files (build artifacts to keep)
		if strings.HasSuffix(entry.Name(), ".jar") {
			continue
		}
		// Remove everything else (source code, .git, target, etc.)
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
