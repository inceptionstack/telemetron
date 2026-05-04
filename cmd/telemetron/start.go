// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/inceptionstack/telemetron/internal/collectorapi"
	"github.com/inceptionstack/telemetron/internal/config"
	"github.com/inceptionstack/telemetron/internal/otlp"
	"github.com/inceptionstack/telemetron/internal/status"
	"github.com/inceptionstack/telemetron/internal/telemetry"
	"github.com/inceptionstack/telemetron/internal/updater"
	"github.com/spf13/cobra"
)

var newOTLPExporter = otlp.NewExporter

func newStartCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "start",
		Short: "Run in the foreground",
		RunE: func(cmd *cobra.Command, args []string) error {
			// Check for rollback FIRST — before config, token, or anything else.
			// If the previous binary update crashed, restore .prev and exit
			// so systemd restarts with the restored binary.
			if updater.IsManagedInstall() {
				earlyLogger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
				if updater.CheckRollback(earlyLogger) {
					os.Exit(updater.ExitCodeUpdate)
				}
			}

			// Honour user-facing opt-out BEFORE reading config, token,
			// or opening any sockets. See internal/telemetry/optout.go.
			if disabled, source, value := telemetry.IsDisabled(); disabled {
				logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
				logger.Info("telemetry disabled; exiting",
					"source", source, "value", value)
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
			exporter := newOTLPExporter(cfg.Endpoint, token, declaredForExporter(cfg, logger), nil)
			sink := otlp.NewSink(exporter, logger, store, cfg.Mode)
			ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
			defer cancel()

			// Start auto-updater in background if this is a managed install
			updateCh := make(chan int, 1)
			if updater.IsManagedInstall() {
				up := updater.New(version, updater.ManagedBinaryPath, logger, store)
				if cfg.AutoUpdate.IsEnabled() {
					go func() {
						if code := up.Run(ctx, cfg.AutoUpdate, cfg.Declared.Tier); code == updater.ExitCodeUpdate {
							logger.Info("auto-update applied, requesting shutdown", slog.Int("exit_code", code))
							updateCh <- code
							cancel()
						}
					}()
				} else {
					// Even with updates disabled, confirm any pending update
					// so the pending flag gets cleared and doesn't cause a
					// false rollback on next restart.
					up.ConfirmIfPending(ctx)
				}
			}

			err = collector.Start(ctx, sink)

			// Check if we're exiting for an update
			select {
			case code := <-updateCh:
				os.Exit(code)
			default:
			}
			return err
		},
	}
}

func declaredForExporter(cfg config.Config, logger *slog.Logger) map[string]string {
	declared := map[string]string{
		"deployment_id": cfg.Declared.DeploymentID,
		"tier":          cfg.Declared.Tier,
		"environment":   cfg.Declared.Environment,
		"pack_version":  cfg.Declared.PackVersion,
	}
	installID, err := readInstallID(setupInstallIDPath)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			logger.Warn("install_id unavailable; continuing without resource attribute", "path", setupInstallIDPath, "error", err)
		}
		return declared
	}
	declared["install_id"] = installID
	return declared
}
