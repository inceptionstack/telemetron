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

func testLogger() *slog.Logger {
	return slog.New(slog.NewJSONHandler(os.Stderr, nil))
}

func newTestUpdater(t *testing.T, opts ...func(*Updater)) *Updater {
	t.Helper()
	dir := t.TempDir()
	logger := testLogger()
	u := &Updater{
		currentVersion: "v0.3.6",
		binaryPath:     filepath.Join(dir, "telemetron"),
		logger:         logger,
		client:         http.DefaultClient,
		flushCounter:   &mockFlushCounter{},
		sf:             NewStateFile(filepath.Join(dir, "state.json"), logger),
	}
	for _, fn := range opts {
		fn(u)
	}
	return u
}

func TestUpdaterCheckNoUpdate(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewEncoder(w).Encode(Release{TagName: "v0.3.6"}); err != nil {
			t.Errorf("encode: %v", err)
		}
	}))
	defer srv.Close()

	u := newTestUpdater(t, func(u *Updater) {
		u.baseURL = srv.URL
		u.client = srv.Client()
	})

	code := u.check(context.Background())
	if code != 0 {
		t.Errorf("expected 0, got %d", code)
	}
}

func TestUpdaterDownloadAndApply(t *testing.T) {
	dir := t.TempDir()
	binDir := filepath.Join(dir, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	binaryPath := filepath.Join(binDir, "telemetron")
	if err := os.WriteFile(binaryPath, []byte("old-binary"), 0o755); err != nil {
		t.Fatal(err)
	}

	archiveBuf := createTestArchive(t, "new-binary-content")

	hash := sha256.Sum256(archiveBuf)
	checksumLine := fmt.Sprintf("%x  telemetron_0.3.7_%s_%s.tar.gz\n",
		hash, runtime.GOOS, runtime.GOARCH)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/archive":
			_, _ = w.Write(archiveBuf)
		case "/checksums":
			_, _ = w.Write([]byte(checksumLine))
		}
	}))
	defer srv.Close()

	statePath := filepath.Join(dir, "state.json")
	logger := testLogger()
	u := &Updater{
		currentVersion: "v0.3.6",
		binaryPath:     binaryPath,
		logger:         logger,
		client:         srv.Client(),
		flushCounter:   &mockFlushCounter{},
		sf:             NewStateFile(statePath, logger),
	}

	assetName := fmt.Sprintf("telemetron_0.3.7_%s_%s.tar.gz", runtime.GOOS, runtime.GOARCH)
	rel := &Release{
		TagName: "v0.3.7",
		Assets: []Asset{
			{Name: assetName, BrowserDownloadURL: srv.URL + "/archive"},
			{Name: "checksums.txt", BrowserDownloadURL: srv.URL + "/checksums"},
		},
	}

	if err := u.downloadAndApply(context.Background(), rel); err != nil {
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
	state := u.sf.Get()
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
	logger := testLogger()

	sf := NewStateFile(statePath, logger)
	if err := sf.Update(func(s *State) {
		s.UpdatePending = true
		s.UpdateStarted = true
		s.PendingVersion = "v0.3.7"
	}); err != nil {
		t.Fatal(err)
	}

	u := &Updater{
		currentVersion: "v0.3.7",
		logger:         logger,
		flushCounter:   fc,
		sf:             sf,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	go func() {
		time.Sleep(50 * time.Millisecond)
		fc.setCount(14) // 10 + 4 > 10 + 3
	}()

	u.confirmUpdateWithInterval(ctx, 100*time.Millisecond)

	state := u.sf.Get()
	if state.UpdatePending {
		t.Error("expected update_pending=false after confirmation")
	}
}

func TestCheckRollback(t *testing.T) {
	dir := t.TempDir()
	statePath := filepath.Join(dir, "update-state.json")
	binDir := filepath.Join(dir, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	binPath := filepath.Join(binDir, "telemetron")
	prevPath := binPath + ".prev"

	logger := testLogger()
	sf := NewStateFile(statePath, logger)
	if err := sf.Update(func(s *State) {
		s.UpdatePending = true
		s.UpdateStarted = true
		s.PendingVersion = "v0.3.7"
	}); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(binPath, []byte("bad-new"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(prevPath, []byte("good-old"), 0o755); err != nil {
		t.Fatal(err)
	}

	rolledBack := checkRollback(logger, statePath, binPath)

	if !rolledBack {
		t.Fatal("expected rollback to return true")
	}

	// Binary should be restored
	data, err := os.ReadFile(binPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "good-old" {
		t.Errorf("binary = %q, want good-old", data)
	}

	// State should be cleared — read from disk
	stateData, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	var s State
	if err := json.Unmarshal(stateData, &s); err != nil {
		t.Fatal(err)
	}
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
	name := f.Name()
	defer func() { _ = os.Remove(name) }()

	gw := gzip.NewWriter(f)
	tw := tar.NewWriter(gw)

	hdr := &tar.Header{
		Name: "telemetron",
		Mode: 0o755,
		Size: int64(len(content)),
	}
	if err := tw.WriteHeader(hdr); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write([]byte(content)); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
