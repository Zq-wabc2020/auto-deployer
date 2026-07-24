package daemon

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/auto-deployer/auto-deployer/internal/config"
	"github.com/auto-deployer/auto-deployer/internal/notify"
	"github.com/auto-deployer/auto-deployer/internal/process"
)

const defaultPidDir = ".deployd/run"

// Stop stops the deployd daemon.
func Stop() error {
	pidFile := filepath.Join(homeDir(), defaultPidDir, "deployd.pid")
	mgr := process.NewManager(pidFile)

	if mgr.Status() != "running" {
		fmt.Println("deployd is not running")
		return nil
	}

	return mgr.Stop()
}

// Status shows the status of deployd and all configured services.
func Status() error {
	pidFile := filepath.Join(homeDir(), defaultPidDir, "deployd.pid")
	mgr := process.NewManager(pidFile)

	fmt.Printf("deployd: %s\n", mgr.Status())

	configPath := filepath.Join(homeDir(), "config.yaml")
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		return nil
	}

	cfg, err := config.Load(configPath)
	if err != nil {
		return err
	}

	for _, svc := range cfg.Services {
		svcPIDFile := filepath.Join(homeDir(), defaultPidDir, svc.Name+".pid")
		svcMgr := process.NewManager(svcPIDFile)
		fmt.Printf("  %-30s %s\n", svc.Name, svcMgr.Status())
	}

	return nil
}

// Logs prints the contents of a log file.
func Logs(serviceName string) error {
	logDir := filepath.Join(homeDir(), ".deployd", "logs")
	var logFile string
	if serviceName != "" {
		logFile = filepath.Join(logDir, serviceName+".log")
	} else {
		logFile = filepath.Join(logDir, "deployd.log")
	}

	data, err := os.ReadFile(logFile)
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Println("no logs found")
			return nil
		}
		return err
	}
	fmt.Print(string(data))
	return nil
}

// TriggerDeploy manually triggers deployment for a service.
func TriggerDeploy(serviceName string) error {
	configPath := filepath.Join(homeDir(), "config.yaml")
	cfg, err := config.Load(configPath)
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

	fmt.Printf("[deploy] triggering deployment for %s...\n", svc.Name)

	// Build notifier and send "running" notification
	notifier := buildNotifier(cfg)
	if notifier != nil {
		go func() {
			_ = notifier.NotifyDeployResult(context.Background(), svc.Name, svc.Repo.Branch, "", "running", "")
		}()
	}

	// TODO: full deploy flow — git pull → build → stop → start
	fmt.Printf("[deploy] workspace: %s\n", svc.Workspace)
	fmt.Printf("[deploy] build command: %s\n", svc.Build.Command)
	fmt.Printf("[deploy] run command: %s\n", svc.Run.Command)

	// Send success notification (placeholder: deploy not yet implemented)
	if notifier != nil {
		go func() {
			_ = notifier.NotifyDeployResult(context.Background(), svc.Name, svc.Repo.Branch, "", "success", "")
		}()
	}

	return nil
}

func homeDir() string {
	h, _ := os.UserHomeDir()
	return h
}

// buildNotifier creates a Notifier from config, or returns nil if not configured.
func buildNotifier(cfg *config.AppConfig) *notify.Notifier {
	if cfg == nil || len(cfg.Notifications.To) == 0 {
		return nil
	}
	return notify.New(
		cfg.SMTP.Host,
		cfg.SMTP.Port,
		cfg.SMTP.Username,
		cfg.SMTP.Token,
		cfg.SMTP.TLS,
		cfg.Notifications.To,
	)
}
