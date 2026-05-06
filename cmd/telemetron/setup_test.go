// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/inceptionstack/telemetron/internal/agentdetect"
	"github.com/inceptionstack/telemetron/internal/config"
	"github.com/inceptionstack/telemetron/internal/enroll"
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
		tier:         "external",
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
	if r.tier != "external" {
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
  tier: external
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
	statusFile := filepath.Join(t.TempDir(), "status.json")
	store := status.New(statusFile)
	_ = store.Write(status.Snapshot{
		LastHTTPStatus: 403,
		LastHTTPBody:   "forbidden_token_invalid",
	})

	err := verifyFirstFlush(&setupEmitter{}, 0, statusFile)
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
  tier: external
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
	prevNewService := newSetupServiceForInstance
	t.Cleanup(func() {
		setupPlatform = prevPlatform
		setupGeteuid = prevGeteuid
		setupServicePrecondition = prevPrecondition
		newSetupServiceForInstance = prevNewService
	})

	setupPlatform = "linux"
	setupGeteuid = func() int { return 1000 }
	setupServicePrecondition = func() error { return nil }
	newSetupServiceForInstance = func(_ string) service.Service {
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
	prevNewService := newSetupServiceForInstance
	prevStatusOverride := setupStatusFileOverride
	t.Cleanup(func() {
		setupPlatform = prevPlatform
		setupGeteuid = prevGeteuid
		setupServicePrecondition = prevPrecondition
		newSetupServiceForInstance = prevNewService
		setupStatusFileOverride = prevStatusOverride
	})

	// Write a status file that indicates a successful flush
	statusFile := filepath.Join(t.TempDir(), "status.json")
	_ = status.New(statusFile).Write(status.Snapshot{LastFlushAt: time.Now().UTC(), LastHTTPStatus: 200})
	setupStatusFileOverride = statusFile

	setupPlatform = "linux"
	setupGeteuid = func() int { return 1000 }
	setupServicePrecondition = func() error { return nil }
	newSetupServiceForInstance = func(_ string) service.Service { return fakeSetupService{} }

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

func TestRunSetup_AutoEnrollOptOutSkipsServiceStart(t *testing.T) {
	resetEnv(t)
	t.Setenv("TELEMETRON_NO_AUTO_ENROLL", "1")
	t.Setenv("TELEMETRON_CONFIG", t.TempDir()+"/config.yaml")

	prevPlatform := setupPlatform
	prevGeteuid := setupGeteuid
	prevPrecondition := setupServicePrecondition
	prevNewService := newSetupServiceForInstance
	prevSetupTokenPath := setupTokenPath
	t.Cleanup(func() {
		setupPlatform = prevPlatform
		setupGeteuid = prevGeteuid
		setupServicePrecondition = prevPrecondition
		newSetupServiceForInstance = prevNewService
		setupTokenPath = prevSetupTokenPath
	})

	setupPlatform = "linux"
	setupGeteuid = func() int { return 1000 }
	setupServicePrecondition = func() error { return nil }
	setupTokenPath = t.TempDir() + "/missing-token"
	newSetupServiceForInstance = func(_ string) service.Service {
		t.Fatal("service should not be constructed when auto-enroll is opted out")
		return nil
	}

	cmd := newSetupCmd()
	var stdout bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)

	err := runSetup(cmd, &setupFlags{
		nonInteractive: true,
		yes:            true,
		endpoint:       "https://example.test/v1/metrics",
		mode:           "openclaw",
		sessionDir:     "/tmp/sessions",
		runAs:          "alice",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "TELEMETRON_NO_AUTO_ENROLL=1") {
		t.Fatalf("expected opt-out message, got %q", stdout.String())
	}
}

func TestRunSetup_AutoEnrollUsesEnrolledToken(t *testing.T) {
	resetEnv(t)
	t.Setenv("TELEMETRON_CONFIG", t.TempDir()+"/config.yaml")
	t.Setenv("TELEMETRON_ENROLL_ENDPOINT", "http://test-enroll.local")
	enrolledToken := "lpk_enroll_" + strings.Repeat("0123456789abcdef", 4)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"token":"` + enrolledToken + `","install_id":"550e8400-e29b-41d4-a716-446655440000"}`))
	}))
	defer server.Close()

	prevPlatform := setupPlatform
	prevGeteuid := setupGeteuid
	prevPrecondition := setupServicePrecondition
	prevNewService := newSetupServiceForInstance
	prevStatusOverride := setupStatusFileOverride
	prevNewEnrollClient := newEnrollClient
	prevReadOrGenerateInstallID := readOrGenerateInstallID
	prevComputeMachineID := computeMachineID
	prevSetupTokenPath := setupTokenPath
	t.Cleanup(func() {
		setupPlatform = prevPlatform
		setupGeteuid = prevGeteuid
		setupServicePrecondition = prevPrecondition
		newSetupServiceForInstance = prevNewService
		setupStatusFileOverride = prevStatusOverride
		newEnrollClient = prevNewEnrollClient
		readOrGenerateInstallID = prevReadOrGenerateInstallID
		computeMachineID = prevComputeMachineID
		setupTokenPath = prevSetupTokenPath
	})

	// Write a status file that indicates a successful flush
	statusFile := filepath.Join(t.TempDir(), "status.json")
	_ = status.New(statusFile).Write(status.Snapshot{LastFlushAt: time.Now().UTC(), LastHTTPStatus: 200})
	setupStatusFileOverride = statusFile

	setupPlatform = "linux"
	setupGeteuid = func() int { return 1000 }
	setupServicePrecondition = func() error { return nil }
	setupTokenPath = t.TempDir() + "/missing-token"
	newEnrollClient = func(endpoint string, httpClient *http.Client) *enroll.Client {
		return enroll.NewClient(server.URL, server.Client())
	}
	readOrGenerateInstallID = func(path string) (string, error) {
		return "550e8400-e29b-41d4-a716-446655440000", nil
	}
	computeMachineID = func() (string, error) {
		return "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef", nil
	}

	fake := &capturingSetupService{}
	newSetupServiceForInstance = func(_ string) service.Service { return fake }

	cmd := newSetupCmd()
	var stdout bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)

	err := runSetup(cmd, &setupFlags{
		nonInteractive: true,
		yes:            true,
		endpoint:       "https://example.test/v1/metrics",
		mode:           "openclaw",
		sessionDir:     "/tmp/sessions",
		runAs:          "alice",
	})
	if err != nil {
		t.Fatal(err)
	}
	if fake.token != enrolledToken {
		t.Fatalf("unexpected token passed to service: %q", fake.token)
	}
}

func TestRunSetup_AutoEnrollWritesInstallIDAndTokenFiles(t *testing.T) {
	resetEnv(t)
	t.Setenv("TELEMETRON_ENROLL_ENDPOINT", "http://test-enroll.local")

	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	installIDPath := filepath.Join(dir, "install-id")
	tokenPath := filepath.Join(dir, "token")
	t.Setenv("TELEMETRON_CONFIG", configPath)

	enrolledToken := "lpk_enroll_" + strings.Repeat("0123456789abcdef", 4)
	const installID = "550e8400-e29b-41d4-a716-446655440000"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"token":"` + enrolledToken + `","install_id":"` + installID + `"}`))
	}))
	defer server.Close()

	prevPlatform := setupPlatform
	prevGeteuid := setupGeteuid
	prevPrecondition := setupServicePrecondition
	prevNewService := newSetupServiceForInstance
	prevStatusOverride := setupStatusFileOverride
	prevNewEnrollClient := newEnrollClient
	prevComputeMachineID := computeMachineID
	prevReadOrGenerate := readOrGenerateInstallID
	prevSetupInstallIDPath := setupInstallIDPath
	prevSetupTokenPath := setupTokenPath
	t.Cleanup(func() {
		setupPlatform = prevPlatform
		setupGeteuid = prevGeteuid
		setupServicePrecondition = prevPrecondition
		newSetupServiceForInstance = prevNewService
		setupStatusFileOverride = prevStatusOverride
		newEnrollClient = prevNewEnrollClient
		computeMachineID = prevComputeMachineID
		readOrGenerateInstallID = prevReadOrGenerate
		setupInstallIDPath = prevSetupInstallIDPath
		setupTokenPath = prevSetupTokenPath
	})

	// Write a status file that indicates a successful flush
	statusFile := filepath.Join(t.TempDir(), "status.json")
	_ = status.New(statusFile).Write(status.Snapshot{LastFlushAt: time.Now().UTC(), LastHTTPStatus: 200})
	setupStatusFileOverride = statusFile

	setupPlatform = "linux"
	setupGeteuid = func() int { return 1000 }
	setupServicePrecondition = func() error { return nil }
	newEnrollClient = func(endpoint string, httpClient *http.Client) *enroll.Client {
		return enroll.NewClient(server.URL, server.Client())
	}
	computeMachineID = func() (string, error) {
		return "sha256:" + strings.Repeat("a", 64), nil
	}
	// Return the same install-id the fake server echoes, so the client's
	// response-mismatch guard doesn't trip. Also persist to disk the way
	// the real installid.ReadOrGenerate would, so the downstream file
	// assertions remain meaningful.
	readOrGenerateInstallID = func(path string) (string, error) {
		if err := os.WriteFile(path, []byte(installID+"\n"), 0o644); err != nil {
			return "", err
		}
		return installID, nil
	}
	setupInstallIDPath = installIDPath
	setupTokenPath = tokenPath
	newSetupServiceForInstance = func(_ string) service.Service {
		// Use a tempdir-aware service stub that writes the token to the
		// path this test controls, rather than cfg.TokenFile (which would
		// default to the real /etc/telemetron/token). This mirrors what
		// the production service does, scoped to the test tempdir.
		return &tempdirWritingSetupService{tokenPath: tokenPath}
	}

	cmd := newSetupCmd()
	var stdout bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)

	err := runSetup(cmd, &setupFlags{
		nonInteractive: true,
		yes:            true,
		endpoint:       "https://example.test/v1/metrics",
		mode:           "openclaw",
		sessionDir:     "/tmp/sessions",
		runAs:          "alice",
	})
	if err != nil {
		t.Fatal(err)
	}

	installData, err := os.ReadFile(installIDPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(installData)) != installID {
		t.Fatalf("unexpected install-id contents: %q", string(installData))
	}
	tokenData, err := os.ReadFile(tokenPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(tokenData) != enrolledToken {
		t.Fatalf("unexpected token contents: %q", string(tokenData))
	}
	if info, err := os.Stat(installIDPath); err != nil {
		t.Fatal(err)
	} else if info.Mode().Perm() != 0o644 {
		t.Fatalf("unexpected install-id perms: %o", info.Mode().Perm())
	}
	if info, err := os.Stat(tokenPath); err != nil {
		t.Fatal(err)
	} else if info.Mode().Perm() != 0o400 {
		t.Fatalf("unexpected token perms: %o", info.Mode().Perm())
	}
}

