// SPDX-License-Identifier: Apache-2.0

package tier

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDetectAndWrite(t *testing.T) {
	// Integration test — only runs if AWS CLI is available and configured
	if _, err := os.Stat("/usr/local/bin/aws"); err != nil {
		if _, err := os.Stat("/usr/bin/aws"); err != nil {
			t.Skip("aws CLI not found")
		}
	}

	// Set up temp tier file
	dir := t.TempDir()
	lokiDir := filepath.Join(dir, ".loki")
	if err := os.MkdirAll(lokiDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(lokiDir, "tier"), []byte("external\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Override HOME so candidateHomes finds our temp dir
	t.Setenv("HOME", dir)

	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	DetectAndWrite(logger)

	// Read result
	data, err := os.ReadFile(filepath.Join(lokiDir, "tier"))
	if err != nil {
		t.Fatal(err)
	}
	got := strings.TrimSpace(string(data))
	t.Logf("tier file after detection: %q", got)
	// Must be a valid tier (detection either upgraded or left as-is)
	if got != "internal" && got != "external" && got != "test" {
		t.Errorf("unexpected tier value: %q", got)
	}
}

func TestReadCurrentTier(t *testing.T) {
	dir := t.TempDir()
	lokiDir := filepath.Join(dir, ".loki")
	if err := os.MkdirAll(lokiDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(lokiDir, "tier"), []byte("internal\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("HOME", dir)

	got := readCurrentTier()
	if got != "internal" {
		t.Errorf("readCurrentTier() = %q, want %q", got, "internal")
	}
}

func TestNoDowngrade(t *testing.T) {
	// If current tier is already "internal", DetectAndWrite should not downgrade
	dir := t.TempDir()
	lokiDir := filepath.Join(dir, ".loki")
	if err := os.MkdirAll(lokiDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(lokiDir, "tier"), []byte("internal\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("HOME", dir)

	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	DetectAndWrite(logger)

	data, err := os.ReadFile(filepath.Join(lokiDir, "tier"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "internal\n" {
		t.Errorf("tier was downgraded: %q", string(data))
	}
}
