// SPDX-License-Identifier: Apache-2.0

package enroll

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const (
	testInstallID = "550e8400-e29b-41d4-a716-446655440000"
	testMachineID = "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
)

var testToken = "lpk_enroll_" + strings.Repeat("0123456789abcdef", 4)

func TestClientEnrollHappyPath(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Content-Type"); got != "application/json" {
			t.Fatalf("unexpected content-type: %q", got)
		}
		_, _ = w.Write([]byte(`{"token":"` + testToken + `","install_id":"` + testInstallID + `"}`))
	}))
	defer server.Close()

	client := NewClient(server.URL, server.Client())
	resp, err := client.Enroll(context.Background(), EnrollRequest{
		InstallID:         testInstallID,
		MachineID:         testMachineID,
		OS:                "linux",
		Arch:              "amd64",
		Source:            "telemetron-standalone",
		TelemetronVersion: "0.3.0",
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Token != testToken {
		t.Fatalf("unexpected token: %q", resp.Token)
	}
	if resp.InstallID != testInstallID {
		t.Fatalf("unexpected install id: %q", resp.InstallID)
	}
}

func TestClientEnrollConflictReturnsErrConflict(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "conflict", http.StatusConflict)
	}))
	defer server.Close()

	client := NewClient(server.URL, server.Client())
	_, err := client.Enroll(context.Background(), EnrollRequest{
		InstallID:         testInstallID,
		MachineID:         testMachineID,
		OS:                "linux",
		Arch:              "amd64",
		Source:            "telemetron-standalone",
		TelemetronVersion: "0.3.0",
	})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("expected ErrConflict, got %v", err)
	}
}

func TestClientEnrollRetriesOnceOnServerError(t *testing.T) {
	t.Parallel()

	var attempts int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		http.Error(w, "boom", http.StatusBadGateway)
	}))
	defer server.Close()

	client := NewClient(server.URL, server.Client())
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	_, err := client.Enroll(ctx, EnrollRequest{
		InstallID:         testInstallID,
		MachineID:         testMachineID,
		OS:                "linux",
		Arch:              "amd64",
		Source:            "telemetron-standalone",
		TelemetronVersion: "0.3.0",
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if attempts != 2 {
		t.Fatalf("expected 2 attempts, got %d", attempts)
	}
	retryable, ok := err.(interface{ Retryable() bool })
	if !ok || !retryable.Retryable() {
		t.Fatalf("expected retryable error, got %T %v", err, err)
	}
}

func TestClientEnrollRejectsMalformedToken(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"token":"bad-token","install_id":"` + testInstallID + `"}`))
	}))
	defer server.Close()

	client := NewClient(server.URL, server.Client())
	_, err := client.Enroll(context.Background(), EnrollRequest{
		InstallID:         testInstallID,
		MachineID:         testMachineID,
		OS:                "linux",
		Arch:              "amd64",
		Source:            "telemetron-standalone",
		TelemetronVersion: "0.3.0",
	})
	if err == nil {
		t.Fatal("expected error")
	}
}
