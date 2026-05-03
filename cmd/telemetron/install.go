// SPDX-License-Identifier: Apache-2.0

package main

import (
	"errors"
	"fmt"
	"os"
	"runtime"
	"strings"

	"github.com/inceptionstack/telemetron/internal/config"
	"github.com/inceptionstack/telemetron/internal/service"
	"github.com/spf13/cobra"
)

func newInstallCmd() *cobra.Command {
	var endpoint string
	var token string
	var tokenFile string
	var mode string
	var deploymentID string
	var tier string
	var sessionDir string
	var insecureEndpoint bool
	var runAs string

	cmd := &cobra.Command{
		Use:   "install",
		Short: "Install the service and start it (low-level; prefer `telemetron setup`)",
		Long: `Install the service and start it.

This is the low-level primitive. For most users (and all bundled
installers), prefer 'telemetron setup' — it auto-detects the agent,
resolves inputs from flags/env, and verifies a first flush before
reporting success.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if token != "" {
				fmt.Fprintln(cmd.ErrOrStderr(),
					"DEPRECATED: --token leaks via shell history and /proc/<pid>/cmdline. "+
						"Use --token-file, TELEMETRON_TOKEN, or `telemetron setup` (interactive prompt).")
			}
			if runtime.GOOS == "darwin" {
				fmt.Fprintln(cmd.ErrOrStderr(), "telemetron install is not supported on macOS.")
				fmt.Fprintln(cmd.ErrOrStderr(), "You can still run `telemetron start --config /path/to/config.yaml` directly or under launchd. See docs/macos.md.")
				return fmt.Errorf("install unsupported on macOS")
			}

			overrides := map[string]any{
				"endpoint":               firstNonEmpty(endpoint, os.Getenv("TELEMETRON_ENDPOINT")),
				"mode":                   firstNonEmpty(mode, os.Getenv("TELEMETRON_MODE")),
				"log_level":              firstNonEmpty(logLevel, os.Getenv("TELEMETRON_LOG_LEVEL")),
				"insecure_endpoint":      insecureEndpoint,
				"declared.deployment_id": deploymentID,
				"declared.tier":          tier,
			}
			cfg, err := config.Load(config.LoadOptions{
				ConfigPath:    configPath,
				Overrides:     overrides,
				BootstrapOnly: true,
			})
			if err != nil {
				return err
			}

			if sessionDir != "" {
				cfg.Collectors[cfg.Mode] = map[string]any{"session_dir": sessionDir}
			}
			resolvedRaw, err := config.ResolveCollectorRaw(cfg.Mode, cfg.Paths, cfg.Collectors[cfg.Mode])
			if err != nil {
				return err
			}
			cfg.Collectors[cfg.Mode] = resolvedRaw

			if token == "" && tokenFile != "" {
				data, err := os.ReadFile(tokenFile)
				if err != nil {
					return fmt.Errorf("read --token-file %s: %w", tokenFile, err)
				}
				token = strings.TrimSpace(string(data))
			}
			if token == "" {
				token = os.Getenv("TELEMETRON_TOKEN")
			}
			if token == "" && os.Getenv("TELEMETRON_TOKEN_FILE") != "" {
				data, err := os.ReadFile(os.Getenv("TELEMETRON_TOKEN_FILE"))
				if err != nil {
					return fmt.Errorf("read TELEMETRON_TOKEN_FILE: %w", err)
				}
				token = strings.TrimSpace(string(data))
			}
			if token == "" {
				if existing, err := cfg.Token(); err == nil {
					token = existing
				}
			}
			if token == "" {
				return fmt.Errorf("token is required via --token-file, TELEMETRON_TOKEN, TELEMETRON_TOKEN_FILE, or existing token file")
			}

			svc := service.New()
			if err := svc.InstallAs(cfg, token, runAs); err != nil {
				if errors.Is(err, service.ErrUnsupported) {
					return err
				}
				return err
			}
			if err := svc.EnableAndStart(); err != nil {
				return err
			}
			unitStatus, err := svc.ProbeStatus()
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "installed endpoint=%s mode=%s deployment_id=%s unit=%s\n", cfg.Endpoint, cfg.Mode, cfg.Declared.DeploymentID, unitStatus.Detail)
			return nil
		},
	}

	cmd.Flags().StringVar(&endpoint, "endpoint", "", "OTLP endpoint (env: TELEMETRON_ENDPOINT, config: endpoint)")
	cmd.Flags().StringVar(&token, "token", "", "DEPRECATED: bearer token (leaks via ps/shell history); use --token-file instead")
	cmd.Flags().StringVar(&tokenFile, "token-file", "", "path to a file containing the bearer token (env: TELEMETRON_TOKEN_FILE)")
	cmd.Flags().StringVar(&mode, "mode", "", "collection mode (env: TELEMETRON_MODE, config: mode)")
	cmd.Flags().StringVar(&deploymentID, "deployment-id", "", "deployment id (config: declared.deployment_id)")
	cmd.Flags().StringVar(&tier, "tier", "", "internal|production|development|staging|unknown (config: declared.tier)")
	cmd.Flags().StringVar(&sessionDir, "session-dir", "", "session directory (config: <mode>.session_dir)")
	cmd.Flags().BoolVar(&insecureEndpoint, "insecure-endpoint", false, "allow http:// endpoints for testing only (config: insecure_endpoint)")
	cmd.Flags().StringVar(&runAs, "run-as", "", "OS user the systemd unit runs as (default: $SUDO_USER when invoked via sudo, else the system 'telemetron' user)")
	return cmd
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
