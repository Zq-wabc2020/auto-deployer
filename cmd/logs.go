package cmd

import (
	"github.com/auto-deployer/auto-deployer/internal/daemon"
	"github.com/spf13/cobra"
)

func init() {
	logsCmd.Flags().StringVarP(&configFile, "config", "c", "", "config file path")
	logsCmd.Flags().StringVarP(&logFile, "file", "f", "", "log file path")
	logsCmd.Flags().IntVarP(&logTail, "tail", "n", 0, "show last N lines (default: all)")
	logsCmd.Flags().BoolVarP(&logFollow, "follow", "t", false, "follow log output (tail -f)")
	rootCmd.AddCommand(logsCmd)
}

var logFile string
var logTail int
var logFollow bool

var logsCmd = &cobra.Command{
	Use:   "logs [service_name]",
	Short: "View deployd or service logs",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		var name string
		if len(args) > 0 {
			name = args[0]
		}
		return daemon.Logs(name, configFile, logFile, logTail, logFollow)
	},
}
