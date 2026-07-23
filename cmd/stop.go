package cmd

import (
	"auto-deployer/internal/daemon"
	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(stopCmd)
}

var stopCmd = &cobra.Command{
	Use:   "stop",
	Short: "Stop the deployd daemon",
	RunE: func(cmd *cobra.Command, args []string) error {
		return daemon.Stop()
	},
}
