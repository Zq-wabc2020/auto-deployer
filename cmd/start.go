package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"auto-deployer/internal/daemon"
	"github.com/spf13/cobra"
)

var startCmd = &cobra.Command{
	Use:   "start",
	Short: "Start the deployd daemon in background",
	RunE: func(cmd *cobra.Command, args []string) error {
		configPath := configFile
		if configPath == "" {
			home, _ := os.UserHomeDir()
			configPath = filepath.Join(home, "config.yaml")
		}

		if err := daemon.Start(configPath); err != nil {
			return err
		}
		fmt.Fprintln(os.Stdout, "deployd started successfully")
		return nil
	},
}

func init() {
	rootCmd.AddCommand(startCmd)
}
