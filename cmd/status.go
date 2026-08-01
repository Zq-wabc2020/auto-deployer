package cmd

import (

	"github.com/auto-deployer/auto-deployer/internal/daemon"
	"github.com/spf13/cobra"
)


func init() {
	statusCmd.Flags().StringVarP(&configFile, "config", "c", "", "config file path")
	rootCmd.AddCommand(statusCmd)
}

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show deployd and all services status",
	RunE: func(cmd *cobra.Command, args []string) error {
		return daemon.Status(configFile)
	},
}