package daemon

import (
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"syscall"

	"github.com/auto-deployer/auto-deployer/internal/build"
	"github.com/auto-deployer/auto-deployer/internal/config"
	"github.com/auto-deployer/auto-deployer/internal/process"
	"github.com/auto-deployer/auto-deployer/internal/webhook"
	"github.com/auto-deployer/auto-deployer/plugins/springboot"
)

const defaultConfigName = "config.yaml"
const pidDirName = ".deployd/run"
const daemonLogName = "deployd.log"

// Start loads config, validates it, checks the environment, and launches the webhook server.
// On Linux, it forks into a detached daemon process and returns immediately.
// On other platforms, it blocks until a termination signal is received.
func Start(configPath string) error {
	if configPath == "" {
		home, _ := os.UserHomeDir()
		configPath = filepath.Join(home, defaultConfigName)
	}

	// 1. Check config file exists
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		return fmt.Errorf("config file not found: %s\nRun `deployd config` to create one", configPath)
	}

	// 2. Load config
	cfg, err := config.Load(configPath)
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	// 3. Validate config
	if errs := config.Validate(cfg); len(errs) > 0 {
		for _, e := range errs {
			fmt.Fprintf(os.Stderr, "config error: %v\n", e)
		}
		return fmt.Errorf("config validation failed")
	}

	// 4. Check environment
	if errs := build.CheckEnvironment(); len(errs) > 0 {
		for _, e := range errs {
			fmt.Fprintf(os.Stderr, "environment error: %v\n", e)
		}
		return fmt.Errorf("environment check failed")
	}

	// 5. Create workspace directories and ensure git config
	for _, svc := range cfg.Services {
		if err := os.MkdirAll(svc.Workspace, 0755); err != nil {
			return fmt.Errorf("failed to create workspace %s: %w", svc.Workspace, err)
		}
		if err := build.EnsureGitConfig(svc.Workspace); err != nil {
			fmt.Fprintf(os.Stderr, "[daemon] warning: failed to set git config in %s: %v\n", svc.Workspace, err)
		}
	}

	// 6. Register Spring Boot plugin
	springboot.New()

	// 7. Ensure SSH key exists and print setup instructions if needed
	if privKeyPath, _, pubKey, err := build.EnsureSSHKey(); err != nil {
		fmt.Fprintf(os.Stderr, "[daemon] warning: failed to check SSH key: %v\n", err)
	} else {
		fmt.Printf("[daemon] SSH key ready: %s\n", privKeyPath)
		fmt.Printf("[daemon] Public key: %s\n", pubKey)
		fmt.Printf("[daemon] Configure this public key on your GitHub/Gitee account.\n")
		fmt.Printf("[daemon]   GitHub: Settings → SSH and GPG keys → New SSH key\n")
		fmt.Printf("[daemon]   Gitee:  设置 → 安全设置 → SSH公钥\n\n")
	}

	// 8. Check if already running
	pidDir := filepath.Join(homeDir(configPath), pidDirName)
	_ = os.MkdirAll(pidDir, 0755)
	pidFile := filepath.Join(pidDir, "deployd.pid")
	mgr := process.NewManager(pidFile)

	if mgr.Status() == "running" {
		existingPID, _ := mgr.ReadPID()
		return fmt.Errorf("deployd is already running (pid: %d)", existingPID)
	}

	// On Linux: fork into background daemon process
	if runtime.GOOS == "linux" {
		return startDaemon(configPath, pidFile)
	}

	// On other platforms (macOS, etc): run in foreground
	return runForeground(configPath, pidFile, mgr)
}

// startDaemon forks a child process, detaches it from the terminal, and exits the parent.
func startDaemon(configPath, pidFile string) error {
	// Build the command to re-run self with the same arguments
	selfPath, _ := os.Executable()
	args := []string{"start", "-c", configPath, "--daemon-child"}

	cmd := exec.Command(selfPath, args...)
	cmd.Stdout = nil
	cmd.Stderr = nil
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setpgid: true,
		Setsid:  true,
	}

	// Redirect stdout/stderr to log file inside the child
	logPath := filepath.Join(homeDir(configPath), ".deployd", daemonLogName)
	if err := os.MkdirAll(filepath.Dir(logPath), 0755); err != nil {
		return fmt.Errorf("failed to create log dir: %w", err)
	}
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return fmt.Errorf("failed to open log file: %w", err)
	}
	cmd.Stdout = logFile
	cmd.Stderr = logFile

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start daemon: %w", err)
	}

	// Parent exits immediately
	fmt.Printf("[daemon] daemon process started (pid: %d)\n", cmd.Process.Pid)
	fmt.Printf("[daemon] logs: %s\n", logPath)
	fmt.Printf("[daemon] use 'deployd status' to check, 'deployd stop' to stop\n")
	return nil
}

// runForeground runs the server and blocks until termination (used on macOS).
func runForeground(configPath, pidFile string, mgr *process.Manager) error {
	myPID := os.Getpid()
	if err := mgr.WritePID(myPID); err != nil {
		return err
	}

	runServer(configPath, pidFile)
	return nil
}

// runServer runs the webhook server and blocks until termination signal.
func runServer(configPath, pidFile string) {
	cfg, _ := config.Load(configPath)
	addr := fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port)
	webhook.SetConfigPath(configPath)

	http.HandleFunc("/webhook", webhook.Handle)

	go func() {
		fmt.Printf("[daemon] starting webhook server on %s\n", addr)
		if err := http.ListenAndServe(addr, nil); err != nil {
			fmt.Fprintf(os.Stderr, "[daemon] webhook server error: %v\n", err)
		}
	}()

	// Block until termination signal
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	sig := <-sigCh
	fmt.Printf("[daemon] received %s, shutting down...\n", sig)

	// Cleanup PID file on exit
	mgr := process.NewManager(pidFile)
	_ = mgr.CleanupPID()
}
