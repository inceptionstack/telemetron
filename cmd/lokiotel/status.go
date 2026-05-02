package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/inceptionstack/loki-otl/internal/config"
	"github.com/inceptionstack/loki-otl/internal/openclaw"
	"github.com/inceptionstack/loki-otl/internal/status"
	"github.com/spf13/cobra"
)

func newStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show service status without hitting the network",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load(config.LoadOptions{
				ConfigPath: configPath,
				Overrides:  map[string]string{"log_level": logLevel},
			})
			if err != nil {
				return err
			}
			snap, _ := status.New(cfg.OpenClaw.StatusFile).Read()
			state, _ := openclaw.LoadState(cfg.OpenClaw.StateFile)
			unit := readUnitStatus()
			files, _ := os.ReadDir(cfg.OpenClaw.SessionDir)
			fmt.Fprintf(cmd.OutOrStdout(), "unit:\t\t%s\n", unit)
			fmt.Fprintf(cmd.OutOrStdout(), "mode:\t\t%s\n", cfg.Mode)
			fmt.Fprintf(cmd.OutOrStdout(), "endpoint:\t%s\n", cfg.Endpoint)
			fmt.Fprintf(cmd.OutOrStdout(), "deployment_id:\t%s\n", cfg.Declared.DeploymentID)
			fmt.Fprintf(cmd.OutOrStdout(), "last flush:\t%s\n", renderWhen(snap.LastFlushAt, snap.LastFlushMetric))
			fmt.Fprintf(cmd.OutOrStdout(), "last heartbeat:\t%s\n", renderWhen(snap.LastHeartbeatAt, 0))
			fmt.Fprintf(cmd.OutOrStdout(), "dropped batches:\t%d\n", snap.DroppedBatches)
			fmt.Fprintf(cmd.OutOrStdout(), "session_dir:\t%s (%d files)\n", cfg.OpenClaw.SessionDir, len(files))
			fmt.Fprintf(cmd.OutOrStdout(), "state file:\t%s (%d sessions tracked)\n", cfg.OpenClaw.StateFile, len(state.Files))
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

func readUnitStatus() string {
	cmd := exec.Command("systemctl", "show", "lokiotel.service", "--property=ActiveState,SubState,ActiveEnterTimestamp", "--value")
	out, err := cmd.Output()
	if err != nil {
		return "unknown"
	}
	parts := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(parts) < 3 {
		return strings.TrimSpace(string(out))
	}
	return fmt.Sprintf("%s (%s) since %s", parts[0], parts[1], parts[2])
}
