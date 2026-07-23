package cmd

import (
	"fmt"
	"os"

	"auto-deployer/internal/daemon"
	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(deployCmd)
}

var deployCmd = &cobra.Command{
	Use:   "deploy <service_name>",
	Short: "Manually trigger deployment for a service",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Fprintln(os.Stdout, "triggering deployment...")
		return daemon.TriggerDeploy(args[0])
	},
}
