//go:build e2e

// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/inceptionstack/telemetron/internal/config"
	"github.com/inceptionstack/telemetron/internal/otlp"
	"github.com/inceptionstack/telemetron/internal/service"
	"github.com/inceptionstack/telemetron/internal/status"
	colmetricpb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"
	"google.golang.org/protobuf/proto"
)

// This e2e test exercises the real setup flow plus the real OTLP exporter.
// The backend is faked in-process at the contract level: /v1/enroll and
// /v1/metrics re-implement the enrollment and authoritative install_id binding
// behavior in Go instead of shelling out to the Python Lambda handlers.
func TestE2EEnrollAndMetricsFlow(t *testing.T) {
	resetEnv(t)

	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	installIDPath := filepath.Join(dir, "install-id")
	tokenPath := filepath.Join(dir, "token")
	t.Setenv("TELEMETRON_CONFIG", configPath)

	state := &e2eBackendState{
		enrollmentsByInstallID: map[string]e2eEnrollment{},
		enrollmentsByTokenHash: map[string]e2eEnrollment{},
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/enroll":
			state.handleEnroll(t, w, r)
		case "/v1/metrics":
			state.handleMetrics(t, w, r)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	prevPlatform := setupPlatform
	prevGeteuid := setupGeteuid
	prevPrecondition := setupServicePrecondition
	prevNewService := newSetupService
	prevReadStatus := readSetupStatus
	prevComputeMachineID := computeMachineID
	prevSetupInstallIDPath := setupInstallIDPath
	prevSetupTokenPath := setupTokenPath
	t.Cleanup(func() {
		setupPlatform = prevPlatform
		setupGeteuid = prevGeteuid
		setupServicePrecondition = prevPrecondition
		newSetupService = prevNewService
		readSetupStatus = prevReadStatus
		computeMachineID = prevComputeMachineID
		setupInstallIDPath = prevSetupInstallIDPath
		setupTokenPath = prevSetupTokenPath
	})

	setupPlatform = "linux"
	setupGeteuid = func() int { return 1000 }
	setupServicePrecondition = func() error { return nil }
	readSetupStatus = func() (status.Snapshot, error) {
		return status.Snapshot{LastFlushAt: time.Now().UTC(), LastHTTPStatus: 200}, nil
	}
	computeMachineID = func() (string, error) {
		return "sha256:" + strings.Repeat("a", 64), nil
	}
	setupInstallIDPath = installIDPath
	setupTokenPath = tokenPath
	newSetupService = func() service.Service { return &e2eWritingSetupService{} }
	t.Setenv("TELEMETRON_ENROLL_ENDPOINT", server.URL+"/v1/enroll")

	cmd := newSetupCmd()
	runArgs := &setupFlags{
		nonInteractive: true,
		yes:            true,
		endpoint:       server.URL + "/v1/metrics",
		mode:           "openclaw",
		sessionDir:     filepath.Join(dir, "sessions"),
		runAs:          "alice",
		deploymentID:   "e2e-deployment",
		tier:           "production",
	}
	if err := runSetup(cmd, runArgs); err != nil {
		t.Fatal(err)
	}

	installData, err := os.ReadFile(installIDPath)
	if err != nil {
		t.Fatal(err)
	}
	installID := strings.TrimSpace(string(installData))
	if !regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`).MatchString(installID) {
		t.Fatalf("unexpected install-id: %q", installID)
	}
	tokenData, err := os.ReadFile(tokenPath)
	if err != nil {
		t.Fatal(err)
	}
	token := string(tokenData)
	if !regexp.MustCompile(`^lpk_enroll_[0-9a-f]{64}$`).MatchString(token) {
		t.Fatalf("unexpected token: %q", token)
	}
	if state.enrollCalls != 1 {
		t.Fatalf("expected one enroll call, got %d", state.enrollCalls)
	}
	if state.lastEnrollResponse.InstallID != installID {
		t.Fatalf("enroll response install_id mismatch: got %q want %q", state.lastEnrollResponse.InstallID, installID)
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

	exporter := otlp.NewExporter(
		server.URL+"/v1/metrics",
		token,
		declaredForExporter(config.Config{
			Declared: config.DeclaredConfig{
				DeploymentID: "e2e-deployment",
				Tier:         "production",
			},
		}, slog.New(slog.NewTextHandler(io.Discard, nil))),
		server.Client(),
	)
	resp, err := exporter.Export(context.Background(), []otlp.Point{{
		Name:  "pack.agent.turn",
		Attrs: map[string]string{"outcome": "success", "model.family": "openclaw"},
		Count: 1,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("unexpected metrics status: %d body=%q", resp.StatusCode, resp.Body)
	}
	if state.metricsCalls != 1 {
		t.Fatalf("expected one metrics call, got %d", state.metricsCalls)
	}
	if len(state.capturedResources) != 1 {
		t.Fatalf("expected one captured resource set, got %d", len(state.capturedResources))
	}
	if got := state.capturedResources[0]; len(got) != 1 || got["install_id"] != installID {
		t.Fatalf("expected authoritative install_id only, got %#v", got)
	}

	if err := runSetup(cmd, runArgs); err != nil {
		t.Fatal(err)
	}
	if state.enrollCalls != 1 {
		t.Fatalf("expected second setup to reuse existing token without re-enroll, got %d enroll calls", state.enrollCalls)
	}
}

type e2eEnrollment struct {
	InstallID string
	MachineID string
	Token     string
	Revoked   bool
}

type e2eBackendState struct {
	enrollCalls            int
	metricsCalls           int
	lastEnrollResponse     struct{ InstallID, Token string }
	enrollmentsByInstallID map[string]e2eEnrollment
	enrollmentsByTokenHash map[string]e2eEnrollment
	capturedResources      []map[string]string
}

func (s *e2eBackendState) handleEnroll(t *testing.T, w http.ResponseWriter, r *http.Request) {
	t.Helper()
	s.enrollCalls++
	defer r.Body.Close()

	var payload struct {
		Schema            string `json:"schema"`
		InstallID         string `json:"install_id"`
		MachineID         string `json:"machine_id"`
		OS                string `json:"os"`
		Arch              string `json:"arch"`
		Source            string `json:"source"`
		TelemetronVersion string `json:"telemetron_version"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		t.Fatalf("decode enroll request: %v", err)
	}
	if payload.Schema != "lowkey.enroll.v1" {
		t.Fatalf("unexpected schema: %q", payload.Schema)
	}
	if existing, ok := s.enrollmentsByInstallID[payload.InstallID]; ok {
		if existing.MachineID != payload.MachineID || existing.Revoked {
			http.Error(w, "conflict", http.StatusConflict)
			return
		}
		s.lastEnrollResponse.InstallID = existing.InstallID
		s.lastEnrollResponse.Token = existing.Token
		_ = json.NewEncoder(w).Encode(map[string]string{
			"token":      existing.Token,
			"install_id": existing.InstallID,
		})
		return
	}

	token := "lpk_enroll_" + strings.Repeat("ab", 32)
	if _, exists := s.enrollmentsByTokenHash[sha256Hex(token)]; exists {
		token = "lpk_enroll_" + strings.Repeat("cd", 32)
	}
	record := e2eEnrollment{
		InstallID: payload.InstallID,
		MachineID: payload.MachineID,
		Token:     token,
	}
	s.enrollmentsByInstallID[payload.InstallID] = record
	s.enrollmentsByTokenHash[sha256Hex(token)] = record
	s.lastEnrollResponse.InstallID = payload.InstallID
	s.lastEnrollResponse.Token = token
	_ = json.NewEncoder(w).Encode(map[string]string{
		"token":      token,
		"install_id": payload.InstallID,
	})
}

