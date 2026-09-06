package cmd

import (
	"fmt"
	"os"
	"runtime"

	"github.com/auto-deployer/auto-deployer/internal/config"
	"github.com/auto-deployer/auto-deployer/internal/deploy"
	"github.com/auto-deployer/auto-deployer/internal/deploylock"
	"github.com/auto-deployer/auto-deployer/internal/registry"
	"github.com/spf13/cobra"
)

func init() {
	serviceRestartCmd.Flags().Bool("no-fork", false, "前台运行（不后台 fork）")
	serviceRestartCmd.Flags().Bool("locked", false, "internal: deploy lock already held by an inherited fd")
	if f := serviceRestartCmd.Flags().Lookup("locked"); f != nil {
		f.Hidden = true
	}
	serviceRestartCmd.Flags().StringVarP(&configFile, "config", "c", "", "config file path")
	serviceCmd.AddCommand(serviceRestartCmd)
}

var serviceRestartCmd = &cobra.Command{
	Use:   "restart <service_name>",
	Short: "Restart a service without rebuilding",
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

		noFork, _ := cmd.Flags().GetBool("no-fork")
		locked, _ := cmd.Flags().GetBool("locked")
		if noFork {
			if !locked {
				lock, err := deploylock.TryAcquire(serviceName)
				if err != nil {
					return fmt.Errorf("服务 %s 正在启动/部署中，请勿重复操作", serviceName)
				}
				defer lock.Release()
			} else {
				defer func() {
					if err := deploylock.ReleaseInherited(); err != nil {
						fmt.Fprintf(os.Stderr, "[service restart] release inherited lock: %v\n", err)
					}
				}()
			}
			return deploy.ServiceRestart(cmd.Context(), svc, d)
		}

		if runtime.GOOS == "linux" {
			exe, err := os.Executable()
			if err != nil {
				return fmt.Errorf("failed to get executable path: %w", err)
			}
			return forkBackground(exe, path, serviceName, []string{"service", "restart"})
		}

		// macOS 前台 + 抢锁。
		lock, err := deploylock.TryAcquire(serviceName)
		if err != nil {
			return fmt.Errorf("服务 %s 正在启动/部署中，请勿重复操作", serviceName)
		}
		defer lock.Release()
		return deploy.ServiceRestart(cmd.Context(), svc, d)
	},
}
