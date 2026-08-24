package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"syscall"

	"github.com/auto-deployer/auto-deployer/internal/cache"
	"github.com/auto-deployer/auto-deployer/internal/config"
	"github.com/auto-deployer/auto-deployer/internal/constants"
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
	RunE: func(cmd *cobra.Command, args []string) (startErr error) {
		path := configFile
		if path == "" {
			path = config.DefaultConfig()
		}
		if path == "" {
			startErr = fmt.Errorf("config file not found. Run 'deployd config' to create one, or use -c to specify path")
			return
		}

		defer func() {
			if startErr != nil {
				absPath, err := filepath.Abs(path)
				if err != nil {
					return
				}

				err = cache.Set(constants.START_CONFIG_PATH_KEY, absPath)
				if err != nil {
					fmt.Printf("Warning: failed to cache config path: %v", err)
				}
			}
		}()

		noFork, _ := cmd.Flags().GetBool("no-fork")
		if noFork {
			startErr = daemon.Start(path)
			return
		}

		// Fork to background on Linux, block in foreground on macOS
		if runtime.GOOS == "linux" {
			startErr = forkToBackground(path)
			return
		}
		startErr = daemon.Start(path)
		return
	},
}

// forkToBackground spawns a child process with --no-fork and exits immediately.
func forkToBackground(configPath string) error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("failed to get executable path: %w", err)
	}

	// Open log file for child process output
	logDir := filepath.Join(filepath.Dir(configPath), ".deployd")
	_ = os.MkdirAll(logDir, 0755)
	logPath := filepath.Join(logDir, "daemon-fork.log")
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return fmt.Errorf("failed to open fork log: %w", err)
	}
	defer logFile.Close()

	cmd := exec.Command(exe, "start", "--no-fork", "-c", configPath)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.Stdin = nil
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start daemon: %w", err)
	}

	// Don't wait - daemon runs in background
	fmt.Printf("daemon started in background (pid: %d)\n", cmd.Process.Pid)
	fmt.Printf("logs: %s\n", logPath)
	fmt.Printf("use 'deployd status' or 'deployd stop' to manage\n")
	return nil
}
