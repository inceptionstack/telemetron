package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var (
	configPath string
	logLevel   string
	version    = "dev"
)

func main() {
	root := newRootCmd()
	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func newRootCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "lokiotel",
		Short: "Loki OTel sidecar",
	}
	cmd.PersistentFlags().StringVar(&configPath, "config", "/etc/lokiotel/config.yaml", "config file path")
	cmd.PersistentFlags().StringVar(&logLevel, "log-level", "info", "trace|debug|info|warn|error")
	cmd.AddCommand(newInstallCmd(), newUninstallCmd(), newStartCmd(), newStatusCmd(), newVersionCmd())
	return cmd
}

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print build version",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Fprintln(cmd.OutOrStdout(), version)
		},
	}
}
