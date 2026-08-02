package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"syscall"

	"github.com/auto-deployer/auto-deployer/internal/daemon"
	"github.com/spf13/cobra"
)

func init() {
	startCmd.Flags().Bool("no-fork", false, "Run in foreground (no background fork)")
	startCmd.Flags().StringVarP(&configFile, "config", "c", "", "config file path")
	rootCmd.AddCommand(startCmd)
}

var startCmd = &cobra.Command{
	Use:   "start",
	Short: "Start the deployd daemon",
	RunE: func(cmd *cobra.Command, args []string) error {
		path := configFile
		if path == "" {
			home, _ := os.UserHomeDir()
			path = filepath.Join(home, "config.yaml")
		}

		noFork, _ := cmd.Flags().GetBool("no-fork")
		if noFork {
			return daemon.Start(path)
		}

		// Fork to background on Linux, block in foreground on macOS
		if runtime.GOOS == "linux" {
			return forkToBackground(path)
		}
		return daemon.Start(path)
	},
}

// forkToBackground spawns a child process with --no-fork and exits immediately.
func forkToBackground(configPath string) error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("failed to get executable path: %w", err)
	}

	cmd := exec.Command(exe, "start", "--no-fork", "-c", configPath)
	cmd.Stdout = nil
	cmd.Stderr = nil
	cmd.Stdin = nil
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start daemon: %w", err)
	}

	fmt.Printf("daemon started in background (pid: %d)\n", cmd.Process.Pid)
	fmt.Printf("logs: ~/.deployd/deployd.log\n")
	fmt.Printf("use 'deployd status' or 'deployd stop' to manage\n")
	return nil
}
