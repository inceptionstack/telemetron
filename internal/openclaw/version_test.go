// SPDX-License-Identifier: Apache-2.0

package openclaw

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDetectVersion(t *testing.T) {
	dir := t.TempDir()
	ocDir := filepath.Join(dir, ".openclaw")
	if err := os.MkdirAll(ocDir, 0o755); err != nil {
		t.Fatal(err)
	}
	configJSON := `{"meta":{"lastTouchedVersion":"2026.4.14","lastTouchedAt":"2026-04-22T21:14:57.193Z"}}`
	if err := os.WriteFile(filepath.Join(ocDir, "openclaw.json"), []byte(configJSON), 0o644); err != nil {
		t.Fatal(err)
	}

	v := readVersionFromHome(dir)
	if v != "2026.4.14" {
		t.Errorf("got %q, want %q", v, "2026.4.14")
	}
}

func TestDetectVersionMissing(t *testing.T) {
	v := readVersionFromHome(t.TempDir())
	if v != "" {
		t.Errorf("expected empty, got %q", v)
	}
}

func TestDetectVersionMalformed(t *testing.T) {
	dir := t.TempDir()
	ocDir := filepath.Join(dir, ".openclaw")
	if err := os.MkdirAll(ocDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ocDir, "openclaw.json"), []byte("not json"), 0o644); err != nil {
		t.Fatal(err)
	}

	v := readVersionFromHome(dir)
	if v != "" {
		t.Errorf("expected empty for malformed JSON, got %q", v)
	}
}
