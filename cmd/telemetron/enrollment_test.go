// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/inceptionstack/telemetron/internal/config"
	"github.com/inceptionstack/telemetron/internal/enroll"
)

// Regression: review blocker #2 (2026-05-03).
//
// TELEMETRON_TOKEN_SECRET is documented as a supported token source. It is
// ordinarily resolved by install.sh, which fetches the secret from AWS
// Secrets Manager, writes /etc/telemetron/token, and then invokes
// `telemetron setup --token-file /etc/telemetron/token`.
//
// If a caller invokes `telemetron setup` directly with TELEMETRON_TOKEN_SECRET
// set but without a staged token file and without --token-file, setup MUST
// hard-fail with ErrTokenSecretNotResolved. Silently auto-enrolling would
// downgrade a managed-token flow to an anonymous one without the operator
// noticing.
func TestLoadTokenOrEnroll_TokenSecretWithNoStagedTokenFailsClosed(t *testing.T) {
	t.Setenv("TELEMETRON_TOKEN_SECRET", "aws:secret-id")
	t.Setenv("TELEMETRON_NO_AUTO_ENROLL", "")
	t.Setenv("TELEMETRON_TOKEN", "")
	t.Setenv("TELEMETRON_TOKEN_FILE", "")
	t.Setenv("TELEMETRON_ENROLL_ENDPOINT", "")

	prevSetupTokenPath := setupTokenPath
	prevNewEnrollClient := newEnrollClient
	t.Cleanup(func() {
		setupTokenPath = prevSetupTokenPath
		newEnrollClient = prevNewEnrollClient
	})
	setupTokenPath = filepath.Join(t.TempDir(), "token-that-does-not-exist")
	// A safety net: if the code regresses and tries to enroll, this would
	// hit a bogus URL. We make the enroll client a trap so the test fails
	// loudly if enrollment is attempted.
	newEnrollClient = func(endpoint string, httpClient *http.Client) *enroll.Client {
		t.Fatalf("enrollment must NOT be attempted when TELEMETRON_TOKEN_SECRET is set; called with endpoint=%q", endpoint)
		return nil
	}

	_, _, err := loadTokenOrEnroll(context.Background(), resolvedSetup{}, config.Config{
		TokenFile: setupTokenPath,
	})
	if !errors.Is(err, ErrTokenSecretNotResolved) {
		t.Fatalf("expected ErrTokenSecretNotResolved, got %v", err)
	}
}

// When install.sh has already staged the token (file exists), setup must
// read it from disk and NOT attempt enrollment, regardless of whether
// TELEMETRON_TOKEN_SECRET is set.
func TestLoadTokenOrEnroll_TokenSecretWithStagedTokenReadsFile(t *testing.T) {
	t.Setenv("TELEMETRON_TOKEN_SECRET", "aws:secret-id")
	t.Setenv("TELEMETRON_NO_AUTO_ENROLL", "")
	t.Setenv("TELEMETRON_TOKEN", "")
	t.Setenv("TELEMETRON_TOKEN_FILE", "")

	tokenPath := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(tokenPath, []byte("lpk_live_"+strings.Repeat("a", 32)+"\n"), 0o400); err != nil {
		t.Fatal(err)
	}

	prevSetupTokenPath := setupTokenPath
	prevNewEnrollClient := newEnrollClient
	t.Cleanup(func() {
		setupTokenPath = prevSetupTokenPath
		newEnrollClient = prevNewEnrollClient
	})
	setupTokenPath = tokenPath
	newEnrollClient = func(endpoint string, httpClient *http.Client) *enroll.Client {
		t.Fatalf("enrollment must NOT be attempted when a staged token exists")
		return nil
	}

	tok, src, err := loadTokenOrEnroll(context.Background(), resolvedSetup{}, config.Config{
		TokenFile: tokenPath,
	})
	if err != nil {
		t.Fatal(err)
	}
	if src != "existing" {
		t.Fatalf("expected source 'existing', got %q", src)
	}
	if !strings.HasPrefix(tok, "lpk_live_") {
		t.Fatalf("expected token from staged file, got %q", tok)
	}
}

// Baseline: when NO explicit token source is set and no opt-out, auto-enroll
// is attempted.
func TestShouldAttemptAutoEnroll_RespectsTokenSecret(t *testing.T) {
	prevSetupTokenPath := setupTokenPath
	t.Cleanup(func() { setupTokenPath = prevSetupTokenPath })
	setupTokenPath = filepath.Join(t.TempDir(), "no-token")

	cases := []struct {
		name string
		env  map[string]string
		r    resolvedSetup
		want bool
	}{
		{"no explicit sources, no token file", nil, resolvedSetup{}, true},
		{"TELEMETRON_TOKEN_SECRET set", map[string]string{"TELEMETRON_TOKEN_SECRET": "aws:x"}, resolvedSetup{}, false},
		{"TELEMETRON_TOKEN set", map[string]string{"TELEMETRON_TOKEN": "lpk_live_x"}, resolvedSetup{tokenFromEnv: "lpk_live_x"}, false},
		{"tokenFile set", nil, resolvedSetup{tokenFile: "/some/file"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, k := range []string{"TELEMETRON_TOKEN_SECRET", "TELEMETRON_TOKEN", "TELEMETRON_TOKEN_FILE"} {
				t.Setenv(k, "")
			}
			for k, v := range tc.env {
				t.Setenv(k, v)
			}
			if got := shouldAttemptAutoEnroll(tc.r); got != tc.want {
				t.Fatalf("shouldAttemptAutoEnroll: got %v, want %v", got, tc.want)
			}
		})
	}
}

// Startup test for httptest-based enrollment (sanity check nothing regresses).
func TestLoadTokenOrEnroll_AutoEnrollHappyPath(t *testing.T) {
	for _, k := range []string{"TELEMETRON_TOKEN_SECRET", "TELEMETRON_TOKEN", "TELEMETRON_TOKEN_FILE", "TELEMETRON_NO_AUTO_ENROLL"} {
		t.Setenv(k, "")
	}

	enrolledToken := "lpk_enroll_" + strings.Repeat("0123456789abcdef", 4)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"token":"` + enrolledToken + `","install_id":"550e8400-e29b-41d4-a716-446655440000"}`))
	}))
	defer server.Close()

	prevSetupTokenPath := setupTokenPath
	prevNewEnrollClient := newEnrollClient
	prevReadOrGenerate := readOrGenerateInstallID
	prevComputeMachineID := computeMachineID
	t.Cleanup(func() {
		setupTokenPath = prevSetupTokenPath
		newEnrollClient = prevNewEnrollClient
		readOrGenerateInstallID = prevReadOrGenerate
		computeMachineID = prevComputeMachineID
	})
	setupTokenPath = filepath.Join(t.TempDir(), "missing")
	newEnrollClient = func(endpoint string, httpClient *http.Client) *enroll.Client {
		return enroll.NewClient(server.URL, server.Client())
	}
	readOrGenerateInstallID = func(path string) (string, error) {
		return "550e8400-e29b-41d4-a716-446655440000", nil
	}
	computeMachineID = func() (string, error) {
		return "sha256:" + strings.Repeat("a", 64), nil
	}

	tok, src, err := loadTokenOrEnroll(context.Background(), resolvedSetup{}, config.Config{
		TokenFile: setupTokenPath,
	})
	if err != nil {
		t.Fatal(err)
	}
	if src != "auto-enroll" {
		t.Fatalf("expected source 'auto-enroll', got %q", src)
	}
	if tok != enrolledToken {
		t.Fatalf("expected enrolled token, got %q", tok)
	}
}
