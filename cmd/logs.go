package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var logsCmd = &cobra.Command{
	Use:   "logs [service_name]",
	Short: "View deployd or service logs",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if len(args) > 0 {
			fmt.Printf("showing logs for %s...\n", args[0])
		} else {
			fmt.Println("showing deployd logs...")
		}
		return nil
	},
}

func init() {
	rootCmd.AddCommand(logsCmd)
}
