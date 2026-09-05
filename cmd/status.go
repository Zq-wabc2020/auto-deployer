package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/auto-deployer/auto-deployer/internal/config"
	"github.com/auto-deployer/auto-deployer/internal/deploy"
	"github.com/auto-deployer/auto-deployer/internal/process"
	"github.com/auto-deployer/auto-deployer/internal/registry"
	"github.com/spf13/cobra"
)

func init() {
	statusCmd.Flags().StringVarP(&configFile, "config", "c", "", "config file path")
	rootCmd.AddCommand(statusCmd)
}

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show deployd and all services status",
	RunE: func(cmd *cobra.Command, args []string) error {
		path := configFile
		if path == "" {
			path = config.DefaultConfig()
		}

		// Check daemon status
		home, _ := os.UserHomeDir()
		pidFile := filepath.Join(home, ".deployd", "run", "deployd.pid")
		mgr := process.NewManager(pidFile)
		fmt.Printf("deployd: %s\n", mgr.Status())

		if path == "" {
			return nil
		}

		cfg, err := config.Load(path)
		if err != nil {
			return fmt.Errorf("failed to load config: %w", err)
		}

		for i := range cfg.Services {
			fmt.Printf("  %-30s %s\n", cfg.Services[i].Name, serviceStatus(&cfg.Services[i]))
		}

		return nil
	},
}

// serviceStatus reports a service's status through its deployment model --
// pid file for process models, health URL for static, docker ps for docker.
// Reading the pid file directly here would always show static as "stopped".
// 状态串已由 GetServiceStatusRich 上色（非 TTY 时为纯文本），此处直接打印。
func serviceStatus(svc *config.ServiceConfig) string {
	d, err := registry.Get(svc.Type)
	if err != nil {
		return "unknown (" + err.Error() + ")"
	}
	st, err := deploy.GetServiceStatusRich(context.Background(), svc, d)
	if err != nil {
		return "unknown (" + err.Error() + ")"
	}
	return st
}
