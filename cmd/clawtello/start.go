// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/inceptionstack/clawtello/internal/collectorapi"
	"github.com/inceptionstack/clawtello/internal/config"
	"github.com/inceptionstack/clawtello/internal/otlp"
	"github.com/inceptionstack/clawtello/internal/status"
	"github.com/inceptionstack/clawtello/internal/telemetry"
	"github.com/spf13/cobra"
)

func newStartCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "start",
		Short: "Run in the foreground",
		RunE: func(cmd *cobra.Command, args []string) error {
			// Honour user-facing opt-out BEFORE reading config, token,
			// or opening any sockets. See internal/telemetry/optout.go.
			if disabled, name, value := telemetry.IsDisabled(); disabled {
				logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
				logger.Info("telemetry disabled via environment; exiting",
					"var", name, "value", value)
				return nil
			}
			cfg, err := config.Load(config.LoadOptions{
				ConfigPath: configPath,
				Overrides:  map[string]any{"log_level": logLevel},
			})
			if err != nil {
				return err
			}
			token, err := cfg.Token()
			if err != nil {
				return err
			}
			logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
			store := status.New(cfg.Paths.StatusFile)
			collector, err := collectorapi.New(cfg.Collectors[cfg.Mode], store, cfg)
			if err != nil {
				return err
			}
			exporter := otlp.NewExporter(cfg.Endpoint, token, map[string]string{
				"deployment_id": cfg.Declared.DeploymentID,
				"tier":          cfg.Declared.Tier,
				"environment":   cfg.Declared.Environment,
				"pack_version":  cfg.Declared.PackVersion,
			}, nil)
			sink := otlp.NewSink(exporter, logger, store, cfg.Mode)
			ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
			defer cancel()
			return collector.Start(ctx, sink)
		},
	}
}
