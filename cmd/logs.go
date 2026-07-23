package cmd

import (
	"auto-deployer/internal/daemon"
	"github.com/spf13/cobra"
)

func init() {
	logsCmd.Flags().StringVarP(&configFile, "file", "f", "", "log file path")
	rootCmd.AddCommand(logsCmd)
}

var logsCmd = &cobra.Command{
	Use:   "logs [service_name]",
	Short: "View deployd or service logs",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		var name string
		if len(args) > 0 {
			name = args[0]
		}
		return daemon.Logs(name)
	},
}
