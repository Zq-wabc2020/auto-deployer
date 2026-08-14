package cmd

import (
	"context"
	"fmt"

	"github.com/auto-deployer/auto-deployer/internal/config"
	"github.com/auto-deployer/auto-deployer/internal/deploy"
	"github.com/auto-deployer/auto-deployer/internal/registry"
	"github.com/spf13/cobra"
)

func init() {
	serviceStartCmd.Flags().StringVarP(&configFile, "config", "c", "", "config file path")
	serviceCmd.AddCommand(serviceStartCmd)
}

var serviceStartCmd = &cobra.Command{
	Use:   "start <service_name>",
	Short: "Start a service without rebuilding",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		serviceName := args[0]

		path := configFile
		if path == "" {
			path = config.DefaultConfig()
		}
		if path == "" {
			return fmt.Errorf("config file not found")
		}

		cfg, err := config.Load(path)
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

		d, err := registry.Get(svc.Type)
		if err != nil {
			return err
		}

		return deploy.ServiceStart(context.Background(), svc, d)
	},
}
