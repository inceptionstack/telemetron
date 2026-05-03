// SPDX-License-Identifier: Apache-2.0

package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/inceptionstack/telemetron/internal/agentdetect"
	"github.com/inceptionstack/telemetron/internal/config"
	"github.com/inceptionstack/telemetron/internal/service"
	"github.com/inceptionstack/telemetron/internal/setupevents"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

var (
	setupPlatform              = runtime.GOOS
	setupGeteuid               = os.Geteuid
	findOpenClawMainCandidates = agentdetect.FindOpenClawMainCandidates
)

const unresolvedRootSessionHint = "cannot resolve session-dir under UID 0 with no $SUDO_USER set.\nPass --run-as <user> --session-dir <path>, or set TELEMETRON_RUN_AS /\nTELEMETRON_SESSION_DIR."

// setupFlags collects everything the setup command accepts. The same
// struct is used for both non-interactive and interactive paths; prompts
// only fire to fill unresolved required fields when a TTY is available
// and --non-interactive was not passed.
type setupFlags struct {
	endpoint         string
	tokenFile        string
	mode             string
	sessionDir       string
	runAs            string
	deploymentID     string
	tier             string
	insecureEndpoint bool

	yes            bool
	nonInteractive bool
	jsonOutput     bool
	dryRun         bool
}

