// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/inceptionstack/telemetron/internal/agentdetect"
	"github.com/inceptionstack/telemetron/internal/config"
	"github.com/inceptionstack/telemetron/internal/service"
	"github.com/inceptionstack/telemetron/internal/status"
)

// These tests target the pure-resolution path: flags + env + detection
// merge into resolvedSetup with a correct missing[] list. Actual install
// and health verification are covered by internal/service tests, which
// mock the filesystem and systemd.

func TestResolveInputs_DetectionFillsDefaults(t *testing.T) {
	resetEnv(t)
	t.Setenv("TELEMETRON_CONFIG", "/dev/null")
	t.Setenv("TELEMETRON_ENDPOINT", "https://otlp.example.com/v1/metrics")
	t.Setenv("TELEMETRON_TOKEN", "abc")

	det := agentdetect.Detection{
		Mode:       "openclaw",
		SessionDir: "/home/dev/.openclaw/agents/main/sessions",
		RunAsUser:  "dev",
		AgentName:  "main",
	}
	f := &setupFlags{nonInteractive: true}
	r, missing, err := resolveInputs(f, det)
	if err != nil {
		t.Fatal(err)
	}
	// A host-local /etc/telemetron/token may satisfy the token check; we
	// cannot rely on missing being empty. Endpoint must be resolved from
	// the env var.
	if contains(missing, "endpoint") {
		t.Errorf("endpoint should be resolved, got missing=%+v", missing)
	}
	if r.endpoint != "https://otlp.example.com/v1/metrics" {
		t.Errorf("endpoint mismatch: %q", r.endpoint)
	}
	if r.mode != "openclaw" {
		t.Errorf("mode: want openclaw, got %q", r.mode)
	}
	if r.sessionDir != det.SessionDir {
		t.Errorf("session_dir: want %q, got %q", det.SessionDir, r.sessionDir)
	}
	if !startsWithLokiPrefix(r.deploymentID) {
		t.Errorf("deployment id default should start with 'loki@' or 'loki-': got %q", r.deploymentID)
	}
	if r.tier == "" {
		t.Errorf("tier should be inferred, got empty")
	}
}

func TestResolveInputs_FlagOverridesWin(t *testing.T) {
	resetEnv(t)
	t.Setenv("TELEMETRON_CONFIG", "/dev/null")

	f := &setupFlags{
		endpoint:     "https://flag.example/v1/metrics",
		tokenFile:    "/tmp/nonexistent-token-for-test",
		mode:         "openclaw",
		sessionDir:   "/custom/sessions",
		deploymentID: "my-deployment",
		tier:         "production",
		runAs:        "someone",
	}
	det := agentdetect.Detection{
		Mode:       "openclaw",
		SessionDir: "/default/sessions",
		RunAsUser:  "default-user",
	}
	r, _, err := resolveInputs(f, det)
	if err != nil {
		t.Fatal(err)
	}
	if r.endpoint != f.endpoint {
		t.Errorf("endpoint: want %q, got %q", f.endpoint, r.endpoint)
	}
	if r.sessionDir != "/custom/sessions" {
		t.Errorf("session_dir override lost: %q", r.sessionDir)
	}
	if r.deploymentID != "my-deployment" {
		t.Errorf("deployment_id override lost: %q", r.deploymentID)
	}
	if r.tier != "production" {
		t.Errorf("tier override lost: %q", r.tier)
	}
}

func TestResolveInputs_ReloadsSessionDirAndRunAsFromExistingConfig(t *testing.T) {
	resetEnv(t)
	configPath := t.TempDir() + "/config.yaml"
	t.Setenv("TELEMETRON_CONFIG", configPath)
	if err := os.WriteFile(configPath, []byte(`
endpoint: https://existing.example/v1/metrics
mode: openclaw
run_as: existing-user
declared:
  deployment_id: existing-deployment
  tier: production
openclaw:
  session_dir: /existing/sessions
`), 0o644); err != nil {
		t.Fatal(err)
	}

	r, _, err := resolveInputs(&setupFlags{}, agentdetect.Detection{})
	if err != nil {
		t.Fatal(err)
	}
	if r.sessionDir != "/existing/sessions" {
		t.Fatalf("want existing session dir, got %q", r.sessionDir)
	}
	if r.runAs != "existing-user" {
		t.Fatalf("want existing run-as, got %q", r.runAs)
	}
}

