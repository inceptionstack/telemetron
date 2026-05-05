// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/inceptionstack/telemetron/internal/collectorapi"
	"github.com/inceptionstack/telemetron/internal/config"
	"github.com/inceptionstack/telemetron/internal/openclaw"
	"github.com/inceptionstack/telemetron/internal/otlp"
	"github.com/inceptionstack/telemetron/internal/roundhouse"
	"github.com/inceptionstack/telemetron/internal/status"
	"github.com/inceptionstack/telemetron/internal/telemetry"
	"github.com/inceptionstack/telemetron/internal/tier"
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
			// Only primary instance owns update state.
			if updater.IsManagedInstall() {
				earlyLogger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
				if !isSecondaryInstance() && updater.CheckRollback(earlyLogger) {
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

			// Detect tier from AWS APIs on startup (best-effort, upgrades only)
			tier.DetectAndWrite(logger)

			store := status.New(cfg.Paths.StatusFile)
			collector, err := collectorapi.New(cfg.Collectors[cfg.Mode], store, cfg)
			if err != nil {
				return err
			}
			exporter := newOTLPExporter(cfg.Endpoint, token, declaredForExporter(cfg, logger), nil)
			sink := otlp.NewSink(exporter, logger, store, cfg.Mode)
			ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
			defer cancel()

			// Watch tier file for changes and update declared attrs dynamically
			go watchTierFile(ctx, cfg.Declared.Tier, exporter, logger)

			// Start auto-updater in background if this is a managed install.
			// Only primary instance owns auto-update; secondaries rely on
			// PartOf=telemetron.service for cascade restart.
			updateCh := make(chan int, 1)
			if updater.IsManagedInstall() && !isSecondaryInstance() {
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
	// Detect pack version based on mode
	packVersion := cfg.Declared.PackVersion
	if packVersion == "" {
		switch cfg.Mode {
		case "openclaw":
			packVersion = openclaw.DetectVersion()
		case "roundhouse":
			packVersion = roundhouse.DetectVersion()
		}
	}
	declared := map[string]string{
		"deployment_id":      cfg.Declared.DeploymentID,
		"tier":               cfg.Declared.Tier,
		"environment":        cfg.Declared.Environment,
		"pack_version":       packVersion,
		"telemetron_version": version,
	}
	installID, err := readInstallID(cfg.Paths.InstallIDFile)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			logger.Warn("install_id unavailable; continuing without resource attribute", "path", cfg.Paths.InstallIDFile, "error", err)
		}
		return declared
	}
	declared["install_id"] = installID
	return declared
}

// watchTierFile polls the tier file every 30s and updates the exporter's
// declared tier attribute if it changes. This allows operators to change
// the tier by editing ~/.loki/tier without restarting telemetron.
func watchTierFile(ctx context.Context, initialTier string, exporter *otlp.Exporter, logger *slog.Logger) {
	currentTier := initialTier
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			info := readInstallerInfo()
			if info.Tier != "" && info.Tier != currentTier {
				logger.Info("tier file changed", "old", currentTier, "new", info.Tier)
				exporter.UpdateDeclared("tier", info.Tier)
				currentTier = info.Tier
			}
		}
	}
}

// isSecondaryInstance returns true if the current process is running as a
// secondary (instanced) telemetron. Determined by the --config path: if it
// lives in the expected config directory AND matches the instance naming
// pattern (config-<name>.yaml), it's secondary.
func isSecondaryInstance() bool {
	if configPath == "" {
		return false
	}
	// Only consider files in the expected telemetron config directories
	dir := filepath.Dir(configPath)
	knownDirs := []string{"/etc/telemetron"}
	if home, err := os.UserHomeDir(); err == nil {
		knownDirs = append(knownDirs, filepath.Join(home, ".config", "telemetron"))
	}
	var inKnownDir bool
	for _, d := range knownDirs {
		if dir == d {
			inKnownDir = true
			break
		}
	}
	if !inKnownDir {
		return false
	}
	base := filepath.Base(configPath)
	return strings.HasPrefix(base, "config-") && strings.HasSuffix(base, ".yaml")
}
