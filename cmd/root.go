package cmd

import (
	"context"
	"fmt"
	"os"
	"runtime"

	"github.com/spf13/cobra"

	"github.com/auto-deployer/auto-deployer/internal/config"
	"github.com/auto-deployer/auto-deployer/internal/deploy"
	"github.com/auto-deployer/auto-deployer/internal/deploylock"
	"github.com/auto-deployer/auto-deployer/internal/registry"
)

var rootCmd = &cobra.Command{
	Use:   "deployd",
	Short: "Automated deployment daemon",
	Long:  "A CLI tool that runs as a background daemon, receives webhooks, and automates service deployment.",
}

var configFile string

var serviceCmd = &cobra.Command{
	Use:     "service",
	Aliases: []string{"svc"},
	Short:   "Manage individual services",
	// Short-flag form: `deployd svc <name> -s|-t|-r` (no flag = status).
	// The `service start/stop/restart <name>` subcommands are kept as the
	// long form.
	RunE: runSvcShortFlags,
	Args: cobra.ExactArgs(1),
}

func init() {
	serviceCmd.Flags().BoolP("start", "s", false, "start the service (short for `service start`)")
	serviceCmd.Flags().BoolP("stop", "t", false, "stop the service (terminate; short for `service stop`)")
	serviceCmd.Flags().BoolP("restart", "r", false, "restart the service (short for `service restart`)")
	rootCmd.AddCommand(serviceCmd)
}

// runSvcShortFlags dispatches `svc <name> {-s|-t|-r}` to the service
// lifecycle; with no flag it prints the service status.
//
// start/restart 走非阻塞：Linux fork 到 `service start/restart` 长格式子进程
// （子进程带 --no-fork --locked）；macOS 前台 + TryAcquire。stop 始终前台
// （ServiceStop 内部已清 state）。
func runSvcShortFlags(cmd *cobra.Command, args []string) error {
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
			svc = &cfg.Services[i]
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

	ctx := context.Background()
	switch {
	case boolFlag(cmd, "start"):
		return runSvcStart(ctx, path, serviceName, svc, d)
	case boolFlag(cmd, "stop"):
		return deploy.ServiceStop(ctx, svc, d)
	case boolFlag(cmd, "restart"):
		return runSvcRestart(ctx, path, serviceName, svc, d)
	default:
		st, err := deploy.GetServiceStatusRich(ctx, svc, d)
		if err != nil {
			return err
		}
		fmt.Printf("%s: %s\n", svc.Name, st)
		return nil
	}
}

// runSvcStart 实现 `svc <name> -s`：Linux fork 到 `service start` 长格式子进程
// （子进程 --no-fork --locked）；macOS 前台 + TryAcquire + ServiceStart。
func runSvcStart(ctx context.Context, configPath, serviceName string, svc *config.ServiceConfig, d deploy.Deployer) error {
	if runtime.GOOS == "linux" {
		exe, err := os.Executable()
		if err != nil {
			return fmt.Errorf("failed to get executable path: %w", err)
		}
		return forkBackground(exe, configPath, serviceName, []string{"service", "start"})
	}
	lock, err := deploylock.TryAcquire(serviceName)
	if err != nil {
		return fmt.Errorf("服务 %s 正在启动/部署中，请勿重复操作", serviceName)
	}
	defer lock.Release()
	return deploy.ServiceStart(ctx, svc, d)
}

// runSvcRestart 实现 `svc <name> -r`：Linux fork 到 `service restart` 长格式子进程；
// macOS 前台 + TryAcquire + ServiceRestart。
func runSvcRestart(ctx context.Context, configPath, serviceName string, svc *config.ServiceConfig, d deploy.Deployer) error {
	if runtime.GOOS == "linux" {
		exe, err := os.Executable()
		if err != nil {
			return fmt.Errorf("failed to get executable path: %w", err)
		}
		return forkBackground(exe, configPath, serviceName, []string{"service", "restart"})
	}
	lock, err := deploylock.TryAcquire(serviceName)
	if err != nil {
		return fmt.Errorf("服务 %s 正在启动/部署中，请勿重复操作", serviceName)
	}
	defer lock.Release()
	return deploy.ServiceRestart(ctx, svc, d)
}

func boolFlag(cmd *cobra.Command, name string) bool {
	v, _ := cmd.Flags().GetBool(name)
	return v
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
