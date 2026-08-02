package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/auto-deployer/auto-deployer/internal/config"
	"github.com/auto-deployer/auto-deployer/internal/process"
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

		for _, svc := range cfg.Services {
			svcPIDFile := filepath.Join(home, ".deployd", "run", svc.Name+".pid")
			svcMgr := process.NewManager(svcPIDFile)
			fmt.Printf("  %-30s %s\n", svc.Name, svcMgr.Status())
		}

		return nil
	},
}
