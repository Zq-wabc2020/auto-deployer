package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var startCmd = &cobra.Command{
	Use:   "start",
	Short: "Start the deployd daemon in background",
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Println("starting deployd...")
		return nil
	},
}

func init() {
	rootCmd.AddCommand(startCmd)
}
