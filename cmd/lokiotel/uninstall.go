package main

import (
	"github.com/inceptionstack/loki-otl/internal/systemd"
	"github.com/spf13/cobra"
)

func newUninstallCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "uninstall",
		Short: "Remove the systemd unit",
		RunE: func(cmd *cobra.Command, args []string) error {
			return systemd.Uninstall()
		},
	}
}
