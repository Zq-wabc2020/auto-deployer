package daemon

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"

	"auto-deployer/internal/build"
	"auto-deployer/internal/config"
	"auto-deployer/internal/process"
	"auto-deployer/internal/webhook"
	"auto-deployer/plugins/springboot"
)

const defaultConfigName = "config.yaml"
const defaultPidDir = ".deployd/run"

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

	// 5. Create workspace directories
	for _, svc := range cfg.Services {
		if err := os.MkdirAll(svc.Workspace, 0755); err != nil {
			return fmt.Errorf("failed to create workspace %s: %w", svc.Workspace, err)
		}
	}

	// 6. Register Spring Boot plugin
	springboot.New()

	// 7. Start webhook HTTP server
	addr := fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port)
	http.HandleFunc("/webhook", webhook.Handle)

	go func() {
		fmt.Printf("[daemon] starting webhook server on %s\n", addr)
		if err := http.ListenAndServe(addr, nil); err != nil {
			fmt.Fprintf(os.Stderr, "[daemon] webhook server error: %v\n", err)
		}
	}()

	// 8. Write PID file
	pidDir := filepath.Join(os.Getenv("HOME"), defaultPidDir)
	_ = os.MkdirAll(pidDir, 0755)
	pidFile := filepath.Join(pidDir, "deployd.pid")
	mgr := process.NewManager(pidFile)

	if mgr.Status() == "running" {
		existingPID, _ := mgr.ReadPID()
		return fmt.Errorf("deployd is already running (pid: %d)", existingPID)
	}

	myPID := os.Getpid()
	if err := mgr.WritePID(myPID); err != nil {
		return err
	}

	fmt.Printf("[daemon] deployd started on %s (pid: %d)\n", addr, myPID)
	return nil
}