type fakeSetupService struct{}

func (fakeSetupService) Install(config.Config, string) error           { return nil }
func (fakeSetupService) InstallAs(config.Config, string, string) error { return nil }
func (fakeSetupService) Uninstall() error                              { return nil }
func (fakeSetupService) EnableAndStart() error                         { return nil }
func (fakeSetupService) ProbeStatus() (service.Status, error)          { return service.Status{}, nil }

type capturingSetupService struct {
	token string
}

func (s *capturingSetupService) Install(config.Config, string) error { return nil }
func (s *capturingSetupService) InstallAs(_ config.Config, token, _ string) error {
	s.token = token
	return nil
}
func (s *capturingSetupService) Uninstall() error                     { return nil }
func (s *capturingSetupService) EnableAndStart() error                { return nil }
func (s *capturingSetupService) ProbeStatus() (service.Status, error) { return service.Status{}, nil }

// tempdirWritingSetupService persists the token to a caller-supplied
// path rather than cfg.TokenFile. Used by tests that want to verify the
// enrollment → install → token-on-disk flow without pre-seeding config.
type tempdirWritingSetupService struct {
	tokenPath string
}

func (tempdirWritingSetupService) Install(config.Config, string) error { return nil }
func (s tempdirWritingSetupService) InstallAs(_ config.Config, token, _ string) error {
	if err := os.MkdirAll(filepath.Dir(s.tokenPath), 0o755); err != nil {
		return err
	}
	return os.WriteFile(s.tokenPath, []byte(token), 0o400)
}
func (tempdirWritingSetupService) Uninstall() error                     { return nil }
func (tempdirWritingSetupService) EnableAndStart() error                { return nil }
func (tempdirWritingSetupService) ProbeStatus() (service.Status, error) { return service.Status{}, nil }

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

