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
	"github.com/auto-deployer/auto-deployer/plugins/springboot"
	"github.com/spf13/cobra"
)

func init() {
	deployCmd.Flags().Bool("no-fork", false, "Run in foreground (no background fork)")
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

		if noFork {
			_, err := deploy.Deploy(cmd.Context(), svc, cfg, d)
			return err
		}

		// Fork to background on Linux
		if runtime.GOOS == "linux" {
			return forkDeploy(path, serviceName)
		}

		// On macOS, run in foreground
		_, err = deploy.Deploy(cmd.Context(), svc, cfg, d)
		return err
	},
}

// forkDeploy spawns a child process to run deployment in background
func forkDeploy(configPath, serviceName string) error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("failed to get executable path: %w", err)
	}

	logDir := filepath.Join(filepath.Dir(configPath), ".deployd", "services")
	if err := os.MkdirAll(logDir, 0755); err != nil {
		return fmt.Errorf("failed to create log directory: %w", err)
	}
	logPath := filepath.Join(logDir, fmt.Sprintf("%s.log", serviceName))

	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return fmt.Errorf("failed to open log file: %w", err)
	}
	defer logFile.Close()

	cmd := exec.Command(exe, "deploy", "--no-fork", "-c", configPath, serviceName)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.Stdin = nil
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start deployment: %w", err)
	}

	fmt.Printf("[deploy] deployment started for %s in background (pid: %d)\n", serviceName, cmd.Process.Pid)
	fmt.Printf("[deploy] logs: %s\n", logPath)
	fmt.Printf("[deploy] use 'deployd logs %s' to follow\n", serviceName)
	return nil
}