func TestResolveDetection_FailsFastForRootWithoutSudoUserWhenCandidatesNotUnique(t *testing.T) {
	resetEnv(t)

	prevPlatform := setupPlatform
	prevGeteuid := setupGeteuid
	prevFind := findOpenClawMainCandidates
	t.Cleanup(func() {
		setupPlatform = prevPlatform
		setupGeteuid = prevGeteuid
		findOpenClawMainCandidates = prevFind
	})

	setupPlatform = "linux"
	setupGeteuid = func() int { return 0 }
	findOpenClawMainCandidates = func(platform, fsRoot string) ([]agentdetect.HomeCandidate, error) {
		return []agentdetect.HomeCandidate{
			{RunAsUser: "alice", AgentName: "main", SessionDir: "/home/alice/.openclaw/agents/main/sessions"},
			{RunAsUser: "bob", AgentName: "main", SessionDir: "/home/bob/.openclaw/agents/main/sessions"},
		}, nil
	}

	_, _, err := resolveDetection(&setupFlags{})
	if err == nil {
		t.Fatal("expected resolveDetection to fail")
	}
	if err.Error() != unresolvedRootSessionHint {
		t.Fatalf("unexpected error: %q", err)
	}
}

func TestResolveDetection_UsesUniqueRootHomeCandidate(t *testing.T) {
	resetEnv(t)

	prevPlatform := setupPlatform
	prevGeteuid := setupGeteuid
	prevFind := findOpenClawMainCandidates
	t.Cleanup(func() {
		setupPlatform = prevPlatform
		setupGeteuid = prevGeteuid
		findOpenClawMainCandidates = prevFind
	})

	setupPlatform = "linux"
	setupGeteuid = func() int { return 0 }
	findOpenClawMainCandidates = func(platform, fsRoot string) ([]agentdetect.HomeCandidate, error) {
		return []agentdetect.HomeCandidate{
			{RunAsUser: "alice", AgentName: "main", SessionDir: "/home/alice/.openclaw/agents/main/sessions"},
		}, nil
	}

	d, ambiguous, err := resolveDetection(&setupFlags{})
	if err != nil {
		t.Fatal(err)
	}
	if len(ambiguous) != 0 {
		t.Fatalf("unexpected ambiguous candidates: %+v", ambiguous)
	}
	if d.RunAsUser != "alice" || d.SessionDir != "/home/alice/.openclaw/agents/main/sessions" || d.Mode != "openclaw" {
		t.Fatalf("unexpected detection: %+v", d)
	}
}

func TestRunSetup_FailsPreconditionWhenSystemdMissing(t *testing.T) {
	resetEnv(t)

	prevPlatform := setupPlatform
	prevPrecondition := setupServicePrecondition
	t.Cleanup(func() {
		setupPlatform = prevPlatform
		setupServicePrecondition = prevPrecondition
	})

	setupPlatform = "linux"
	setupServicePrecondition = func() error {
		return errors.New("telemetron setup requires systemd; detected init: bash. Use 'telemetron install' + manual service management.")
	}

	cmd := newSetupCmd()
	var stdout bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)

	err := runSetup(cmd, &setupFlags{})
	if err == nil {
		t.Fatal("expected precondition failure")
	}
	if err.Error() != "precondition_failed: telemetron setup requires systemd; detected init: bash. Use 'telemetron install' + manual service management." {
		t.Fatalf("unexpected error: %q", err)
	}
}

