package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"syscall"

	"github.com/auto-deployer/auto-deployer/internal/config"
	"github.com/auto-deployer/auto-deployer/internal/deploy"
	"github.com/auto-deployer/auto-deployer/internal/deploylock"
	"github.com/auto-deployer/auto-deployer/plugins/springboot"
	"github.com/spf13/cobra"
)

func init() {
	deployCmd.Flags().Bool("no-fork", false, "Run in foreground (no background fork)")
	deployCmd.Flags().Bool("locked", false, "internal: deploy lock already held by an inherited fd")
	if f := deployCmd.Flags().Lookup("locked"); f != nil {
		f.Hidden = true
	}
	deployCmd.Flags().StringVarP(&configFile, "config", "c", "", "config file path")
	rootCmd.AddCommand(deployCmd)
}

var deployCmd = &cobra.Command{
	Use:   "deploy <service_name>",
	Short: "Manually trigger full deployment for a service",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		serviceName := args[0]
		noFork, _ := cmd.Flags().GetBool("no-fork")
		locked, _ := cmd.Flags().GetBool("locked")

		path := configFile
		if path == "" {
			path = config.DefaultConfig()
		}
		if path == "" {
			return fmt.Errorf("config file not found. Run 'deployd config' to create one, or use -c to specify path")
		}

		cfg, err := config.Load(path)
		if err != nil {
			return fmt.Errorf("failed to load config: %w", err)
		}

		var svc *config.ServiceConfig
		for i := range cfg.Services {
			if cfg.Services[i].Name == serviceName {
				s := &cfg.Services[i]
				svc = s
				break
			}
		}
		if svc == nil {
			return fmt.Errorf("service %q not found in config", serviceName)
		}

		var d deploy.Deployer
		switch svc.Type {
		case "springboot":
			d = springboot.New()
		default:
			return fmt.Errorf("unknown service type %q (supported: springboot)", svc.Type)
		}

		// Manual deploys have the lowest priority: if the service is already
		// deploying (webhook queue busy or another manual deploy running), fail
		// fast instead of waiting or queuing.
		if noFork {
			// --no-fork is used both by the fork child (with --locked, lock
			// already inherited) and by explicit foreground runs (must acquire
			// the lock themselves).
			if !locked {
				lock, err := deploylock.TryAcquire(serviceName)
				if err != nil {
					return fmt.Errorf("服务 %s 正在部署中，请勿重复操作", serviceName)
				}
				defer lock.Release()
			} else {
				// Lock inherited from the fork parent (ExtraFiles). Release it
				// explicitly after Deploy: the deployed service also inherits
				// the fd and would hold the lock forever if we relied on this
				// process exiting.
				defer func() {
					if err := deploylock.ReleaseInherited(); err != nil {
						fmt.Fprintf(os.Stderr, "[deploy] release inherited lock: %v\n", err)
					}
				}()
			}
			_, err := deploy.Deploy(cmd.Context(), svc, cfg, d, nil)
			return err
		}

		// Fork to background on Linux (survives SSH disconnect).
		if runtime.GOOS == "linux" {
			return forkDeploy(path, serviceName)
		}

		// On macOS, run in foreground with the deploy lock.
		lock, err := deploylock.TryAcquire(serviceName)
		if err != nil {
			return fmt.Errorf("服务 %s 正在部署中，请勿重复操作", serviceName)
		}
		defer lock.Release()
		_, err = deploy.Deploy(cmd.Context(), svc, cfg, d, nil)
		return err
	},
}

// forkDeploy spawns a child process to run deployment in background. The deploy
// lock is acquired (non-blocking) in the parent for fail-fast feedback, then
// handed to the child via ExtraFiles; the child runs --locked so it skips its
// own TryAcquire. The parent closes its lock fd without unlocking (flock locks
// are shared across duplicated fds), so the lock stays held by the child until
// it exits.
func forkDeploy(configPath, serviceName string) error {
	lock, err := deploylock.TryAcquire(serviceName)
	if err != nil {
		return fmt.Errorf("服务 %s 正在部署中，请勿重复操作", serviceName)
	}

	exe, err := os.Executable()
	if err != nil {
		lock.Release()
		return fmt.Errorf("failed to get executable path: %w", err)
	}

	// Use home directory for consistent log path with 'deployd logs' command
	homeDir, _ := os.UserHomeDir()
	logDir := filepath.Join(homeDir, ".deployd", "services")
	if err := os.MkdirAll(logDir, 0755); err != nil {
		lock.Release()
		return fmt.Errorf("failed to create log directory: %w", err)
	}
	logPath := filepath.Join(logDir, fmt.Sprintf("%s.log", serviceName))

	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		lock.Release()
		return fmt.Errorf("failed to open log file: %w", err)
	}
	defer logFile.Close()

	cmd := exec.Command(exe, "deploy", "--no-fork", "--locked", "-c", configPath, serviceName)
	// Tell the child which fd holds the inherited deploy lock (ExtraFiles[0] = fd 3)
	// so it can mark it close-on-exec before launching the service.
	cmd.Env = append(os.Environ(), "DEPLOYD_LOCK_FD=3")
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.Stdin = nil
	cmd.ExtraFiles = []*os.File{lock.File()}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}

	if err := cmd.Start(); err != nil {
		lock.Release()
		return fmt.Errorf("failed to start deployment: %w", err)
	}

	// Child has inherited the lock fd; parent detaches without unlocking.
	lock.Close()

	fmt.Printf("[deploy] deployment started for %s in background (pid: %d)\n", serviceName, cmd.Process.Pid)
	fmt.Printf("[deploy] logs: %s\n", logPath)
	fmt.Printf("[deploy] use 'deployd logs %s' to follow\n", serviceName)
	return nil
}
