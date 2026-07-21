package cmd

import (
	"fmt"
	"os"

	"auto-deployer/internal/config"
	"github.com/spf13/cobra"
)

var configCmd = &cobra.Command{
	Use:   "config",
	Short: "Interactive configuration wizard",
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Fprintf(os.Stdout, "Starting configuration wizard...\n")
		fmt.Fprintf(os.Stdout, "Config file: config.yaml\n\n")
		return config.RunWizard(os.Stdout, os.Stdin, "config.yaml")
	},
}

func init() {
	rootCmd.AddCommand(configCmd)
}
