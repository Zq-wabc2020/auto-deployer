package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "deployd",
	Short: "Automated deployment daemon",
	Long:  "A CLI tool that runs as a background daemon, receives webhooks, and automates service deployment.",
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