func TestRenderSummaryIncludesSecretTokenSource(t *testing.T) {
	t.Setenv("TELEMETRON_TOKEN_SECRET", "aws-secret-id")

	summary := renderSummary(resolvedSetup{
		tokenFile: "/etc/telemetron/token",
	})
	if !strings.Contains(summary, "TELEMETRON_TOKEN_SECRET=aws-secret-id") {
		t.Fatalf("expected secret token source in summary, got %q", summary)
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

// TestLoadExistingConfigReturnsNilWhenNoConfigFileOnDisk is a regression
// test for the v0.3.0-enrollment show-stopper discovered on a clean
// host (2026-05-03): config.Load returns a fully-defaulted Config{}
// whenever ValidateBootstrap is satisfied — including the case where
// TELEMETRON_ENDPOINT is set in the environment and no config file
// exists on disk. loadExistingConfig used to report a non-nil
// "existing install" in that case, and its default TokenFile
// (/etc/telemetron/token) got assigned to r.tokenFile. That tripped
// the tokenFile branch in loadTokenOrEnroll and auto-enroll never ran
// on a fresh install — the primary use case of v0.3.0.
//
// If this test starts failing, setup will regress to "token_read_failed"
// on every first-time install where TELEMETRON_ENDPOINT is exported
// (which is the path every bundled installer uses).
func TestLoadExistingConfigReturnsNilWhenNoConfigFileOnDisk(t *testing.T) {
	resetEnv(t)
	dir := t.TempDir()
	missing := filepath.Join(dir, "config.yaml")
	t.Setenv("TELEMETRON_CONFIG", missing)
	// Mirror the auto-enroll path: endpoint is set by the installer
	// before setup runs, which makes config.Load's ValidateBootstrap
	// pass with only defaults — the exact scenario that used to return
	// a spurious non-nil Config from loadExistingConfig.
	t.Setenv("TELEMETRON_ENDPOINT", "https://example.test/v1/metrics")
	t.Setenv("TELEMETRON_MODE", "openclaw")

	if got := loadExistingConfig(""); got != nil {
		t.Fatalf("loadExistingConfig() = %#v; want nil when %s does not exist", got, missing)
	}
}

// TestLoadExistingConfigReturnsConfigWhenFileExists keeps the happy path
// covered so the fix above doesn't silently over-correct and break the
// "reconcile against an existing install" behaviour.
func TestLoadExistingConfigReturnsConfigWhenFileExists(t *testing.T) {
	resetEnv(t)
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	tokenPath := filepath.Join(dir, "token")
	if err := os.WriteFile(cfgPath, []byte("mode: openclaw\nendpoint: https://example.test\ntoken_file: "+tokenPath+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TELEMETRON_CONFIG", cfgPath)

	got := loadExistingConfig("")
	if got == nil {
		t.Fatalf("loadExistingConfig() = nil; want *config.Config when %s exists", cfgPath)
	}
	if got.Endpoint != "https://example.test" {
		t.Fatalf("Endpoint = %q; want https://example.test", got.Endpoint)
	}
	if got.TokenFile != tokenPath {
		t.Fatalf("TokenFile = %q; want %q", got.TokenFile, tokenPath)
	}
}

func resetEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{
		"TELEMETRON_ENDPOINT", "TELEMETRON_TOKEN", "TELEMETRON_TOKEN_FILE",
		"TELEMETRON_TOKEN_SECRET",
		"TELEMETRON_MODE", "TELEMETRON_SESSION_DIR", "TELEMETRON_RUN_AS",
		"TELEMETRON_DEPLOYMENT_ID", "TELEMETRON_TIER", "TELEMETRON_HEALTH_TIMEOUT",
		"TELEMETRON_NO_AUTO_ENROLL", "TELEMETRON_ENROLL_ENDPOINT", "SUDO_USER",
		"TELEMETRON_CONFIG",
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