func newSetupCmd() *cobra.Command {
	var f setupFlags
	cmd := &cobra.Command{
		Use:   "setup",
		Short: "One-shot reconciler: detect, configure, install, start, verify",
		Long: `Resolve inputs from flags, environment, auto-detection, and existing
state, then install or update the telemetron systemd service and verify
a first flush.

setup is non-interactive-first. Prompts only fire to fill unresolved
required inputs (endpoint, token) when stdin is a TTY and
--non-interactive was not passed.

setup is safe to rerun. Re-running with the same resolved state is a
no-op success. Re-running with changed endpoint or token updates the
on-disk state and restarts the service as needed.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSetup(cmd, &f)
		},
	}

	cmd.Flags().StringVar(&f.endpoint, "endpoint", "", "OTLP/HTTP endpoint (env: TELEMETRON_ENDPOINT)")
	cmd.Flags().StringVar(&f.tokenFile, "token-file", "", "path to a file containing the bearer token (env: TELEMETRON_TOKEN_FILE)")
	cmd.Flags().StringVar(&f.mode, "mode", "", "collection mode; default: auto-detected (env: TELEMETRON_MODE)")
	cmd.Flags().StringVar(&f.sessionDir, "session-dir", "", "session directory; default: auto-detected (env: TELEMETRON_SESSION_DIR)")
	cmd.Flags().StringVar(&f.runAs, "run-as", "", "OS user the service runs as; default: $SUDO_USER (env: TELEMETRON_RUN_AS)")
	cmd.Flags().StringVar(&f.deploymentID, "deployment-id", "", "deployment id; default: loki@<hostname> (env: TELEMETRON_DEPLOYMENT_ID)")
	cmd.Flags().StringVar(&f.tier, "tier", "", "deployment tier (env: TELEMETRON_TIER)")
	cmd.Flags().BoolVar(&f.insecureEndpoint, "insecure-endpoint", false, "allow http:// endpoints (testing only)")
	cmd.Flags().BoolVar(&f.yes, "yes", false, "skip the confirmation prompt; the summary is still printed")
	cmd.Flags().BoolVar(&f.nonInteractive, "non-interactive", false, "never prompt; fail fast on missing required input")
	cmd.Flags().BoolVar(&f.jsonOutput, "json", false, "emit line-delimited JSON events (contract: telemetron.setup.v1)")
	cmd.Flags().BoolVar(&f.dryRun, "dry-run", false, "print what would happen, touch nothing")
	return cmd
}

type setupEmitter struct {
	json bool
	out  io.Writer
	err  io.Writer
}

func (e *setupEmitter) emit(event string, fields map[string]any) {
	if !e.json {
		return
	}
	payload := map[string]any{"schema": setupevents.SchemaVersion, "event": event}
	for k, v := range fields {
		payload[k] = v
	}
	data, _ := json.Marshal(payload)
	fmt.Fprintln(e.out, string(data))
}

func (e *setupEmitter) info(msg string) {
	if e.json {
		return
	}
	fmt.Fprintln(e.out, msg)
}

func (e *setupEmitter) errorEnvelope(code string, missing []string, hint string, err error) error {
	fields := map[string]any{"error_code": code}
	if len(missing) > 0 {
		fields["missing_fields"] = missing
	}
	if hint != "" {
		fields["hint"] = hint
	}
	if err != nil {
		fields["error"] = err.Error()
	}
	e.emit(setupevents.EventSetupFailed, fields)
	if e.json {
		return fmt.Errorf("setup failed: %s", code)
	}
	if err != nil {
		return fmt.Errorf("%s: %w", code, err)
	}
	return errors.New(code)
}

func runSetup(cmd *cobra.Command, f *setupFlags) error {
	emitter := &setupEmitter{json: f.jsonOutput, out: cmd.OutOrStdout(), err: cmd.ErrOrStderr()}

	if setupPlatform == "darwin" {
		return emitter.errorEnvelope(setupevents.ErrPreconditionFailed, nil,
			"telemetron setup is not supported on macOS; see docs/macos.md", nil)
	}

	// --- 1. Resolve agent detection ---------------------------------------
	detection, ambiguousCandidates, err := resolveDetection(f)
	if err != nil {
		return emitter.errorEnvelope(setupevents.ErrDetectionFailed, nil, "", err)
	}
	if len(ambiguousCandidates) > 0 {
		agents := make([]string, 0, len(ambiguousCandidates))
		for _, c := range ambiguousCandidates {
			agents = append(agents, c.AgentName)
		}
		return emitter.errorEnvelope(setupevents.ErrAmbiguousAgent, nil,
			fmt.Sprintf("multiple agent slots matched (%s); pass --session-dir to disambiguate",
				strings.Join(agents, ", ")),
			nil)
	}
	if detection.Mode != "" {
		emitter.emit(setupevents.EventAgentDetected, map[string]any{
			"mode":        detection.Mode,
			"agent_name":  detection.AgentName,
			"session_dir": detection.SessionDir,
			"run_as":      detection.RunAsUser,
		})
	}

	// --- 2. Resolve required inputs ---------------------------------------
	resolved, missing, err := resolveInputs(f, detection)
	if err != nil {
		return emitter.errorEnvelope(setupevents.ErrInvalidConfig, nil, "", err)
	}

	// If fields are missing, try interactive fallback when allowed.
	if len(missing) > 0 {
		if f.nonInteractive || !term.IsTerminal(int(os.Stdin.Fd())) {
			hint := hintForMissing(missing)
			return emitter.errorEnvelope(setupevents.ErrMissingRequiredInput, missing, hint, nil)
		}
		if err := promptMissing(cmd, &resolved, missing); err != nil {
			return emitter.errorEnvelope(setupevents.ErrTokenReadFailed, nil, "", err)
		}
	}

	emitter.emit(setupevents.EventConfigResolved, map[string]any{
		"endpoint":      resolved.endpoint,
		"mode":          resolved.mode,
		"session_dir":   resolved.sessionDir,
		"deployment_id": resolved.deploymentID,
		"tier":          resolved.tier,
		"run_as":        resolved.runAs,
	})

	// --- 3. Print summary / confirm ---------------------------------------
	summary := renderSummary(resolved)
	emitter.info(summary)
	if !f.yes && !f.nonInteractive && term.IsTerminal(int(os.Stdin.Fd())) && !f.jsonOutput {
		ok, err := promptYesNo(cmd, "Proceed?", true)
		if err != nil {
			return emitter.errorEnvelope(setupevents.ErrPreconditionFailed, nil, "", err)
		}
		if !ok {
			emitter.info("Aborted.")
			return nil
		}
	}

	if f.dryRun {
		emitter.emit(setupevents.EventSetupCompleted, map[string]any{
			"endpoint":      resolved.endpoint,
			"mode":          resolved.mode,
			"session_dir":   resolved.sessionDir,
			"deployment_id": resolved.deploymentID,
			"tier":          resolved.tier,
			"run_as":        resolved.runAs,
			"action_taken":  "dry_run",
			"health":        "skipped",
		})
		emitter.info("dry-run: would install telemetron with the above config; no changes made")
		return nil
	}

	// --- 4. Materialise config + install -----------------------------------
	token, tokenSource, err := loadToken(resolved)
	if err != nil {
		return emitter.errorEnvelope(setupevents.ErrTokenReadFailed, nil,
			"check --token-file / TELEMETRON_TOKEN_FILE / TELEMETRON_TOKEN", err)
	}
	emitter.emit(setupevents.EventTokenLoaded, map[string]any{"source": tokenSource})

	cfg, err := buildConfig(resolved)
	if err != nil {
		return emitter.errorEnvelope(setupevents.ErrInvalidConfig, nil, "", err)
	}

	svc := service.New()
	action := setupevents.ActionInstalled
	if unitExists() {
		action = setupevents.ActionUpdated
	}
	if err := svc.InstallAs(cfg, token, resolved.runAs); err != nil {
		return emitter.errorEnvelope(setupevents.ErrSystemdInstallFailed, nil, "", err)
	}
	emitter.emit(setupevents.EventTokenWritten, map[string]any{"path": cfg.TokenFile})
	emitter.emit(setupevents.EventServiceInstalled, map[string]any{
		"unit_path":   "/etc/systemd/system/telemetron.service",
		"config_path": cfg.FilePath,
	})

	if err := svc.EnableAndStart(); err != nil {
		return emitter.errorEnvelope(setupevents.ErrServiceStartFailed, nil, "", err)
	}
	emitter.emit(setupevents.EventServiceStarted, nil)

	// --- 5. Health verification --------------------------------------------
	if err := verifyFirstFlush(emitter); err != nil {
		return emitter.errorEnvelope(setupevents.ErrHealthCheckFailed, nil,
			"service started but first flush did not land within the timeout", err)
	}
	emitter.emit(setupevents.EventHealthcheckPassed, nil)

	emitter.emit(setupevents.EventSetupCompleted, map[string]any{
		"endpoint":      resolved.endpoint,
		"mode":          resolved.mode,
		"session_dir":   resolved.sessionDir,
		"deployment_id": resolved.deploymentID,
		"tier":          resolved.tier,
		"run_as":        resolved.runAs,
		"token_path":    cfg.TokenFile,
		"action_taken":  action,
		"health":        "passed",
	})
	emitter.info(fmt.Sprintf("telemetron %s — first flush ok", action))
	return nil
}

// --- resolved state -----------------------------------------------------

type resolvedSetup struct {
	endpoint         string
	tokenFile        string
	tokenFromEnv     string
	mode             string
	sessionDir       string
	runAs            string
	deploymentID     string
	tier             string
	insecureEndpoint bool
}

func resolveDetection(f *setupFlags) (agentdetect.Detection, []agentdetect.Candidate, error) {
	// Skip detection entirely if user specified both mode and session dir.
	mode := firstNonEmpty(f.mode, os.Getenv("TELEMETRON_MODE"))
	sessionDir := firstNonEmpty(f.sessionDir, os.Getenv("TELEMETRON_SESSION_DIR"))
	runAs := firstNonEmpty(f.runAs, os.Getenv("TELEMETRON_RUN_AS"))
	if mode != "" && sessionDir != "" {
		return agentdetect.Detection{
			Mode:       mode,
			SessionDir: sessionDir,
			RunAsUser:  runAs,
		}, nil, nil
	}

	if shouldUseRootHomeScan(runAs, sessionDir) {
		candidates, err := findOpenClawMainCandidates(setupPlatform, "")
		if err != nil {
			return agentdetect.Detection{}, nil, err
		}
		if len(candidates) != 1 {
			return agentdetect.Detection{}, nil, errors.New(unresolvedRootSessionHint)
		}
		return agentdetect.Detection{
			Mode:       "openclaw",
			SessionDir: candidates[0].SessionDir,
			RunAsUser:  candidates[0].RunAsUser,
			AgentName:  candidates[0].AgentName,
		}, nil, nil
	}

	d, err := agentdetect.DetectOpenClaw(agentdetect.Options{User: runAs})
	if err != nil {
		return agentdetect.Detection{}, nil, err
	}
	if len(d.Ambiguous) > 0 && sessionDir == "" {
		return agentdetect.Detection{}, d.Ambiguous, nil
	}
	// Explicit overrides win even over detection.
	if mode != "" {
		d.Mode = mode
	}
	if sessionDir != "" {
		d.SessionDir = sessionDir
	}
	if runAs != "" {
		d.RunAsUser = runAs
	}
	return d, nil, nil
}

func shouldUseRootHomeScan(runAs, sessionDir string) bool {
	if runAs != "" || sessionDir != "" {
		return false
	}
	if setupGeteuid() != 0 {
		return false
	}
	sudoUser := strings.TrimSpace(os.Getenv("SUDO_USER"))
	return sudoUser == ""
}

func resolveInputs(f *setupFlags, d agentdetect.Detection) (resolvedSetup, []string, error) {
	r := resolvedSetup{
		endpoint:         firstNonEmpty(f.endpoint, os.Getenv("TELEMETRON_ENDPOINT")),
		tokenFile:        firstNonEmpty(f.tokenFile, os.Getenv("TELEMETRON_TOKEN_FILE")),
		tokenFromEnv:     os.Getenv("TELEMETRON_TOKEN"),
		mode:             firstNonEmpty(f.mode, os.Getenv("TELEMETRON_MODE"), d.Mode),
		sessionDir:       firstNonEmpty(f.sessionDir, os.Getenv("TELEMETRON_SESSION_DIR"), d.SessionDir),
		runAs:            firstNonEmpty(f.runAs, os.Getenv("TELEMETRON_RUN_AS"), d.RunAsUser),
		deploymentID:     firstNonEmpty(f.deploymentID, os.Getenv("TELEMETRON_DEPLOYMENT_ID")),
		tier:             firstNonEmpty(f.tier, os.Getenv("TELEMETRON_TIER")),
		insecureEndpoint: f.insecureEndpoint,
	}

	// Reconcile against an existing install when fields are unset.
	if existing := loadExistingConfig(); existing != nil {
		if r.endpoint == "" {
			r.endpoint = existing.Endpoint
		}
		if r.mode == "" {
			r.mode = existing.Mode
		}
		if r.sessionDir == "" {
			r.sessionDir = existingSessionDir(*existing)
		}
		if r.runAs == "" {
			r.runAs = existing.RunAs
		}
		if r.deploymentID == "" {
			r.deploymentID = existing.Declared.DeploymentID
		}
		if r.tier == "" {
			r.tier = existing.Declared.Tier
		}
	}

	if r.mode == "" {
		r.mode = "openclaw"
	}
	if r.deploymentID == "" {
		r.deploymentID = defaultDeploymentID(d.AgentName)
	}
	if r.tier == "" {
		r.tier = inferTier()
	}

	var missing []string
	if r.endpoint == "" {
		missing = append(missing, "endpoint")
	}
	if r.tokenFile == "" && r.tokenFromEnv == "" && !existingTokenFilePresent() {
		missing = append(missing, "token")
	}
	return r, missing, nil
}

func defaultDeploymentID(agentName string) string {
	host, err := os.Hostname()
	if err != nil || host == "" {
		host = "unknown"
	}
	if agentName != "" && agentName != "main" {
		return fmt.Sprintf("loki-%s@%s", agentName, host)
	}
	return fmt.Sprintf("loki@%s", host)
}

func inferTier() string {
	if sudo := strings.TrimSpace(os.Getenv("SUDO_USER")); sudo != "" && sudo != "root" {
		if term.IsTerminal(int(os.Stdin.Fd())) {
			return "development"
		}
	}
	return "production"
}

func hintForMissing(missing []string) string {
	var hints []string
	for _, m := range missing {
		switch m {
		case "endpoint":
			hints = append(hints, "set --endpoint or TELEMETRON_ENDPOINT")
		case "token":
			hints = append(hints, "set --token-file, TELEMETRON_TOKEN_FILE, or TELEMETRON_TOKEN")
		}
	}
	return strings.Join(hints, "; ")
}

func renderSummary(r resolvedSetup) string {
	var b strings.Builder
	b.WriteString("About to install telemetron:\n")
	fmt.Fprintf(&b, "  endpoint:      %s\n", r.endpoint)
	fmt.Fprintf(&b, "  mode:          %s\n", r.mode)
	fmt.Fprintf(&b, "  session dir:   %s\n", r.sessionDir)
	fmt.Fprintf(&b, "  run-as:        %s\n", r.runAs)
	fmt.Fprintf(&b, "  deployment_id: %s\n", r.deploymentID)
	fmt.Fprintf(&b, "  tier:          %s\n", r.tier)
	if r.tokenFile != "" {
		fmt.Fprintf(&b, "  token file:    %s (copied to /etc/telemetron/token, mode 0400)\n", r.tokenFile)
	} else if r.tokenFromEnv != "" {
		b.WriteString("  token source:  TELEMETRON_TOKEN (env)\n")
	} else {
		b.WriteString("  token source:  existing /etc/telemetron/token\n")
	}
	return b.String()
}

func loadToken(r resolvedSetup) (string, string, error) {
	if r.tokenFile != "" {
		data, err := os.ReadFile(r.tokenFile)
		if err != nil {
			return "", "", err
		}
		return strings.TrimSpace(string(data)), "token-file", nil
	}
	if r.tokenFromEnv != "" {
		return strings.TrimSpace(r.tokenFromEnv), "env", nil
	}
	// Fall back to any existing token file on disk.
	existingTokenPath := "/etc/telemetron/token"
	data, err := os.ReadFile(existingTokenPath)
	if err != nil {
		return "", "", err
	}
	return strings.TrimSpace(string(data)), "existing", nil
}

func existingTokenFilePresent() bool {
	_, err := os.Stat("/etc/telemetron/token")
	return err == nil
}

func unitExists() bool {
	_, err := os.Stat("/etc/systemd/system/telemetron.service")
	return err == nil
}

func loadExistingConfig() *config.Config {
	cfg, err := config.Load(config.LoadOptions{BootstrapOnly: true})
	if err != nil {
		return nil
	}
	return &cfg
}

func buildConfig(r resolvedSetup) (config.Config, error) {
	overrides := map[string]any{
		"endpoint":               r.endpoint,
		"mode":                   r.mode,
		"insecure_endpoint":      r.insecureEndpoint,
		"run_as":                 r.runAs,
		"declared.deployment_id": r.deploymentID,
		"declared.tier":          r.tier,
	}
	cfg, err := config.Load(config.LoadOptions{Overrides: overrides, BootstrapOnly: true})
	if err != nil {
		return config.Config{}, err
	}
	if r.sessionDir != "" {
		cfg.Collectors[cfg.Mode] = map[string]any{"session_dir": r.sessionDir}
	}
	resolvedRaw, err := config.ResolveCollectorRaw(cfg.Mode, cfg.Paths, cfg.Collectors[cfg.Mode])
	if err != nil {
		return config.Config{}, err
	}
	cfg.Collectors[cfg.Mode] = resolvedRaw
	return cfg, nil
}

func existingSessionDir(cfg config.Config) string {
	raw := cfg.Collectors[cfg.Mode]
	m, ok := raw.(map[string]any)
	if !ok {
		return ""
	}
	sessionDir, _ := m["session_dir"].(string)
	return sessionDir
}

// --- verifyFirstFlush is intentionally simple. It waits for the status
// file (written by the background flusher) to show a first success. The
// status store is already the source of truth for `telemetron status`.
func verifyFirstFlush(e *setupEmitter) error {
	// 30s is well over the 15s default flush interval; users can Ctrl+C.
	const timeoutSeconds = 30
	for i := 0; i < timeoutSeconds; i++ {
		// Existing status.json check. We do not hard-require it because
		// tests may mock the service; absence after 30s is a warning, not
		// a hard failure, but we still surface it as health_check_failed.
		if statusHealthy() {
			return nil
		}
		time.Sleep(time.Second)
	}
	return fmt.Errorf("no successful flush observed within %ds", timeoutSeconds)
}

func statusHealthy() bool {
	data, err := os.ReadFile("/var/lib/telemetron/status.json")
	if err != nil {
		return false
	}
	// Very lax: any JSON object with a "last_flush_ok" flag true passes.
	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		return false
	}
	if ok, _ := payload["last_flush_ok"].(bool); ok {
		return true
	}
	return false
}

// --- prompt helpers ------------------------------------------------------

func promptMissing(cmd *cobra.Command, r *resolvedSetup, missing []string) error {
	for _, field := range missing {
		switch field {
		case "endpoint":
			v, err := promptLine(cmd, "OTLP/HTTP endpoint: ")
			if err != nil {
				return err
			}
			r.endpoint = strings.TrimSpace(v)
		case "token":
			v, err := promptSecret(cmd, "Bearer token (hidden): ")
			if err != nil {
				return err
			}
			r.tokenFromEnv = strings.TrimSpace(v)
		}
	}
	return nil
}

func promptLine(cmd *cobra.Command, prompt string) (string, error) {
	fmt.Fprint(cmd.OutOrStdout(), prompt)
	var s string
	_, err := fmt.Fscanln(cmd.InOrStdin(), &s)
	if err != nil && err != io.EOF {
		return "", err
	}
	return s, nil
}

func promptSecret(cmd *cobra.Command, prompt string) (string, error) {
	fmt.Fprint(cmd.OutOrStdout(), prompt)
	b, err := term.ReadPassword(int(os.Stdin.Fd()))
	if err != nil {
		return "", err
	}
	fmt.Fprintln(cmd.OutOrStdout())
	return string(b), nil
}

func promptYesNo(cmd *cobra.Command, prompt string, dflt bool) (bool, error) {
	suffix := "[Y/n]"
	if !dflt {
		suffix = "[y/N]"
	}
	fmt.Fprintf(cmd.OutOrStdout(), "%s %s: ", prompt, suffix)
	var s string
	_, err := fmt.Fscanln(cmd.InOrStdin(), &s)
	if err != nil && err != io.EOF {
		return false, err
	}
	s = strings.TrimSpace(strings.ToLower(s))
	if s == "" {
		return dflt, nil
	}
	return s == "y" || s == "yes", nil
}
