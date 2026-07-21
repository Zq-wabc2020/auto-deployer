package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var configCmd = &cobra.Command{
	Use:   "config",
	Short: "Interactive configuration wizard",
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Println("opening configuration wizard...")
		fmt.Fprintf(os.Stdout, "Config file: config.yaml\n\n")
		return nil
	},
}

func init() {
	rootCmd.AddCommand(configCmd)
}
