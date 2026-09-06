package cmd

import (
	"fmt"

	"github.com/auto-deployer/auto-deployer/internal/deploylock"
	"github.com/auto-deployer/auto-deployer/internal/servstate"
	"github.com/spf13/cobra"
)

// cancelCmd 实现 `deployd cancel <name>`：向正在部署/启动中的服务发取消信号。
// 未持锁（无进行中的部署/启动）时仅提示，不报错。
var cancelCmd = &cobra.Command{
	Use:   "cancel <service_name>",
	Short: "取消正在部署/启动中的服务",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return runCancel(args[0])
	},
}

func init() { rootCmd.AddCommand(cancelCmd) }

// runCancel 检查锁：未持锁 → 提示无需取消；持锁 → 写取消 sentinel（watchCancel 命中后中止）。
func runCancel(serviceName string) error {
	if !deploylock.IsHeld(serviceName) {
		fmt.Printf("服务 %s 未在部署/启动中，无需取消\n", serviceName)
		return nil
	}
	if err := servstate.WriteCancel(serviceName); err != nil {
		return fmt.Errorf("写取消信号失败: %w", err)
	}
	fmt.Printf("已向 %s 发出取消信号，部署/启动进程将中止\n", serviceName)
	return nil
}
