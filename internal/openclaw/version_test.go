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

func TestParseVersionOutput(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"OpenClaw 2026.5.3-1 (2eae30e)\n", "2026.5.3-1"},
		{"OpenClaw 2026.4.14 (abc1234)\n", "2026.4.14"},
		{"openclaw 2026.3.8 (def5678)\n", "2026.3.8"},
		{"2026.5.3-1\n", "2026.5.3-1"},
		{"", ""},
		{"   \n", ""},
	}
	for _, tt := range tests {
		got := parseVersionOutput(tt.input)
		if got != tt.want {
			t.Errorf("parseVersionOutput(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestDetectVersionCLIFallback(t *testing.T) {
	// This test exercises the real CLI fallback on this machine.
	// It uses candidateHomes() (same as production) to find mise paths.
	// Works both as the install user and as root with TELEMETRON_RUN_AS.
	homes := candidateHomes()
	found := false
	for _, home := range homes {
		nodeDir := filepath.Join(home, ".local", "share", "mise", "installs", "node")
		entries, err := os.ReadDir(nodeDir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			ocBin := filepath.Join(nodeDir, e.Name(), "bin", "openclaw")
			if _, err := os.Stat(ocBin); err == nil {
				found = true
				break
			}
		}
		if found {
			break
		}
	}
	if !found {
		t.Skip("openclaw not installed in any candidate home's mise node version")
	}

	v := detectVersionCLI()
	if v == "" {
		t.Error("detectVersionCLI() returned empty, expected a version string")
	}
	t.Logf("detectVersionCLI() = %q", v)
}

func TestDetectVersionNoMeta(t *testing.T) {
	// Simulate a fresh install: openclaw.json exists but has no meta block.
	// DetectVersion should fall through to CLI fallback.
	dir := t.TempDir()
	ocDir := filepath.Join(dir, ".openclaw")
	if err := os.MkdirAll(ocDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Config with no meta block (like fresh installs)
	configJSON := `{"channels":{"telegram":{"enabled":true}}}`
	if err := os.WriteFile(filepath.Join(ocDir, "openclaw.json"), []byte(configJSON), 0o644); err != nil {
		t.Fatal(err)
	}

	// readVersionFromHome should return empty for this
	v := readVersionFromHome(dir)
	if v != "" {
		t.Errorf("readVersionFromHome should return empty for config without meta, got %q", v)
	}
}
