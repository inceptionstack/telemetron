// SPDX-License-Identifier: Apache-2.0

package enroll

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"slices"
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
	resp, err := client.Enroll(context.Background(), validRequest())
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
	_, err := client.Enroll(context.Background(), validRequest())
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

	_, err := client.Enroll(ctx, validRequest())
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
	_, err := client.Enroll(context.Background(), validRequest())
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestClientEnrollDoesNotRetryNonConflict4xx(t *testing.T) {
	t.Parallel()

	for _, status := range []int{http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			t.Parallel()

			var attempts int
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				attempts++
				http.Error(w, "nope", status)
			}))
			defer server.Close()

			client := NewClient(server.URL, server.Client())
			_, err := client.Enroll(context.Background(), validRequest())
			if err == nil {
				t.Fatal("expected error")
			}
			if attempts != 1 {
				t.Fatalf("expected 1 attempt, got %d", attempts)
			}
		})
	}
}

func TestClientEnrollHonorsContextCancellationDuringRetryBackoff(t *testing.T) {
	t.Parallel()

	var attempts int
	ctx, cancel := context.WithCancel(context.Background())
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		cancel()
		http.Error(w, "boom", http.StatusBadGateway)
	}))
	defer server.Close()

	client := NewClient(server.URL, server.Client())
	_, err := client.Enroll(ctx, validRequest())
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context cancellation, got %v", err)
	}
	if attempts != 1 {
		t.Fatalf("expected 1 attempt before cancellation, got %d", attempts)
	}
}

func TestClientEnrollRequestBodyUsesCanonicalContractKeysOnly(t *testing.T) {
	t.Parallel()

	wantKeys := loadContractKeys(t)
	var gotKeys []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		for key := range payload {
			gotKeys = append(gotKeys, key)
		}
		slices.Sort(gotKeys)
		_, _ = w.Write([]byte(`{"token":"` + testToken + `","install_id":"` + testInstallID + `"}`))
	}))
	defer server.Close()

	client := NewClient(server.URL, server.Client())
	if _, err := client.Enroll(context.Background(), validRequest()); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(gotKeys, wantKeys) {
		t.Fatalf("unexpected request keys: got %v want %v", gotKeys, wantKeys)
	}
}

func TestClientEnrollRejectsOversizedResponseBody(t *testing.T) {
	t.Parallel()

	payload := `{"token":"` + testToken + `","install_id":"` + testInstallID + `","padding":"` + strings.Repeat("x", maxResponseBodyRead) + `"}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(payload))
	}))
	defer server.Close()

	client := NewClient(server.URL, server.Client())
	_, err := client.Enroll(context.Background(), validRequest())
	if err == nil || !strings.Contains(err.Error(), "exceeded") {
		t.Fatalf("expected oversized response error, got %v", err)
	}
}

func TestClientEnrollRejectsMismatchedInstallIDInResponse(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"token":"` + testToken + `","install_id":"123e4567-e89b-42d3-a456-426614174000"}`))
	}))
	defer server.Close()

	client := NewClient(server.URL, server.Client())
	_, err := client.Enroll(context.Background(), validRequest())
	if err == nil || !strings.Contains(err.Error(), "mismatch") {
		t.Fatalf("expected install_id mismatch error, got %v", err)
	}
}

func TestClientEnrollParsesJSONEvenWhenContentTypeIsNotApplicationJSON(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte(`{"token":"` + testToken + `","install_id":"` + testInstallID + `"}`))
	}))
	defer server.Close()

	client := NewClient(server.URL, server.Client())
	resp, err := client.Enroll(context.Background(), validRequest())
	if err != nil {
		t.Fatal(err)
	}
	if resp.Token != testToken {
		t.Fatalf("unexpected token: %q", resp.Token)
	}
}

func validRequest() EnrollRequest {
	return EnrollRequest{
		InstallID:         testInstallID,
		MachineID:         testMachineID,
		OS:                "linux",
		Arch:              "amd64",
		Source:            "telemetron-standalone",
		TelemetronVersion: "0.3.0",
		Pack:              "openclaw",
		Tier:              "production",
	}
}

func loadContractKeys(t *testing.T) []string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	var payload struct {
		AllowedKeys []string `json:"allowed_keys"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatal(err)
	}
	slices.Sort(payload.AllowedKeys)
	return payload.AllowedKeys
}
