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

// Build executes git clone/pull followed by the configured build command.
func (p *Plugin) Build(ctx context.Context, svc *config.ServiceConfig) error {
	if svc.Build.Command == "" {
		return fmt.Errorf("build command is empty")
	}

	keyFile, _, _, err := build.EnsureSSHKey()
	if err != nil {
		return fmt.Errorf("failed to ensure SSH key: %w", err)
	}

	// Check if workspace needs to be cloned (doesn't exist or not a git repo)
	gitDir := filepath.Join(svc.Workspace, ".git")
	if _, err := os.Stat(gitDir); os.IsNotExist(err) {
		// Clone the repository
		fmt.Printf("[deploy] cloning %s to %s...\n", svc.Repo.URL, svc.Workspace)
		if err := build.Clone(svc.Repo.URL, keyFile, svc.Repo.Branch, svc.Workspace); err != nil {
			return fmt.Errorf("git clone failed: %w", err)
		}
	} else {
		// Pull updates
		fmt.Printf("[deploy] pulling updates for %s...\n", svc.Workspace)
		if err := build.Pull(svc.Workspace, svc.Repo.Branch, keyFile); err != nil {
			if build.IsSSHAuthError(err) {
				privKeyPath, _, pubKey, err := build.EnsureSSHKey()
				if err == nil {
					fmt.Printf("\n[warn] SSH authentication failed. Public key to configure on your Git platform:\n")
					fmt.Printf("  %s\n\n", pubKey)
					fmt.Printf("1. Add the above public key to your GitHub/Gitee account:\n")
					fmt.Printf("   GitHub: Settings → SSH and GPG keys → New SSH key\n")
					fmt.Printf("   Gitee:  设置 → 安全设置 → SSH公钥\n")
					fmt.Printf("2. Key file: %s\n", privKeyPath)
					fmt.Printf("3. Re-run: deployd deploy %s\n\n", svc.Name)
				}
			}
			return fmt.Errorf("git pull failed: %w", err)
		}
	}

	if err := build.ExecuteBuild(svc.Workspace, svc.Build.Command); err != nil {
		return err
	}

	// Move built jar to workspace root
	if err := moveJarToRoot(svc.Workspace); err != nil {
		fmt.Printf("[springboot] warning: failed to move jar: %v\n", err)
	}

	// Clean up everything except the jar file and target directory
	if err := cleanWorkspace(svc.Workspace); err != nil {
		fmt.Printf("[springboot] warning: failed to clean workspace: %v\n", err)
	} else {
		fmt.Printf("[springboot] cleaned workspace (kept jar and target/)\n")
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

// cleanWorkspace removes all files except jar files and target/ directory.
func cleanWorkspace(workspace string) error {
	entries, err := os.ReadDir(workspace)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		// Skip jar files (already copied to root)
		if strings.HasSuffix(entry.Name(), ".jar") {
			continue
		}
		// Skip target directory (contains build artifacts)
		if entry.Name() == "target" {
			continue
		}
		// Remove everything else
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
