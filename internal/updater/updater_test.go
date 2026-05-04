// SPDX-License-Identifier: Apache-2.0

package updater

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"
)

type mockFlushCounter struct {
	mu    sync.Mutex
	count uint64
}

func (m *mockFlushCounter) FlushCount() uint64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.count
}

func (m *mockFlushCounter) setCount(v uint64) {
	m.mu.Lock()
	m.count = v
	m.mu.Unlock()
}

func TestUpdaterCheckNoUpdate(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(Release{TagName: "v0.3.6"})
	}))
	defer srv.Close()

	u := &Updater{
		currentVersion: "v0.3.6",
		binaryPath:     "/tmp/test-telemetron",
		statePath:      filepath.Join(t.TempDir(), "state.json"),
		baseURL:        srv.URL,
		logger:         slog.New(slog.NewJSONHandler(os.Stderr, nil)),
		client:         srv.Client(),
		flushCounter:   &mockFlushCounter{},
	}

	// Patch the fetch to use our test server
	// We need to test the check method directly
	code := u.check(context.Background())
	if code != 0 {
		t.Errorf("expected 0, got %d", code)
	}
}

func TestUpdaterDownloadAndApply(t *testing.T) {
	// Create a fake archive
	dir := t.TempDir()
	binDir := filepath.Join(dir, "bin")
	os.MkdirAll(binDir, 0o755)
	binaryPath := filepath.Join(binDir, "telemetron")

	// Write a "current" binary
	os.WriteFile(binaryPath, []byte("old-binary"), 0o755)

	// Create the tarball with a new binary
	archiveBuf := createTestArchive(t, "new-binary-content")

	// Compute checksum
	hash := sha256.Sum256(archiveBuf)
	checksumLine := fmt.Sprintf("%x  telemetron_0.3.7_%s_%s.tar.gz\n",
		hash, runtime.GOOS, runtime.GOARCH)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/archive":
			w.Write(archiveBuf)
		case r.URL.Path == "/checksums":
			w.Write([]byte(checksumLine))
		}
	}))
	defer srv.Close()

	statePath := filepath.Join(dir, "state.json")
	u := &Updater{
		currentVersion: "v0.3.6",
		binaryPath:     binaryPath,
		statePath:      statePath,
		logger:         slog.New(slog.NewJSONHandler(os.Stderr, nil)),
		client:         srv.Client(),
		flushCounter:   &mockFlushCounter{},
	}

	assetName := fmt.Sprintf("telemetron_0.3.7_%s_%s.tar.gz", runtime.GOOS, runtime.GOARCH)
	rel := &Release{
		TagName: "v0.3.7",
		Assets: []Asset{
			{Name: assetName, BrowserDownloadURL: srv.URL + "/archive"},
			{Name: "checksums.txt", BrowserDownloadURL: srv.URL + "/checksums"},
		},
	}

	err := u.downloadAndApply(context.Background(), rel)
	if err != nil {
		t.Fatalf("downloadAndApply: %v", err)
	}

	// Verify new binary is in place
	data, err := os.ReadFile(binaryPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "new-binary-content" {
		t.Errorf("binary content = %q, want %q", data, "new-binary-content")
	}

	// Verify .prev exists
	prevData, err := os.ReadFile(binaryPath + ".prev")
	if err != nil {
		t.Fatal(err)
	}
	if string(prevData) != "old-binary" {
		t.Errorf("prev content = %q, want %q", prevData, "old-binary")
	}

	// Verify state has update_pending
	stateData, _ := os.ReadFile(statePath)
	var state State
	json.Unmarshal(stateData, &state)
	if !state.UpdatePending {
		t.Error("expected update_pending=true")
	}
	if state.UpdateStarted {
		t.Error("expected update_started=false")
	}
	if state.PendingVersion != "v0.3.7" {
		t.Errorf("pending_version = %q, want v0.3.7", state.PendingVersion)
	}
}

