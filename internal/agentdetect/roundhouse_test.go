// SPDX-License-Identifier: Apache-2.0

package agentdetect

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDetectRoundhouse_SessionsDirExists(t *testing.T) {
	home := t.TempDir()
	sessionsDir := filepath.Join(home, ".roundhouse", "sessions", "main")
	require.NoError(t, os.MkdirAll(sessionsDir, 0o755))

	d, err := DetectRoundhouse(Options{HomeDirOverride: home})
	require.NoError(t, err)
	assert.Equal(t, "roundhouse", d.Mode)
	assert.Equal(t, sessionsDir, d.SessionDir)
	assert.Equal(t, "main", d.AgentName)
}

func TestDetectRoundhouse_NoDir(t *testing.T) {
	home := t.TempDir()

	d, err := DetectRoundhouse(Options{HomeDirOverride: home})
	require.NoError(t, err)
	assert.Empty(t, d.Mode)
}

func TestDetectAll_BothPresent(t *testing.T) {
	home := t.TempDir()
	ocDir := filepath.Join(home, ".openclaw", "agents", "main", "sessions")
	rhDir := filepath.Join(home, ".roundhouse", "sessions", "main")
	require.NoError(t, os.MkdirAll(ocDir, 0o755))
	require.NoError(t, os.MkdirAll(rhDir, 0o755))

	results, errs := DetectAll(Options{HomeDirOverride: home})
	assert.Empty(t, errs)
	assert.Len(t, results, 2)

	modes := make(map[string]string)
	for _, d := range results {
		modes[d.Mode] = d.SessionDir
	}
	assert.Equal(t, ocDir, modes["openclaw"])
	assert.Equal(t, rhDir, modes["roundhouse"])
}

func TestDetectAll_OnlyOpenClaw(t *testing.T) {
	home := t.TempDir()
	ocDir := filepath.Join(home, ".openclaw", "agents", "main", "sessions")
	require.NoError(t, os.MkdirAll(ocDir, 0o755))

	results, errs := DetectAll(Options{HomeDirOverride: home})
	assert.Empty(t, errs)
	assert.Len(t, results, 1)
	assert.Equal(t, "openclaw", results[0].Mode)
}

func TestDetectAll_NonePresent(t *testing.T) {
	home := t.TempDir()
	results, errs := DetectAll(Options{HomeDirOverride: home})
	assert.Empty(t, errs)
	assert.Empty(t, results)
}