func (s *e2eBackendState) handleMetrics(t *testing.T, w http.ResponseWriter, r *http.Request) {
	t.Helper()
	s.metricsCalls++

	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	record, ok := s.enrollmentsByTokenHash[sha256Hex(token)]
	if !ok || record.Revoked {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatalf("read metrics body: %v", err)
	}
	var req colmetricpb.ExportMetricsServiceRequest
	if err := proto.Unmarshal(body, &req); err != nil {
		t.Fatalf("unmarshal metrics body: %v", err)
	}

	for i := range req.ResourceMetrics {
		req.ResourceMetrics[i].Resource = &resourcepb.Resource{
			Attributes: []*commonpb.KeyValue{{
				Key: "install_id",
				Value: &commonpb.AnyValue{
					Value: &commonpb.AnyValue_StringValue{StringValue: record.InstallID},
				},
			}},
		}
	}
	s.capturedResources = captureResourceAttrs(&req)
	w.WriteHeader(http.StatusOK)
}

func captureResourceAttrs(req *colmetricpb.ExportMetricsServiceRequest) []map[string]string {
	out := make([]map[string]string, 0, len(req.ResourceMetrics))
	for _, rm := range req.ResourceMetrics {
		attrs := map[string]string{}
		for _, kv := range rm.Resource.Attributes {
			if stringValue, ok := kv.Value.Value.(*commonpb.AnyValue_StringValue); ok {
				attrs[kv.Key] = stringValue.StringValue
			}
		}
		out = append(out, attrs)
	}
	return out
}

type e2eWritingSetupService struct{}

func (e2eWritingSetupService) Install(config.Config, string) error { return nil }
func (e2eWritingSetupService) InstallAs(cfg config.Config, token, _ string) error {
	data, err := cfg.Marshal()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(cfg.FilePath), 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(cfg.TokenFile), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(cfg.FilePath, data, 0o644); err != nil {
		return err
	}
	return os.WriteFile(cfg.TokenFile, []byte(token), 0o400)
}
func (e2eWritingSetupService) Uninstall() error                     { return nil }
func (e2eWritingSetupService) EnableAndStart() error                { return nil }
func (e2eWritingSetupService) ProbeStatus() (service.Status, error) { return service.Status{}, nil }

func sha256Hex(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
