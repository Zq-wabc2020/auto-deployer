package daemon

import (
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"

	"github.com/auto-deployer/auto-deployer/internal/build"
	"github.com/auto-deployer/auto-deployer/internal/config"
	"github.com/auto-deployer/auto-deployer/internal/process"
	"github.com/auto-deployer/auto-deployer/internal/webhook"
	"github.com/auto-deployer/auto-deployer/plugins/springboot"
)

const defaultConfigName = "config.yaml"

// Start loads config, validates it, checks the environment, and launches the webhook server.
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

	// 6.5 Set config path for webhook handler
	webhook.SetConfigPath(configPath)

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

	// 8. Start webhook HTTP server
	addr := fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port)
	http.HandleFunc("/webhook", webhook.Handle)

	go func() {
		fmt.Printf("[daemon] starting webhook server on %s\n", addr)
		if err := http.ListenAndServe(addr, nil); err != nil {
			fmt.Fprintf(os.Stderr, "[daemon] webhook server error: %v\n", err)
		}
	}()

	// 9. Write PID file
	pidDir := filepath.Join(homeDir(configPath), defaultPidDir)
	_ = os.MkdirAll(pidDir, 0755)
	pidFile := filepath.Join(pidDir, "deployd.pid")
	mgr := process.NewManager(pidFile)

	if mgr.Status() == "running" {
		existingPID, _ := mgr.ReadPID()
		return fmt.Errorf("deployd is already running (pid: %d)", existingPID)
	}

	// Fork into background (double-fork to create proper daemon)
	// First fork: child continues, parent exits
	pid, err := syscall.ForkExec(os.Args[0], os.Args, &syscall.ProcAttr{
		Env:   os.Environ(),
		Dirs:  []string{"/"},
		Files: []uintptr{os.Stdin.Fd(), os.Stdout.Fd(), os.Stderr.Fd()},
		Sys:   &syscall.SysProcAttr{Setsid: true},
	})
	if err != nil {
		return fmt.Errorf("failed to fork daemon: %w", err)
	}

	// Parent exits immediately — child becomes a daemon
	fmt.Printf("[daemon] starting webhook server on %s\n", addr)
	fmt.Printf("[daemon] deployd started as daemon (pid: %d)\n", pid)
	fmt.Printf("[daemon] run 'deployd stop' to stop, 'deployd logs' to view logs\n")
	return nil
}

// run is the actual daemon entry point, called by the forked child.
// It blocks until SIGTERM/SIGINT, then cleans up.
func run(configPath string) {
	// 8. Start webhook HTTP server
	addr := fmt.Sprintf("%s:%d", loadConfig(configPath).Server.Host, loadConfig(configPath).Server.Port)
	http.HandleFunc("/webhook", webhook.Handle)

	go func() {
		fmt.Printf("[daemon] webhook server listening on %s\n", addr)
		if err := http.ListenAndServe(addr, nil); err != nil {
			fmt.Fprintf(os.Stderr, "[daemon] webhook server error: %v\n", err)
		}
	}()

	// 9. Write PID file
	pidDir := filepath.Join(homeDir(configPath), defaultPidDir)
	_ = os.MkdirAll(pidDir, 0755)
	pidFile := filepath.Join(pidDir, "deployd.pid")
	mgr := process.NewManager(pidFile)

	// Re-check: prevent race if another instance started between fork and here
	if mgr.Status() == "running" {
		return
	}

	myPID := os.Getpid()
	if err := mgr.WritePID(myPID); err != nil {
		fmt.Fprintf(os.Stderr, "[daemon] failed to write PID file: %v\n", err)
	}

	fmt.Printf("[daemon] deployd running on %s (pid: %d)\n", addr, myPID)

	// Block until termination signal
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh
	fmt.Printf("[daemon] received signal, shutting down...\n")

	// Cleanup PID file on exit
	_ = mgr.CleanupPID()
}

func loadConfig(path string) *config.AppConfig {
	cfg, err := config.Load(path)
	if err != nil {
		return &config.AppConfig{}
	}
	return cfg
}
