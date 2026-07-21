package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var deployCmd = &cobra.Command{
	Use:   "deploy <service_name>",
	Short: "Manually trigger deployment for a service",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Printf("triggering deploy for %s...\n", args[0])
		return nil
	},
}

func init() {
	rootCmd.AddCommand(deployCmd)
}
