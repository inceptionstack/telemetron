package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLoadMergePrecedence(t *testing.T) {
	dir := t.TempDir()
	tokenFile := filepath.Join(dir, "token")
	require.NoError(t, os.WriteFile(tokenFile, []byte("from-file"), 0o600))

	configPath := filepath.Join(dir, "config.yaml")
	configYAML := `mode: openclaw
endpoint: https://file.example/v1/metrics
token_file: ` + tokenFile + `
openclaw:
  session_dir: /tmp/file-sessions
  flush_interval: 15s
  scan_interval: 15s
`
	require.NoError(t, os.WriteFile(configPath, []byte(configYAML), 0o644))

	t.Setenv("LOKIOTEL_ENDPOINT", "https://env.example/v1/metrics")
	t.Setenv("LOKIOTEL_MODE", "openclaw")

	cfg, err := Load(LoadOptions{
		ConfigPath: configPath,
		Overrides: map[string]string{
			"endpoint": "https://flag.example/v1/metrics",
		},
	})
	require.NoError(t, err)
	require.Equal(t, "https://flag.example/v1/metrics", cfg.Endpoint)
	require.Equal(t, "/tmp/file-sessions", cfg.OpenClaw.SessionDir)
}
