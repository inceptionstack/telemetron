// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"fmt"
	"time"

	"github.com/inceptionstack/clawtello/internal/collectorapi"
	"github.com/inceptionstack/clawtello/internal/config"
	"github.com/inceptionstack/clawtello/internal/service"
	"github.com/inceptionstack/clawtello/internal/status"
	"github.com/inceptionstack/clawtello/internal/telemetry"
	"github.com/spf13/cobra"
)

func newStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show service status without hitting the network",
		RunE: func(cmd *cobra.Command, args []string) error {
			if disabled, name, _ := telemetry.IsDisabled(); disabled {
				fmt.Fprintf(cmd.OutOrStdout(), "telemetry:\tdisabled (via %s)\n", name)
				return nil
			}
			svcStatus, err := service.New().ProbeStatus()
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "unit:\t\t%s\n", svcStatus.Detail)

			cfg, err := config.Load(config.LoadOptions{
				ConfigPath: configPath,
				Overrides:  map[string]any{"log_level": logLevel},
			})
			if err == nil {
				snap, _ := status.New(cfg.Paths.StatusFile).Read()
				fmt.Fprintf(cmd.OutOrStdout(), "mode:\t\t%s\n", cfg.Mode)
				fmt.Fprintf(cmd.OutOrStdout(), "endpoint:\t%s\n", cfg.Endpoint)
				fmt.Fprintf(cmd.OutOrStdout(), "deployment_id:\t%s\n", cfg.Declared.DeploymentID)
				fmt.Fprintf(cmd.OutOrStdout(), "last flush:\t%s\n", renderWhen(snap.LastFlushAt, snap.LastFlushMetric))
				fmt.Fprintf(cmd.OutOrStdout(), "last heartbeat:\t%s\n", renderWhen(snap.LastHeartbeatAt, 0))
				fmt.Fprintf(cmd.OutOrStdout(), "dropped batches:\t%d\n", snap.DroppedBatches)

				collector, collectorErr := collectorapi.New(cfg.Collectors[cfg.Mode], status.New(cfg.Paths.StatusFile), cfg)
				if collectorErr == nil {
					if reporter, ok := collector.(collectorapi.StatusReporter); ok {
						for _, line := range reporter.ReportStatus(context.Background()) {
							fmt.Fprintf(cmd.OutOrStdout(), "%s:\t%s\n", line.Label, line.Value)
						}
					}
				}
			} else if svcStatus.Installed {
				return err
			}

			if !svcStatus.Active {
				return fmt.Errorf("clawtello is not active")
			}
			return nil
		},
	}
}

func renderWhen(ts time.Time, metrics int) string {
	if ts.IsZero() {
		return "never"
	}
	rendered := fmt.Sprintf("%s (%s ago", ts.UTC().Format("2006-01-02 15:04:05 UTC"), time.Since(ts).Round(time.Second))
	if metrics > 0 {
		rendered += fmt.Sprintf(", %d metrics", metrics)
	}
	return rendered + ")"
}
