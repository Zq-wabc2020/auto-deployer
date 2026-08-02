package cmd

import (
	"context"
	"fmt"
	"os"

	"github.com/auto-deployer/auto-deployer/internal/config"
	"github.com/auto-deployer/auto-deployer/internal/deploy"
	"github.com/auto-deployer/auto-deployer/plugins/springboot"
	"github.com/spf13/cobra"
)

func init() {
	deployCmd.Flags().StringVarP(&configFile, "config", "c", "", "config file path")
	rootCmd.AddCommand(deployCmd)
}

var deployCmd = &cobra.Command{
	Use:   "deploy <service_name>",
	Short: "Manually trigger full deployment for a service",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		serviceName := args[0]

		path := configFile
		if path == "" {
			path = config.DefaultConfig()
		}
		if path == "" {
			return fmt.Errorf("config file not found. Run 'deployd config' to create one, or use -c to specify path")
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

		var d deploy.Deployer
		switch svc.Type {
		case "springboot":
			d = springboot.New()
		default:
			return fmt.Errorf("unknown service type %q (supported: springboot)", svc.Type)
		}

		_, err = deploy.Deploy(context.Background(), svc, cfg, d)
		return err
	},
}
