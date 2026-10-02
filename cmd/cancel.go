package cmd

import (
	"fmt"

	"github.com/auto-deployer/auto-deployer/internal/deploylock"
	"github.com/auto-deployer/auto-deployer/internal/runstate"
	"github.com/spf13/cobra"
)

// cancelCmd 实现 `deployd cancel <name>`：向正在执行/排队中的工作项发取消信号。
// 未持锁（无进行中的执行）时仅提示，不报错。
var cancelCmd = &cobra.Command{
	Use:   "cancel <工作项名>",
	Short: "取消正在执行/排队中的工作项",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return runCancel(args[0])
	},
}

func init() { rootCmd.AddCommand(cancelCmd) }

// runCancel 检查锁：未持锁 → 提示无需取消；持锁 → 写取消 sentinel（watchCancel 命中后中止）。
func runCancel(serviceName string) error {
	if !deploylock.IsHeld(serviceName) {
		fmt.Printf("工作项 %s 未在执行/排队中，无需取消\n", serviceName)
		return nil
	}
	if err := runstate.WriteCancel(serviceName); err != nil {
		return fmt.Errorf("写取消信号失败: %w", err)
	}
	fmt.Printf("已向 %s 发出取消信号，执行进程将中止\n", serviceName)
	return nil
}
