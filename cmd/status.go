package cmd

import (
	"fmt"
	"time"

	"github.com/auto-deployer/auto-deployer/internal/config"
	"github.com/auto-deployer/auto-deployer/internal/daemon"
	"github.com/auto-deployer/auto-deployer/internal/runstate"
	"github.com/auto-deployer/auto-deployer/internal/term"
	"github.com/spf13/cobra"
)

func init() {
	statusCmd.Flags().StringVarP(&configFile, "config", "c", "", "config file path")
	rootCmd.AddCommand(statusCmd)
}

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "查看所有工作项最后执行状态",
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Printf("deployd: %s\n", daemon.DaemonStatus())

		path := configFile
		if path == "" {
			path = config.DefaultConfig()
		}
		if path == "" {
			return nil
		}
		cfg, err := config.Load(path)
		if err != nil {
			return fmt.Errorf("failed to load config: %w", err)
		}
		for i := range cfg.Pipelines {
			p := &cfg.Pipelines[i]
			st := runstate.StaleRecover(p.Name) // running 且锁空 → 就地改写 failed
			fmt.Printf("  %-30s %s\n", p.Name, pipelineStatusLine(st))
		}
		return nil
	},
}

// pipelineStatusLine 拼一行状态摘要：彩色状态 + 触发方式 + 耗时 + commit + 失败节点。
func pipelineStatusLine(st runstate.RunState) string {
	if st.State == "" {
		return term.Colorize("never") // 从未执行
	}
	line := term.Colorize(st.State)
	if st.Trigger != "" {
		line += "  " + st.Trigger
	}
	if st.FinishedAt != nil {
		line += "  " + st.FinishedAt.Sub(st.StartedAt).Round(time.Second).String()
	}
	if len(st.Commit) >= 7 {
		line += "  " + st.Commit[:7]
	}
	if st.FailedStage != "" {
		line += "  失败节点: " + st.FailedStage
	}
	return line
}