func TestVerifyFirstFlushIncludesLastHTTPResponse(t *testing.T) {
	prevReadStatus := readSetupStatus
	t.Cleanup(func() {
		readSetupStatus = prevReadStatus
	})

	readSetupStatus = func() (status.Snapshot, error) {
		return status.Snapshot{
			LastHTTPStatus: 403,
			LastHTTPBody:   "forbidden_token_invalid",
		}, nil
	}

	err := verifyFirstFlush(&setupEmitter{}, 0)
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if !strings.Contains(err.Error(), "last HTTP response: 403 forbidden_token_invalid") {
		t.Fatalf("unexpected error: %q", err)
	}
}

func TestNewSetupCmdSilencesUsage(t *testing.T) {
	if !newSetupCmd().SilenceUsage {
		t.Fatal("setup command must silence usage on runtime errors")
	}
}

func TestConfigStateHashChangesWithConfigOrToken(t *testing.T) {
	base := configStateHash([]byte("config-a"), []byte("token-a"))
	if base == configStateHash([]byte("config-b"), []byte("token-a")) {
		t.Fatal("config hash should change when config changes")
	}
	if base == configStateHash([]byte("config-a"), []byte("token-b")) {
		t.Fatal("config hash should change when token changes")
	}
}

func TestRunSetup_SkipsRestartWhenConfigUnchanged(t *testing.T) {
	resetEnv(t)

	dir := t.TempDir()
	configPath := dir + "/config.yaml"
	tokenPath := dir + "/token"
	configYAML := strings.TrimSpace(`
endpoint: https://existing.example/v1/metrics
mode: openclaw
run_as: existing-user
token_file: `+tokenPath+`
declared:
  deployment_id: existing-deployment
  tier: production
openclaw:
  session_dir: /existing/sessions
`) + "\n"
	if err := os.WriteFile(configPath, []byte(configYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tokenPath, []byte("secret"), 0o400); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TELEMETRON_CONFIG", configPath)

	prevPlatform := setupPlatform
	prevGeteuid := setupGeteuid
	prevPrecondition := setupServicePrecondition
	prevNewService := newSetupService
	t.Cleanup(func() {
		setupPlatform = prevPlatform
		setupGeteuid = prevGeteuid
		setupServicePrecondition = prevPrecondition
		newSetupService = prevNewService
	})

	setupPlatform = "linux"
	setupGeteuid = func() int { return 1000 }
	setupServicePrecondition = func() error { return nil }
	newSetupService = func() service.Service {
		t.Fatal("service should not be constructed on unchanged setup")
		return nil
	}

	cmd := newSetupCmd()
	var stdout bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)

	err := runSetup(cmd, &setupFlags{
		nonInteractive: true,
		yes:            true,
		mode:           "openclaw",
		sessionDir:     "/existing/sessions",
		runAs:          "existing-user",
		tokenFile:      tokenPath,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "telemetron unchanged") {
		t.Fatalf("expected unchanged output, got %q", stdout.String())
	}
}

func TestRunSetup_EmitsProgressSteps(t *testing.T) {
	resetEnv(t)

	dir := t.TempDir()
	configPath := dir + "/config.yaml"
	tokenPath := dir + "/token"
	if err := os.WriteFile(tokenPath, []byte("secret"), 0o400); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TELEMETRON_CONFIG", configPath)

	prevPlatform := setupPlatform
	prevGeteuid := setupGeteuid
	prevPrecondition := setupServicePrecondition
	prevNewService := newSetupService
	prevReadStatus := readSetupStatus
	t.Cleanup(func() {
		setupPlatform = prevPlatform
		setupGeteuid = prevGeteuid
		setupServicePrecondition = prevPrecondition
		newSetupService = prevNewService
		readSetupStatus = prevReadStatus
	})

	setupPlatform = "linux"
	setupGeteuid = func() int { return 1000 }
	setupServicePrecondition = func() error { return nil }
	newSetupService = func() service.Service { return fakeSetupService{} }
	readSetupStatus = func() (status.Snapshot, error) {
		return status.Snapshot{LastFlushAt: time.Now().UTC(), LastHTTPStatus: 200}, nil
	}

	cmd := newSetupCmd()
	var stdout bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)

	err := runSetup(cmd, &setupFlags{
		nonInteractive: true,
		yes:            true,
		endpoint:       "https://example.test/v1/metrics",
		tokenFile:      tokenPath,
		mode:           "openclaw",
		sessionDir:     "/tmp/sessions",
		runAs:          "alice",
	})
	if err != nil {
		t.Fatal(err)
	}

	output := stdout.String()
	for _, step := range []string{
		"[1/4] writing config + token",
		"[2/4] installing telemetron.service",
		"[3/4] enabling + starting telemetron.service",
		"[4/4] probing first flush",
	} {
		if !strings.Contains(output, step) {
			t.Fatalf("missing progress step %q in output %q", step, output)
		}
	}
}

