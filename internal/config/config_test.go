// SPDX-License-Identifier: Apache-2.0

package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func init() {
	RegisterMode("testmode", func(raw any) (any, error) {
		section, ok := raw.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("raw collector config must be a map")
		}
		for key := range section {
			switch key {
			case "session_dir", "flush_interval", "scan_interval":
			default:
				return nil, fmt.Errorf("unknown testmode config key %q", key)
			}
		}
		if strings.TrimSpace(fmt.Sprint(section["session_dir"])) == "" {
			return nil, fmt.Errorf("testmode.session_dir is required")
		}
		return raw, nil
	}, func(Paths) any {
		return map[string]any{
			"session_dir":    "/tmp/testmode-sessions",
			"flush_interval": "15s",
			"scan_interval":  "15s",
		}
	})
}

func TestLoadMergePrecedence(t *testing.T) {
	dir := t.TempDir()
	tokenFile := filepath.Join(dir, "token")
	require.NoError(t, os.WriteFile(tokenFile, []byte("from-file"), 0o600))

	configPath := filepath.Join(dir, "config.yaml")
	configYAML := `mode: testmode
endpoint: https://file.example/v1/metrics
token_file: ` + tokenFile + `
testmode:
  session_dir: /tmp/file-sessions
  flush_interval: 15s
  scan_interval: 15s
`
	require.NoError(t, os.WriteFile(configPath, []byte(configYAML), 0o644))

	t.Setenv("TELEMETRON_ENDPOINT", "https://env.example/v1/metrics")
	t.Setenv("TELEMETRON_MODE", "testmode")

	cfg, err := Load(LoadOptions{
		ConfigPath: configPath,
		Overrides: map[string]any{
			"endpoint": "https://flag.example/v1/metrics",
		},
	})
	require.NoError(t, err)
	require.Equal(t, "https://flag.example/v1/metrics", cfg.Endpoint)
	require.Equal(t, "/tmp/file-sessions", cfg.Collectors["testmode"].(map[string]any)["session_dir"])
}

func TestLoadNegativeCases(t *testing.T) {
	dir := t.TempDir()
	tokenFile := filepath.Join(dir, "token")
	require.NoError(t, os.WriteFile(tokenFile, []byte("from-file"), 0o600))

	tests := []struct {
		name   string
		config string
	}{
		{
			name: "unknown mode",
			config: `mode: missing
endpoint: https://example.test/v1/metrics
token_file: ` + tokenFile + `
`,
		},
		{
			name: "missing endpoint",
			config: `mode: testmode
token_file: ` + tokenFile + `
testmode:
  session_dir: /tmp/file-sessions
`,
		},
		{
			name: "insecure endpoint rejected",
			config: `mode: testmode
endpoint: http://example.test/v1/metrics
token_file: ` + tokenFile + `
testmode:
  session_dir: /tmp/file-sessions
`,
		},
		{
			name: "unknown collector sub-key",
			config: `mode: testmode
endpoint: https://example.test/v1/metrics
token_file: ` + tokenFile + `
testmode:
  session_dir: /tmp/file-sessions
  extra: nope
`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			configPath := filepath.Join(dir, tt.name+".yaml")
			require.NoError(t, os.WriteFile(configPath, []byte(tt.config), 0o644))
			_, err := Load(LoadOptions{ConfigPath: configPath})
			require.Error(t, err)
		})
	}
}

func TestConfigMarshalRoundTrip(t *testing.T) {
	dir := t.TempDir()
	tokenFile := filepath.Join(dir, "token")
	require.NoError(t, os.WriteFile(tokenFile, []byte("from-file"), 0o600))

	cfg := Config{
		Mode:      "testmode",
		Endpoint:  "https://example.test/v1/metrics",
		TokenFile: tokenFile,
		LogLevel:  "debug",
		Declared: DeclaredConfig{
			DeploymentID: "dep-1",
			Tier:         "internal",
		},
		Collectors: map[string]any{
			"testmode": map[string]any{
				"session_dir":    "/tmp/file-sessions",
				"flush_interval": "15s",
				"scan_interval":  "15s",
			},
		},
	}

	data, err := cfg.Marshal()
	require.NoError(t, err)

	var roundTrip Config
	require.NoError(t, yaml.Unmarshal(data, &roundTrip))
	require.Equal(t, cfg.Mode, roundTrip.Mode)
	require.Equal(t, cfg.Endpoint, roundTrip.Endpoint)
	require.Equal(t, cfg.TokenFile, roundTrip.TokenFile)
	require.Equal(t, cfg.LogLevel, roundTrip.LogLevel)
	require.Equal(t, cfg.Collectors, roundTrip.Collectors)
}
