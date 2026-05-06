// SPDX-License-Identifier: Apache-2.0

package main

import (
	"fmt"
	"os"
	"runtime"

	_ "github.com/inceptionstack/telemetron/internal/openclaw"
	_ "github.com/inceptionstack/telemetron/internal/roundhouse"
	"github.com/spf13/cobra"
)

var (
	configPath string
	logLevel   string
	version    = "dev"
	commit     = "unknown"
	date       = "unknown"
)

func main() {
	root := newRootCmd()
	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func newRootCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "telemetron",
		Short: "OTLP metrics sidecar for stateful agents",
	}
	cmd.PersistentFlags().StringVar(&configPath, "config", "", "config file path (env: TELEMETRON_CONFIG, default: platform-specific)")
	cmd.PersistentFlags().StringVar(&logLevel, "log-level", "info", "trace|debug|info|warn|error (env: TELEMETRON_LOG_LEVEL, config: log_level)")
	cmd.AddCommand(newInstallCmd(), newSetupCmd(), newDetectCmd(), newUninstallCmd(), newStartCmd(), newStatusCmd(), newUpdateCmd(), newVersionCmd())
	return cmd
}

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print build version",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Fprintf(cmd.OutOrStdout(), "version=%s commit=%s date=%s %s/%s\n", version, commit, date, runtime.GOOS, runtime.GOARCH)
		},
	}
}