type fakeSetupService struct{}

func (fakeSetupService) Install(config.Config, string) error           { return nil }
func (fakeSetupService) InstallAs(config.Config, string, string) error { return nil }
func (fakeSetupService) Uninstall() error                              { return nil }
func (fakeSetupService) EnableAndStart() error                         { return nil }
func (fakeSetupService) ProbeStatus() (service.Status, error)          { return service.Status{}, nil }

func TestDefaultDeploymentID(t *testing.T) {
	if got := defaultDeploymentID("main"); !startsWithLokiPrefix(got) {
		t.Errorf("want 'loki@...', got %q", got)
	}
	if got := defaultDeploymentID("other"); !startsWithLokiPrefix(got) {
		t.Errorf("want 'loki-<agent>@...', got %q", got)
	}
}

func TestHintForMissing(t *testing.T) {
	h := hintForMissing([]string{"endpoint", "token"})
	if h == "" {
		t.Fatalf("empty hint")
	}
}

func TestResolveHealthTimeout_DefaultAndOverrides(t *testing.T) {
	resetEnv(t)

	timeout, err := resolveHealthTimeout(&setupFlags{})
	if err != nil {
		t.Fatal(err)
	}
	if timeout != 60*time.Second {
		t.Fatalf("want default 60s, got %s", timeout)
	}

	t.Setenv("TELEMETRON_HEALTH_TIMEOUT", "45s")
	timeout, err = resolveHealthTimeout(&setupFlags{})
	if err != nil {
		t.Fatal(err)
	}
	if timeout != 45*time.Second {
		t.Fatalf("want env override 45s, got %s", timeout)
	}

	timeout, err = resolveHealthTimeout(&setupFlags{healthTimeout: "90s"})
	if err != nil {
		t.Fatal(err)
	}
	if timeout != 90*time.Second {
		t.Fatalf("want flag override 90s, got %s", timeout)
	}
}

func TestResolveHealthTimeout_RejectsInvalidValues(t *testing.T) {
	resetEnv(t)

	if _, err := resolveHealthTimeout(&setupFlags{healthTimeout: "nope"}); err == nil {
		t.Fatal("expected parse error")
	}
	if _, err := resolveHealthTimeout(&setupFlags{healthTimeout: "0s"}); err == nil {
		t.Fatal("expected non-positive timeout error")
	}
}

// --- helpers ------------------------------------------------------------

func resetEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{
		"TELEMETRON_ENDPOINT", "TELEMETRON_TOKEN", "TELEMETRON_TOKEN_FILE",
		"TELEMETRON_MODE", "TELEMETRON_SESSION_DIR", "TELEMETRON_RUN_AS",
		"TELEMETRON_DEPLOYMENT_ID", "TELEMETRON_TIER", "TELEMETRON_HEALTH_TIMEOUT",
		"SUDO_USER",
	} {
		t.Setenv(k, "")
	}
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

func startsWithLokiPrefix(s string) bool {
	if len(s) < 5 {
		return false
	}
	return s[:5] == "loki@" || s[:5] == "loki-"
}

// Keep HOME/user import-free.
var _ = os.Getenv
