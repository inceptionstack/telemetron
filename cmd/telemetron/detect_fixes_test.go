// SPDX-License-Identifier: Apache-2.0

package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/inceptionstack/telemetron/internal/agentdetect"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInstanceModeMatches(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")

	// Write a config with mode: openclaw
	require.NoError(t, os.WriteFile(configPath, []byte("mode: openclaw\nendpoint: https://example.com\n"), 0o644))

	// Patch configPathForInstance to use our temp dir
	// Since we can't easily patch, test the function directly with a real file
	assert.True(t, modeMatchesFile(configPath, "openclaw"))
	assert.False(t, modeMatchesFile(configPath, "roundhouse"))
	assert.False(t, modeMatchesFile(configPath, ""))
}

func TestInstanceModeMatches_MissingFile(t *testing.T) {
	assert.False(t, modeMatchesFile("/nonexistent/path/config.yaml", "openclaw"))
}

func TestInstanceModeMatches_NoModeLine(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	require.NoError(t, os.WriteFile(configPath, []byte("endpoint: https://example.com\n"), 0o644))

	assert.False(t, modeMatchesFile(configPath, "openclaw"))
}

func TestDetectFromAllHomes_SetsRunAsUser(t *testing.T) {
	// Create a fake home with openclaw sessions
	userHome := t.TempDir()
	sessionsDir := filepath.Join(userHome, ".openclaw", "agents", "main", "sessions")
	require.NoError(t, os.MkdirAll(sessionsDir, 0o755))

	// Use DetectAll with explicit HomeDirOverride and User
	// This tests that User propagates to RunAsUser in detection
	results, _ := agentdetect.DetectAll(agentdetect.Options{
		HomeDirOverride: userHome,
		User:            "fakeuser",
	})

	require.Len(t, results, 1)
	assert.Equal(t, "openclaw", results[0].Mode)
	assert.Equal(t, "fakeuser", results[0].RunAsUser)
}
