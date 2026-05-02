package main

import (
	"fmt"
	"os"

	"github.com/inceptionstack/loki-otl/internal/config"
	"github.com/inceptionstack/loki-otl/internal/systemd"
	"github.com/spf13/cobra"
)

func newInstallCmd() *cobra.Command {
	var endpoint string
	var token string
	var mode string
	var deploymentID string
	var tier string

	cmd := &cobra.Command{
		Use:   "install",
		Short: "Install the systemd unit and start it",
		RunE: func(cmd *cobra.Command, args []string) error {
			overrides := map[string]string{
				"endpoint":               firstNonEmpty(endpoint, os.Getenv("LOKIOTEL_ENDPOINT")),
				"mode":                   firstNonEmpty(mode, os.Getenv("LOKIOTEL_MODE")),
				"log_level":              firstNonEmpty(logLevel, os.Getenv("LOKIOTEL_LOG_LEVEL")),
				"declared.deployment_id": deploymentID,
				"declared.tier":          tier,
			}
			cfg, err := config.Load(config.LoadOptions{ConfigPath: configPath, Overrides: overrides})
			if err != nil {
				return err
			}
			if token == "" {
				token = os.Getenv("LOKIOTEL_TOKEN")
			}
			if token == "" {
				if existing, err := cfg.Token(); err == nil {
					token = existing
				}
			}
			if token == "" {
				return fmt.Errorf("token is required via --token, LOKIOTEL_TOKEN, or existing token file")
			}
			if err := systemd.Install(cfg, token); err != nil {
				return err
			}
			if err := systemd.EnableAndStart(); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "installed endpoint=%s mode=%s deployment_id=%s unit=lokiotel.service\n", cfg.Endpoint, cfg.Mode, cfg.Declared.DeploymentID)
			return nil
		},
	}

	cmd.Flags().StringVar(&endpoint, "endpoint", "", "OTLP endpoint")
	cmd.Flags().StringVar(&token, "token", "", "bearer token")
	cmd.Flags().StringVar(&mode, "mode", "", "collection mode")
	cmd.Flags().StringVar(&deploymentID, "deployment-id", "", "deployment id")
	cmd.Flags().StringVar(&tier, "tier", "", "internal|production|development|staging|unknown")
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
