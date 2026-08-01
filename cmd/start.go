package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/auto-deployer/auto-deployer/internal/daemon"
	"github.com/spf13/cobra"
)


func init() {
	startCmd.Flags().StringVarP(&configFile, "config", "c", "", "config file path")
	startCmd.Flags().BoolVar(&daemonChild, "daemon-child", false, "internal: run as daemon child process (do not fork)")
	rootCmd.AddCommand(startCmd)
}

var daemonChild bool

var startCmd = &cobra.Command{
	Use:   "start",
	Short: "Start the deployd daemon in background",
	RunE: func(cmd *cobra.Command, args []string) error {
		path := configFile
		if path == "" {
			home, _ := os.UserHomeDir()
			path = filepath.Join(home, "config.yaml")
		}

		if err := daemon.Start(path); err != nil {
			return err
		}
		if !daemonChild {
			fmt.Fprintln(os.Stdout, "deployd started successfully")
		}
		return nil
	},
}
