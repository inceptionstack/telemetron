package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/inceptionstack/loki-otl/internal/collectors"
	"github.com/inceptionstack/loki-otl/internal/config"
	"github.com/inceptionstack/loki-otl/internal/otlp"
	"github.com/inceptionstack/loki-otl/internal/status"
	"github.com/spf13/cobra"
)

func newStartCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "start",
		Short: "Run in the foreground",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load(config.LoadOptions{
				ConfigPath: configPath,
				Overrides:  map[string]string{"log_level": logLevel},
			})
			if err != nil {
				return err
			}
			token, err := cfg.Token()
			if err != nil {
				return err
			}
			logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
			store := status.New(cfg.OpenClaw.StatusFile)
			collector, err := collectors.New(cfg, store)
			if err != nil {
				return err
			}
			exporter := otlp.NewExporter(cfg.Endpoint, token, map[string]string{
				"deployment_id": cfg.Declared.DeploymentID,
				"tier":          cfg.Declared.Tier,
				"environment":   cfg.Declared.Environment,
				"pack_version":  cfg.Declared.PackVersion,
			}, nil)
			sink := otlp.NewSink(exporter, logger, store)
			ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
			defer cancel()
			return collector.Start(ctx, sink)
		},
	}
}
