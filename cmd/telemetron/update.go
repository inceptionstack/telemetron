// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/inceptionstack/telemetron/internal/updater"
	"github.com/spf13/cobra"
)

func newUpdateCmd() *cobra.Command {
	var dryRun bool

	cmd := &cobra.Command{
		Use:   "update",
		Short: "Check for a new version and update in place",
		Long: `Check GitHub for the latest telemetron release. If a newer
version is available, download it, verify its checksum, and
replace the running binary.

After a successful update the systemd service is restarted
automatically (requires root). Use --dry-run to check without
applying.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if updater.ShouldSkip(version) {
				return fmt.Errorf("cannot update a dev/snapshot build (version=%q)", version)
			}

			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()

			fmt.Fprintf(cmd.OutOrStdout(), "current version: %s\n", version)
			fmt.Fprintf(cmd.OutOrStdout(), "checking for updates...\n")

			rel, err := updater.FetchLatest(ctx, nil, "")
			if err != nil {
				return fmt.Errorf("check failed: %w", err)
			}

			if !updater.IsNewerVersion(version, rel.TagName) {
				fmt.Fprintf(cmd.OutOrStdout(), "already up to date (%s)\n", rel.TagName)
				return nil
			}

			fmt.Fprintf(cmd.OutOrStdout(), "new version available: %s\n", rel.TagName)

			if dryRun {
				fmt.Fprintln(cmd.OutOrStdout(), "(dry-run: skipping download and install)")
				return nil
			}

			up := updater.NewForManualUpdate(version, updater.ResolveBinaryPath())

			fmt.Fprintf(cmd.OutOrStdout(), "downloading %s...\n", rel.TagName)
			if err := up.ApplyRelease(ctx, rel); err != nil {
				return fmt.Errorf("update failed: %w", err)
			}

			fmt.Fprintf(cmd.OutOrStdout(), "updated %s → %s\n", version, rel.TagName)

			// Try to restart the service if running as root
			if os.Geteuid() == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "restarting telemetron service...")
				if err := restartService(ctx); err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "warning: restart failed: %v\n", err)
					fmt.Fprintln(cmd.OutOrStdout(), "please restart manually: systemctl restart telemetron")
				} else {
					fmt.Fprintln(cmd.OutOrStdout(), "service restarted successfully")
				}
			} else {
				fmt.Fprintln(cmd.OutOrStdout(), "restart the service to use the new version:")
				fmt.Fprintln(cmd.OutOrStdout(), "  sudo systemctl restart telemetron")
			}

			return nil
		},
	}

	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "check for updates without applying")
	return cmd
}

func restartService(ctx context.Context) error {
	cmd := exec.CommandContext(ctx, "systemctl", "restart", "telemetron.service")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}
