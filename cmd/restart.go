package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"syscall"

	"github.com/auto-deployer/auto-deployer/internal/config"
	"github.com/auto-deployer/auto-deployer/internal/daemon"
	"github.com/spf13/cobra"
)

func init() {
	restartCmd.Flags().Bool("no-fork", false, "Run in foreground (no background fork)")
	restartCmd.Flags().StringVarP(&configFile, "config", "c", "", "config file path")
	rootCmd.AddCommand(restartCmd)
}

var restartCmd = &cobra.Command{
	Use:   "restart",
	Short: "Restart the deployd daemon",
	RunE: func(cmd *cobra.Command, args []string) error {
		path := configFile
		if path == "" {
			path = config.DefaultConfig()
		}

		// Stop current daemon
		if err := daemon.Stop(path); err != nil {
			fmt.Printf("stopped existing daemon: %v\n", err)
		}

		noFork, _ := cmd.Flags().GetBool("no-fork")
		if noFork {
			return daemon.Start(path)
		}

		if runtime.GOOS == "linux" {
			return forkToRestart(path)
		}
		return daemon.Start(path)
	},
}

func forkToRestart(configPath string) error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("failed to get executable path: %w", err)
	}

	logDir := filepath.Join(filepath.Dir(configPath), ".deployd")
	_ = os.MkdirAll(logDir, 0755)
	logPath := filepath.Join(logDir, "daemon-fork.log")
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return fmt.Errorf("failed to open fork log: %w", err)
	}
	defer logFile.Close()

	cmd := exec.Command(exe, "restart", "--no-fork", "-c", configPath)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.Stdin = nil
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to restart daemon: %w", err)
	}

	fmt.Printf("daemon restarted in background (pid: %d)\n", cmd.Process.Pid)
	fmt.Printf("logs: %s\n", logPath)
	return nil
}
