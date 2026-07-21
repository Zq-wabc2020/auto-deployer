package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var stopCmd = &cobra.Command{
	Use:   "stop",
	Short: "Stop the deployd daemon",
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Println("stopping deployd...")
		return nil
	},
}

func init() {
	rootCmd.AddCommand(stopCmd)
}
