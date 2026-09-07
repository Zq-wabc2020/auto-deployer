package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/auto-deployer/auto-deployer/internal/config"
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
			path = config.DefaultConfig()
		}
		if path == "" {
			return fmt.Errorf("config file not found. Run 'deployd config' to create one, or use -c to specify path")
		}

		noFork, _ := cmd.Flags().GetBool("no-fork")
		if noFork {
			return daemon.Start(path)
		}

		// fork 前先校验配置：校验错误（如缺 deploy.health）直接报给用户，
		// 而不是等 fork 子进程死后只报一个 10s 就绪超时。
		if err := validateConfigFile(path); err != nil {
			return err
		}

		// Fork to background on Linux, block in foreground on macOS
		if runtime.GOOS == "linux" {
			return forkToBackground(path)
		}
		return daemon.Start(path)
	},
}

// validateConfigFile 加载并校验配置文件，返回首个校验错误（供 fork 前快速失败）。
func validateConfigFile(path string) error {
	cfg, err := config.Load(path)
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}
	if errs := config.Validate(cfg); len(errs) > 0 {
		for _, e := range errs {
			fmt.Fprintf(os.Stderr, "config error: %v\n", e)
		}
		return fmt.Errorf("config validation failed")
	}
	return nil
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

	// Wait until the child is ready (writes its pid file) or exits, so a
	// config/validation/environment failure surfaces in the terminal instead
	// of silently dying into daemon-fork.log.
	if err := waitDaemonReady(cmd.Process.Pid, logPath); err != nil {
		return err
	}

	fmt.Printf("daemon started in background (pid: %d)\n", cmd.Process.Pid)
	fmt.Printf("logs: %s\n", logPath)
	fmt.Printf("use 'deployd status' or 'deployd stop' to manage\n")
	return nil
}

// waitDaemonReady polls the forked daemon child until it signals readiness
// (deployd.pid contains the child's pid) or exits. On exit, the tail of the
// fork log -- where the child's startup errors go -- is printed to stderr.
//
// 退出探测用 Wait4(WNOHANG) 而非 Kill(pid, 0)：子进程死后未 Wait 会变僵尸，
// Kill 对僵尸也返回成功，导致退出被误判为存活（表现为等满 10s 报就绪超时，
// 掩盖真实启动错误）。
func waitDaemonReady(childPid int, logPath string) error {
	home, _ := os.UserHomeDir()
	pidFile := filepath.Join(home, ".deployd", "run", "deployd.pid")

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		var ws syscall.WaitStatus
		if wpid, err := syscall.Wait4(childPid, &ws, syscall.WNOHANG, nil); err == nil && wpid == childPid {
			printLogTail(logPath, 30)
			return fmt.Errorf("daemon exited during startup (log: %s)", logPath)
		}
		if pid, err := readPidFile(pidFile); err == nil && pid == childPid {
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Errorf("daemon did not signal readiness within 10s (log: %s)", logPath)
}

func readPidFile(path string) (int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return 0, err
	}
	return pid, nil
}

// printLogTail prints the last n lines of the log file to stderr.
func printLogTail(path string, n int) {
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	fmt.Fprintf(os.Stderr, "--- %s (last %d lines) ---\n", path, len(lines))
	for _, l := range lines {
		fmt.Fprintln(os.Stderr, l)
	}
	fmt.Fprintln(os.Stderr, "--- end ---")
}
