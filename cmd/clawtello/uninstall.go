// SPDX-License-Identifier: Apache-2.0

package main

import (
	"errors"

	"github.com/inceptionstack/clawtello/internal/service"
	"github.com/spf13/cobra"
)

func newUninstallCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "uninstall",
		Short: "Remove the service unit",
		RunE: func(cmd *cobra.Command, args []string) error {
			err := service.New().Uninstall()
			if errors.Is(err, service.ErrUnsupported) {
				return err
			}
			return err
		},
	}
}
