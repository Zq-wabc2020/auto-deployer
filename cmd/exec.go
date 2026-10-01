package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/auto-deployer/auto-deployer/internal/config"
	"github.com/auto-deployer/auto-deployer/internal/deploylock"
	"github.com/auto-deployer/auto-deployer/internal/pipeline"
	"github.com/spf13/cobra"
)

func init() {
	// interspersed=false：name 之后的一切都是 key=value 参数（--foo=bar 形式），
	// 不再被 cobra 当未知 flag 拒绝
	execCmd.Flags().SetInterspersed(false)
	execCmd.Flags().StringVarP(&configFile, "config", "c", "", "config file path")
	rootCmd.AddCommand(execCmd)
}

var execCmd = &cobra.Command{
	Use:   "exec <pipeline_name> [--key=value ...]",
	Short: "前台执行工作项（实时输出，Ctrl+C 取消）",
	Args:  cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name, kv, err := parseExecArgs(args)
		if err != nil {
			return err
		}
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
		pl := config.FindPipeline(cfg, name)
		if pl == nil {
			return fmt.Errorf("工作项 %q 不在配置中", name)
		}
		// 手动执行优先级最低：队列忙（pending 或 in-flight）直接拒绝，不排队
		lock, err := deploylock.TryAcquire(name)
		if err != nil {
			return fmt.Errorf("工作项 %s 正在执行或排队中，请勿重复操作", name)
		}
		defer lock.Release()

		res := pipeline.Run(cmd.Context(), pl, pipeline.Options{
			Trigger: "manual", Args: kv, Console: os.Stdout, Cfg: cfg,
		})
		if res.Status != "success" {
			os.Exit(1) // 失败/取消非零退出码，供脚本判断
		}
		return nil
	},
}

// parseExecArgs 解析 exec 的位置参数：args[0]=工作项名，其余为 --k=v / k=v。
func parseExecArgs(args []string) (string, map[string]string, error) {
	if len(args) == 0 {
		return "", nil, fmt.Errorf("缺少工作项名")
	}
	kv := map[string]string{}
	for _, a := range args[1:] {
		a = strings.TrimPrefix(a, "--")
		k, v, ok := strings.Cut(a, "=")
		if !ok || k == "" {
			return "", nil, fmt.Errorf("参数 %q 需要 key=value 形式", a)
		}
		kv[k] = v
	}
	return args[0], kv, nil
}
