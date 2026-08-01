package cmd

import (

	"github.com/auto-deployer/auto-deployer/internal/daemon"
	"github.com/spf13/cobra"
)


func init() {
	stopCmd.Flags().StringVarP(&configFile, "config", "c", "", "config file path")
	rootCmd.AddCommand(stopCmd)
}

var stopCmd = &cobra.Command{
	Use:   "stop",
	Short: "Stop the deployd daemon",
	RunE: func(cmd *cobra.Command, args []string) error {
		return daemon.Stop(configFile)
	},
}