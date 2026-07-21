package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show deployd and all services status",
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Println("checking status...")
		return nil
	},
}

func init() {
	rootCmd.AddCommand(statusCmd)
}
