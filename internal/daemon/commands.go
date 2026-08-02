package daemon

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/auto-deployer/auto-deployer/internal/config"
	"github.com/auto-deployer/auto-deployer/internal/notify"
	"github.com/auto-deployer/auto-deployer/internal/process"
)

const defaultPidDir = ".deployd/run"

// Stop stops the deployd daemon.
func Stop(configPath string) error {
	pidFile := filepath.Join(homeDir(configPath), defaultPidDir, "deployd.pid")
	mgr := process.NewManager(pidFile)

	if mgr.Status() != "running" {
		fmt.Println("deployd is not running")
		return nil
	}

	return mgr.Stop()
}

// Status shows the status of deployd and all configured services.
func Status(configPath string) error {
	pidFile := filepath.Join(homeDir(configPath), defaultPidDir, "deployd.pid")
	mgr := process.NewManager(pidFile)

	fmt.Printf("deployd: %s\n", mgr.Status())

	cfgPath := configPath
	if cfgPath == "" {
		cfgPath = filepath.Join(homeDir(configPath), "config.yaml")
	}
	if _, err := os.Stat(cfgPath); os.IsNotExist(err) {
		return nil
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		return err
	}

	for _, svc := range cfg.Services {
		svcPIDFile := filepath.Join(homeDir(configPath), defaultPidDir, svc.Name+".pid")
		svcMgr := process.NewManager(svcPIDFile)
		fmt.Printf("  %-30s %s\n", svc.Name, svcMgr.Status())
	}

	return nil
}

// Logs prints the contents of a log file.
// If tail > 0, shows only the last N lines.
// If follow is true, tails the file in real-time (like tail -f).
func Logs(serviceName, configPath, logFile string, tail int, follow bool) error {
	var lf string
	if logFile != "" {
		lf = logFile
	} else {
		logDir := filepath.Join(homeDir(configPath), ".deployd")
		if serviceName != "" {
			lf = filepath.Join(logDir, "services", serviceName+".log")
		} else {
			lf = filepath.Join(logDir, daemonLogName)
		}
	}

	// Check if file exists
	if _, err := os.Stat(lf); os.IsNotExist(err) {
		fmt.Println("no logs found")
		return nil
	}

	if follow {
		return tailFollow(lf, tail)
	}

	data, err := os.ReadFile(lf)
	if err != nil {
		return err
	}

	if tail > 0 {
		lines := bytes.Split(data, []byte("\n"))
		if len(lines) > tail {
			lines = lines[len(lines)-tail:]
		}
		fmt.Print(string(bytes.Join(lines, []byte("\n"))))
	} else {
		fmt.Print(string(data))
	}
	return nil
}

// tailFollow tails a log file in real-time, optionally starting from line N.
func tailFollow(path string, tail int) error {
	// First show last N lines if requested
	if tail > 0 {
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		lines := bytes.Split(data, []byte("\n"))
		if len(lines) > tail {
			lines = lines[len(lines)-tail:]
		}
		fmt.Print(string(bytes.Join(lines, []byte("\n"))))
	}

	// Open file and seek to end
	f, err := os.OpenFile(path, os.O_RDONLY, 0644)
	if err != nil {
		return err
	}
	defer f.Close()

	// Seek to end
	stat, _ := f.Stat()
	if _, err := f.Seek(stat.Size(), io.SeekStart); err != nil {
		return err
	}

	buf := make([]byte, 4096)
	var pending []byte
	for {
		n, err := f.Read(buf)
		if n > 0 {
			pending = append(pending, buf[:n]...)
			lines := bytes.Split(pending, []byte("\n"))
			// Keep last incomplete line in pending
			pending = lines[len(lines)-1]
			for _, line := range lines[:len(lines)-1] {
				if len(line) > 0 {
					fmt.Println(string(line))
				}
			}
		}
		if err != nil {
			if err == io.EOF {
				err = nil
				continue
			}
			return err
		}
	}
}

// TriggerDeploy manually triggers deployment for a service.
func TriggerDeploy(serviceName, configPath string) error {
	cfgPath := configPath
	if cfgPath == "" {
		cfgPath = filepath.Join(homeDir(configPath), "config.yaml")
	}
	cfg, err := config.Load(cfgPath)
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
	notifier := buildNotifier(cfg, "")
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

func homeDir(_ string) string {
	h, _ := os.UserHomeDir()
	return h
}

// buildNotifier creates a Notifier from config.
// Always includes authorEmail as the default recipient.
// notifications.To are additional recipients appended to the list.
// Returns nil only if no notification provider (SMTP or Resend) is configured.
func buildNotifier(cfg *config.AppConfig, authorEmail string) *notify.Notifier {
	hasSMTP := cfg != nil && cfg.SMTP.Host != ""
	hasResend := cfg != nil && cfg.Resend.APIKey != ""
	if !hasSMTP && !hasResend {
		return nil
	}
	recipients := []string{authorEmail}
	recipients = append(recipients, cfg.Notifications.To...)
	return notify.New(
		cfg.SMTP.Host,
		cfg.SMTP.Port,
		cfg.SMTP.Username,
		cfg.SMTP.Token,
		cfg.SMTP.TLS,
		cfg.Resend.APIKey,
		cfg.Resend.From,
		recipients,
	)
}
