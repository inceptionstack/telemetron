// SPDX-License-Identifier: Apache-2.0

package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/inceptionstack/telemetron/internal/agentdetect"
	"github.com/spf13/cobra"
)

type detectFlags struct {
	endpoint       string
	enrollEndpoint string
	mode           string
	home           string
	dryRun         bool
	force          bool
}

func newDetectCmd() *cobra.Command {
	var f detectFlags
	cmd := &cobra.Command{
		Use:   "detect",
		Short: "Auto-detect packs and configure telemetron for each",
		Long: `Scan the host for known agent session directories and configure
telemetron for each detected pack. This is the recommended entry point
for all installers.

For each detected pack that isn't already configured:
  1. Writes config file (primary or instance-named)
  2. Enrolls with the server (gets token)
  3. Installs systemd unit (primary or instance-named)
  4. Starts the service

Re-running is idempotent: already-configured packs are skipped unless
--force is passed.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDetect(cmd, &f)
		},
		SilenceUsage: true,
	}

	cmd.Flags().StringVar(&f.endpoint, "endpoint", "", "Metrics endpoint (env: TELEMETRON_ENDPOINT)")
	cmd.Flags().StringVar(&f.enrollEndpoint, "enroll-endpoint", "", "Enrollment endpoint (env: TELEMETRON_ENROLL_ENDPOINT)")
	cmd.Flags().StringVar(&f.mode, "mode", "", "Only detect/configure this specific mode")
	cmd.Flags().StringVar(&f.home, "home", "", "Override home directory for detection (testing)")
	cmd.Flags().BoolVar(&f.dryRun, "dry-run", false, "Show what would be detected without making changes")
	cmd.Flags().BoolVar(&f.force, "force", false, "Re-configure even if already set up")
	return cmd
}

func runDetect(cmd *cobra.Command, f *detectFlags) error {
	endpoint := firstNonEmpty(f.endpoint, os.Getenv("TELEMETRON_ENDPOINT"))
	if endpoint == "" {
		return fmt.Errorf("endpoint is required: pass --endpoint or set TELEMETRON_ENDPOINT")
	}

	detections, detectErrs := detectPacks(f)

	// Warn about detection errors (non-fatal)
	for _, err := range detectErrs {
		fmt.Fprintf(cmd.ErrOrStderr(), "warning: detection error: %v\n", err)
	}

	// Filter by mode if specified
	if f.mode != "" {
		var filtered []agentdetect.Detection
		for _, d := range detections {
			if d.Mode == f.mode {
				filtered = append(filtered, d)
			}
		}
		detections = filtered
	}

	if len(detections) == 0 {
		return fmt.Errorf("no packs detected on this host")
	}

	// Filter out ambiguous detections (Mode="") and warn
	var resolved []agentdetect.Detection
	for _, d := range detections {
		if d.Mode == "" && len(d.Ambiguous) > 0 {
			fmt.Fprintf(cmd.ErrOrStderr(), "warning: multiple openclaw agents found; pass --mode or use `telemetron setup --session-dir` to disambiguate\n")
			continue
		}
		if d.Mode != "" {
			resolved = append(resolved, d)
		}
	}
	detections = resolved

	if len(detections) == 0 {
		return fmt.Errorf("no packs detected on this host (openclaw detection was ambiguous; pass --session-dir to setup)")
	}

	// Print detection summary
	fmt.Fprintf(cmd.OutOrStdout(), "Detected %d pack(s):\n", len(detections))
	for _, d := range detections {
		fmt.Fprintf(cmd.OutOrStdout(), "  %s  %-12s %s (user: %s)\n", checkMark, d.Mode, d.SessionDir, d.RunAsUser)
	}

	if f.dryRun {
		fmt.Fprintln(cmd.OutOrStdout(), "\ndry-run: no changes made")
		return nil
	}

	// For each detection, delegate to setup.
	// openclaw is always the primary instance (uses unsuffixed config paths).
	// All other modes are instanced by their mode name.
	enrollEndpoint := firstNonEmpty(f.enrollEndpoint, os.Getenv("TELEMETRON_ENROLL_ENDPOINT"))
	var configured int
	for i, d := range detections {
		fmt.Fprintf(cmd.OutOrStdout(), "\n[%d/%d] Setting up %s...\n", i+1, len(detections), d.Mode)

		instance := instanceForModeInContext(d.Mode, len(detections))

		// Phase 2: instance-aware setup (--instance flag on setup command).
		// For now, only the primary instance (openclaw) can be fully configured.
		if instance != "" {
			fmt.Fprintf(cmd.OutOrStdout(), "  %s instance %q requires --instance support (Phase 2); skipping\n", arrow, instance)
			continue
		}

		if !f.force && instanceAlreadyConfigured(instance) && instanceModeMatches(instance, d.Mode) {
			fmt.Fprintf(cmd.OutOrStdout(), "  %s already configured, skipping (use --force to reconfigure)\n", arrow)
			configured++
			continue
		}

		err := runDetectSetup(cmd, d, endpoint, enrollEndpoint)
		if err != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "  %s %s setup failed: %v\n", crossMark, d.Mode, err)
			continue
		}
		fmt.Fprintf(cmd.OutOrStdout(), "  %s %s configured\n", checkMark, d.Mode)
		configured++
	}

	if configured == 0 {
		return fmt.Errorf("all detected packs failed to configure")
	}
	return nil
}

const (
	checkMark = "\u2713"
	crossMark = "\u2717"
	arrow     = "\u2192"
)

// detectPacks resolves detections, handling the root-without-SUDO_USER case
// by scanning all home directories (same fallback as setup uses).
// Returns detections and any detector warnings.
func detectPacks(f *detectFlags) ([]agentdetect.Detection, []error) {
	if f.home != "" {
		return agentdetect.DetectAll(agentdetect.Options{HomeDirOverride: f.home})
	}

	// When running as root without SUDO_USER (cloud-init, UserData),
	// scan all plausible home dirs to find agents.
	if os.Geteuid() == 0 && os.Getenv("SUDO_USER") == "" {
		return detectFromAllHomes(), nil
	}

	return agentdetect.DetectAll(agentdetect.Options{})
}

// detectFromAllHomes scans /home/* (and /root) for agent directories.
// Passes explicit User so RunAsUser resolves to the home dir owner, not root.
func detectFromAllHomes() []agentdetect.Detection {
	var results []agentdetect.Detection
	seen := map[string]bool{}

	entries, err := os.ReadDir("/home")
	if err == nil {
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			username := e.Name()
			home := "/home/" + username
			detections, _ := agentdetect.DetectAll(agentdetect.Options{
				HomeDirOverride: home,
				User:            username,
			})
			for _, d := range detections {
				key := d.Mode + ":" + d.SessionDir
				if !seen[key] {
					seen[key] = true
					results = append(results, d)
				}
			}
		}
	}

	// Also check /root
	detections, _ := agentdetect.DetectAll(agentdetect.Options{
		HomeDirOverride: "/root",
		User:            "root",
	})
	for _, d := range detections {
		key := d.Mode + ":" + d.SessionDir
		if !seen[key] {
			seen[key] = true
			results = append(results, d)
		}
	}
	return results
}

func instanceAlreadyConfigured(instance string) bool {
	path := configPathForInstance(instance)
	_, err := os.Stat(path)
	return err == nil
}

// instanceModeMatches checks if the existing config's mode matches the detected mode.
// Prevents skipping setup when config exists but is for a different pack.
func instanceModeMatches(instance, mode string) bool {
	path := configPathForInstance(instance)
	return modeMatchesFile(path, mode)
}

// modeMatchesFile reads a config file and checks if its mode field matches.
func modeMatchesFile(path, mode string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "mode:") {
			v := strings.TrimSpace(strings.TrimPrefix(line, "mode:"))
			return v == mode
		}
	}
	return false
}

func configPathForInstance(instance string) string {
	if instance == "" {
		return "/etc/telemetron/config.yaml"
	}
	return fmt.Sprintf("/etc/telemetron/config-%s.yaml", instance)
}

// instanceForMode determines the instance name based on detection context.
// When only one pack is detected, it's always primary (empty instance).
// When multiple packs exist, openclaw gets primary and others are instanced.
func instanceForModeInContext(mode string, totalDetections int) string {
	if totalDetections == 1 {
		return "" // only pack on host = always primary
	}
	if mode == "openclaw" {
		return ""
	}
	return mode
}

func runDetectSetup(cmd *cobra.Command, d agentdetect.Detection, endpoint, enrollEndpoint string) error {
	args := []string{
		"--endpoint", endpoint,
		"--mode", d.Mode,
		"--session-dir", d.SessionDir,
		"--yes",
		"--non-interactive",
	}
	if d.RunAsUser != "" {
		args = append(args, "--run-as", d.RunAsUser)
	}

	// Propagate enroll endpoint: set env before sub-command so setup reads it.
	// Restore after to avoid leaking state. Safe because the loop is sequential;
	// if parallelized in the future, pass via flag instead.
	if enrollEndpoint != "" {
		prev := os.Getenv("TELEMETRON_ENROLL_ENDPOINT")
		_ = os.Setenv("TELEMETRON_ENROLL_ENDPOINT", enrollEndpoint)
		defer func() { _ = os.Setenv("TELEMETRON_ENROLL_ENDPOINT", prev) }()
	}

	setupCmd := newSetupCmd()
	setupCmd.SetOut(cmd.OutOrStdout())
	setupCmd.SetErr(cmd.ErrOrStderr())
	setupCmd.SetArgs(args)
	setupCmd.SilenceUsage = true
	setupCmd.SilenceErrors = true

	return setupCmd.Execute()
}
