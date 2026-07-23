package cmd

import (
	"fmt"
	"os"

	"auto-deployer/internal/config"
	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(configCmd)
}

var configCmd = &cobra.Command{
	Use:   "config",
	Short: "Interactive configuration wizard",
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Fprintln(os.Stdout, "Starting configuration wizard...")
		fmt.Fprintln(os.Stdout, "Config file: config.yaml\n")
		return config.RunWizard(os.Stdout, os.Stdin, "config.yaml")
	},
}
