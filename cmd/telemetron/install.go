// SPDX-License-Identifier: Apache-2.0

package main

import (
	"errors"
	"fmt"
	"os"
	"runtime"

	"github.com/inceptionstack/telemetron/internal/config"
	"github.com/inceptionstack/telemetron/internal/service"
	"github.com/spf13/cobra"
)

func newInstallCmd() *cobra.Command {
	var endpoint string
	var token string
	var mode string
	var deploymentID string
	var tier string
	var sessionDir string
	var insecureEndpoint bool

	cmd := &cobra.Command{
		Use:   "install",
		Short: "Install the service and start it",
		RunE: func(cmd *cobra.Command, args []string) error {
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

			if token == "" {
				token = os.Getenv("TELEMETRON_TOKEN")
			}
			if token == "" {
				if existing, err := cfg.Token(); err == nil {
					token = existing
				}
			}
			if token == "" {
				return fmt.Errorf("token is required via --token, TELEMETRON_TOKEN, or existing token file")
			}

			svc := service.New()
			if err := svc.Install(cfg, token); err != nil {
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
	cmd.Flags().StringVar(&token, "token", "", "bearer token (env: TELEMETRON_TOKEN, written to token_file)")
	cmd.Flags().StringVar(&mode, "mode", "", "collection mode (env: TELEMETRON_MODE, config: mode)")
	cmd.Flags().StringVar(&deploymentID, "deployment-id", "", "deployment id (config: declared.deployment_id)")
	cmd.Flags().StringVar(&tier, "tier", "", "internal|production|development|staging|unknown (config: declared.tier)")
	cmd.Flags().StringVar(&sessionDir, "session-dir", "", "session directory (config: <mode>.session_dir)")
	cmd.Flags().BoolVar(&insecureEndpoint, "insecure-endpoint", false, "allow http:// endpoints for testing only (config: insecure_endpoint)")
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