func TestConfirmUpdate(t *testing.T) {
	dir := t.TempDir()
	statePath := filepath.Join(dir, "state.json")

	fc := &mockFlushCounter{count: 10}

	u := &Updater{
		currentVersion: "v0.3.7",
		statePath:      statePath,
		logger:         slog.New(slog.NewJSONHandler(os.Stderr, nil)),
		flushCounter:   fc,
		state: State{
			UpdatePending:  true,
			UpdateStarted:  true,
			PendingVersion: "v0.3.7",
		},
	}
	writeStateTo(statePath, u.state)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// Simulate flushes
	go func() {
		time.Sleep(50 * time.Millisecond)
		fc.setCount(14) // 10 + 4 > 10 + 3
	}()

	u.confirmUpdateWithInterval(ctx, 100*time.Millisecond)

	// State should be confirmed
	u.mu.Lock()
	pending := u.state.UpdatePending
	u.mu.Unlock()
	if pending {
		t.Error("expected update_pending=false after confirmation")
	}
}

func TestCheckRollback(t *testing.T) {
	dir := t.TempDir()
	statePath := filepath.Join(dir, "update-state.json")
	binDir := filepath.Join(dir, "bin")
	os.MkdirAll(binDir, 0o755)
	binPath := filepath.Join(binDir, "telemetron")
	prevPath := binPath + ".prev"

	// Write state with update_pending=true, update_started=true
	state := State{
		UpdatePending:  true,
		UpdateStarted:  true,
		PendingVersion: "v0.3.7",
	}
	writeStateTo(statePath, state)

	os.WriteFile(binPath, []byte("bad-new"), 0o755)
	os.WriteFile(prevPath, []byte("good-old"), 0o755)

	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	rolledBack := checkRollback(logger, statePath, binPath)

	if !rolledBack {
		t.Fatal("expected rollback to return true")
	}

	// Binary should be restored
	data, _ := os.ReadFile(binPath)
	if string(data) != "good-old" {
		t.Errorf("binary = %q, want good-old", data)
	}

	// State should be cleared
	stateData, _ := os.ReadFile(statePath)
	var s State
	json.Unmarshal(stateData, &s)
	if s.UpdatePending {
		t.Error("expected update_pending=false after rollback")
	}
	if s.RolledBackVersion != "v0.3.7" {
		t.Errorf("rolled_back_version = %q, want v0.3.7", s.RolledBackVersion)
	}
}

func TestConfigDefaults(t *testing.T) {
	c := Config{}
	if !c.IsEnabled() {
		t.Error("expected default enabled=true")
	}
	if c.Interval() != 720*time.Minute {
		t.Errorf("expected 720m, got %v", c.Interval())
	}
}

func TestConfigOverrides(t *testing.T) {
	f := false
	c := Config{Enabled: &f, IntervalMinutes: 60}
	if c.IsEnabled() {
		t.Error("expected enabled=false")
	}
	if c.Interval() != 60*time.Minute {
		t.Errorf("expected 60m, got %v", c.Interval())
	}
}

func TestConfigEnvOverride(t *testing.T) {
	t.Setenv("TELEMETRON_AUTO_UPDATE", "false")
	c := Config{}
	if c.IsEnabled() {
		t.Error("expected disabled via env")
	}

	t.Setenv("TELEMETRON_AUTO_UPDATE_INTERVAL", "30")
	if c.Interval() != 30*time.Minute {
		t.Errorf("expected 30m, got %v", c.Interval())
	}
}

func createTestArchive(t *testing.T, content string) []byte {
	t.Helper()
	f, err := os.CreateTemp("", "test-archive-*.tar.gz")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(f.Name())
	defer f.Close()

	gw := gzip.NewWriter(f)
	tw := tar.NewWriter(gw)

	hdr := &tar.Header{
		Name: "telemetron",
		Mode: 0o755,
		Size: int64(len(content)),
	}
	tw.WriteHeader(hdr)
	tw.Write([]byte(content))
	tw.Close()
	gw.Close()
	f.Close()

	data, err := os.ReadFile(f.Name())
	if err != nil {
		t.Fatal(err)
	}
	return data
}
