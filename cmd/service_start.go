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
	serviceStartCmd.Flags().Bool("no-fork", false, "前台运行（不后台 fork）")
	serviceStartCmd.Flags().Bool("locked", false, "internal: deploy lock already held by an inherited fd")
	if f := serviceStartCmd.Flags().Lookup("locked"); f != nil {
		f.Hidden = true
	}
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

		noFork, _ := cmd.Flags().GetBool("no-fork")
		locked, _ := cmd.Flags().GetBool("locked")
		if noFork {
			// --no-fork 既被 fork 子进程用（带 --locked，锁已继承），也被显式前台运行用（需自己抢锁）。
			if !locked {
				lock, err := deploylock.TryAcquire(serviceName)
				if err != nil {
					return fmt.Errorf("服务 %s 正在启动/部署中，请勿重复操作", serviceName)
				}
				defer lock.Release()
			} else {
				// 锁自 fork 父进程继承（ExtraFiles）；Start 后显式释放：部署的服务也继承该 fd。
				defer func() {
					if err := deploylock.ReleaseInherited(); err != nil {
						fmt.Fprintf(os.Stderr, "[service start] release inherited lock: %v\n", err)
					}
				}()
			}
			return deploy.ServiceStart(cmd.Context(), svc, d)
		}

		// Linux fork 到后台（SSH 断开存活）。
		if runtime.GOOS == "linux" {
			exe, err := os.Executable()
			if err != nil {
				return fmt.Errorf("failed to get executable path: %w", err)
			}
			return forkBackground(exe, path, serviceName, []string{"service", "start"})
		}

		// macOS 前台 + 抢锁。
		lock, err := deploylock.TryAcquire(serviceName)
		if err != nil {
			return fmt.Errorf("服务 %s 正在启动/部署中，请勿重复操作", serviceName)
		}
		defer lock.Release()
		return deploy.ServiceStart(cmd.Context(), svc, d)
	},
}
