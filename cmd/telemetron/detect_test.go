// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDetectCmd_DryRun_FindsRoundhouse(t *testing.T) {
	home := t.TempDir()
	sessionsDir := filepath.Join(home, ".roundhouse", "sessions", "main")
	require.NoError(t, os.MkdirAll(sessionsDir, 0o755))

	cmd := newRootCmd()
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetArgs([]string{"detect",
		"--endpoint", "https://example.com/v1/metrics",
		"--dry-run",
		"--home", home,
	})

	err := cmd.Execute()
	require.NoError(t, err)

	output := out.String()
	assert.Contains(t, output, "roundhouse")
	assert.Contains(t, output, sessionsDir)
}

func TestDetectCmd_RoundhouseOnly_IsPrimary(t *testing.T) {
	// A host with only roundhouse should treat it as primary (not skip it)
	assert.Equal(t, "", instanceForModeInContext("roundhouse", 1),
		"single roundhouse detection should be primary")
}

func TestDetectCmd_DryRun_FindsBoth(t *testing.T) {
	home := t.TempDir()
	ocDir := filepath.Join(home, ".openclaw", "agents", "main", "sessions")
	rhDir := filepath.Join(home, ".roundhouse", "sessions", "main")
	require.NoError(t, os.MkdirAll(ocDir, 0o755))
	require.NoError(t, os.MkdirAll(rhDir, 0o755))

	cmd := newRootCmd()
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetArgs([]string{"detect",
		"--endpoint", "https://example.com/v1/metrics",
		"--dry-run",
		"--home", home,
	})

	err := cmd.Execute()
	require.NoError(t, err)

	output := out.String()
	assert.Contains(t, output, "openclaw")
	assert.Contains(t, output, "roundhouse")
}

func TestDetectCmd_DryRun_NothingFound(t *testing.T) {
	home := t.TempDir()

	cmd := newRootCmd()
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetArgs([]string{"detect",
		"--endpoint", "https://example.com/v1/metrics",
		"--dry-run",
		"--home", home,
	})

	err := cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no packs detected")
}

func TestDetectCmd_MissingEndpoint(t *testing.T) {
	t.Setenv("TELEMETRON_ENDPOINT", "")
	home := t.TempDir()
	sessionsDir := filepath.Join(home, ".roundhouse", "sessions", "main")
	require.NoError(t, os.MkdirAll(sessionsDir, 0o755))

	cmd := newRootCmd()
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetArgs([]string{"detect", "--dry-run", "--home", home})

	err := cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "endpoint is required")
}

func TestDetectCmd_FilterByMode(t *testing.T) {
	home := t.TempDir()
	ocDir := filepath.Join(home, ".openclaw", "agents", "main", "sessions")
	rhDir := filepath.Join(home, ".roundhouse", "sessions", "main")
	require.NoError(t, os.MkdirAll(ocDir, 0o755))
	require.NoError(t, os.MkdirAll(rhDir, 0o755))

	cmd := newRootCmd()
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetArgs([]string{"detect",
		"--endpoint", "https://example.com/v1/metrics",
		"--dry-run",
		"--home", home,
		"--mode", "roundhouse",
	})

	err := cmd.Execute()
	require.NoError(t, err)

	output := out.String()
	assert.Contains(t, output, "roundhouse")
	assert.NotContains(t, output, "openclaw")
}

func TestInstanceForModeInContext(t *testing.T) {
	// Single detection: always primary regardless of mode
	assert.Equal(t, "", instanceForModeInContext("openclaw", 1))
	assert.Equal(t, "", instanceForModeInContext("roundhouse", 1))

	// Multiple detections: openclaw is primary, others are instanced
	assert.Equal(t, "", instanceForModeInContext("openclaw", 2))
	assert.Equal(t, "roundhouse", instanceForModeInContext("roundhouse", 2))
	assert.Equal(t, "future-pack", instanceForModeInContext("future-pack", 2))
}
