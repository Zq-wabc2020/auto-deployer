package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"

	"github.com/auto-deployer/auto-deployer/internal/config"
	"github.com/auto-deployer/auto-deployer/internal/deploy"
	"github.com/auto-deployer/auto-deployer/internal/deploylock"
	"github.com/auto-deployer/auto-deployer/internal/registry"
	"github.com/spf13/cobra"
)

func init() {
	deployCmd.Flags().Bool("no-fork", false, "Run in foreground (no background fork)")
	deployCmd.Flags().Bool("locked", false, "internal: deploy lock already held by an inherited fd")
	if f := deployCmd.Flags().Lookup("locked"); f != nil {
		f.Hidden = true
	}
	deployCmd.Flags().StringVarP(&configFile, "config", "c", "", "config file path")
	rootCmd.AddCommand(deployCmd)
}

var deployCmd = &cobra.Command{
	Use:     "deploy <service_name>",
	Aliases: []string{"dep"},
	Short:   "Manually trigger full deployment for a service",
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		serviceName := args[0]
		noFork, _ := cmd.Flags().GetBool("no-fork")
		locked, _ := cmd.Flags().GetBool("locked")

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

		d, err := registry.Get(svc.Type)
		if err != nil {
			return err
		}

		// Manual deploys have the lowest priority: if the service is already
		// deploying (webhook queue busy or another manual deploy running), fail
		// fast instead of waiting or queuing.
		if noFork {
			// --no-fork is used both by the fork child (with --locked, lock
			// already inherited) and by explicit foreground runs (must acquire
			// the lock themselves).
			if !locked {
				lock, err := deploylock.TryAcquire(serviceName)
				if err != nil {
					return fmt.Errorf("服务 %s 正在部署中，请勿重复操作", serviceName)
				}
				defer lock.Release()
			} else {
				// Lock inherited from the fork parent (ExtraFiles). Release it
				// explicitly after Deploy: the deployed service also inherits
				// the fd and would hold the lock forever if we relied on this
				// process exiting.
				defer func() {
					if err := deploylock.ReleaseInherited(); err != nil {
						fmt.Fprintf(os.Stderr, "[deploy] release inherited lock: %v\n", err)
					}
				}()
			}
			_, err := deploy.Deploy(cmd.Context(), svc, cfg, d, nil)
			return err
		}

		// Fork to background on Linux (survives SSH disconnect).
		if runtime.GOOS == "linux" {
			exe, err := os.Executable()
			if err != nil {
				return fmt.Errorf("failed to get executable path: %w", err)
			}
			return forkBackground(exe, path, serviceName, []string{"deploy"})
		}

		// On macOS, run in foreground with the deploy lock.
		lock, err := deploylock.TryAcquire(serviceName)
		if err != nil {
			return fmt.Errorf("服务 %s 正在部署中，请勿重复操作", serviceName)
		}
		defer lock.Release()
		_, err = deploy.Deploy(cmd.Context(), svc, cfg, d, nil)
		return err
	},
}

// forkBackground 用 setsid 后台执行 exe childArgs（如 ["deploy"] 或 ["service","start"]），
// 继承 deploy lock fd，日志写服务日志。复用于 deploy / svc start / svc restart。
// 父进程以 TryAcquire 抢锁做 fail-fast 反馈，然后经 ExtraFiles 把锁 fd 传给子进程；
// 父进程 Close（不解锁）——flock 锁跨重复 fd 共享，子进程持有至退出。子进程跑 --locked
// 跳过自己的 TryAcquire，退出前 ReleaseInherited 显式释放（部署的服务也会继承该 fd）。
func forkBackground(exe, configPath, serviceName string, childArgs []string) error {
	lock, err := deploylock.TryAcquire(serviceName)
	if err != nil {
		return fmt.Errorf("服务 %s 正在启动/部署中，请勿重复操作", serviceName)
	}

	homeDir, _ := os.UserHomeDir()
	logDir := filepath.Join(homeDir, ".deployd", "services")
	if err := os.MkdirAll(logDir, 0755); err != nil {
		lock.Release()
		return fmt.Errorf("failed to create log directory: %w", err)
	}
	logPath := filepath.Join(logDir, fmt.Sprintf("%s.log", serviceName))
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		lock.Release()
		return fmt.Errorf("failed to open log file: %w", err)
	}
	defer logFile.Close()

	// 子进程参数：<childArgs...> --no-fork --locked -c <configPath> <serviceName>
	args := append(append([]string{}, childArgs...), "--no-fork", "--locked", "-c", configPath, serviceName)
	c := exec.Command(exe, args...)
	// 告知子进程哪个 fd 持有继承来的 deploy lock（ExtraFiles[0] = fd 3），
	// 供其在启动服务前标记 close-on-exec。
	c.Env = append(os.Environ(), "DEPLOYD_LOCK_FD=3")
	c.Stdout = logFile
	c.Stderr = logFile
	c.Stdin = nil
	c.ExtraFiles = []*os.File{lock.File()}
	c.SysProcAttr = &syscall.SysProcAttr{Setsid: true}

	if err := c.Start(); err != nil {
		lock.Release()
		return fmt.Errorf("failed to start background process: %w", err)
	}

	// 子进程已继承锁 fd；父进程分离且不解锁。
	lock.Close()

	fmt.Printf("[%s] %s 后台启动 (pid: %d)，用 deployd status 查看\n", strings.Join(childArgs, " "), serviceName, c.Process.Pid)
	return nil
}
