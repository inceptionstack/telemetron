// SPDX-License-Identifier: Apache-2.0

package main

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/inceptionstack/telemetron/internal/config"
	"github.com/stretchr/testify/assert"
)

func TestInstallIDPathForInstance_Primary(t *testing.T) {
	expected := config.InstancePaths(runtime.GOOS, "").InstallIDFile
	assert.Equal(t, expected, installIDPathForInstance(""))
}

func TestInstallIDPathForInstance_Named(t *testing.T) {
	expected := config.InstancePaths(runtime.GOOS, "roundhouse").InstallIDFile
	assert.Equal(t, expected, installIDPathForInstance("roundhouse"))
}

func TestIsSecondaryInstance(t *testing.T) {
	tests := []struct {
		name     string
		path     string
		expected bool
	}{
		{"empty path (primary)", "", false},
		{"primary config", "/etc/telemetron/config.yaml", false},
		{"roundhouse instance", "/etc/telemetron/config-roundhouse.yaml", true},
		{"pi instance", "/etc/telemetron/config-pi.yaml", true},
		{"random yaml outside config dir", "/tmp/config-test.yaml", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			prev := configPath
			configPath = tt.path
			defer func() { configPath = prev }()

			assert.Equal(t, tt.expected, isSecondaryInstance())
		})
	}
}

func TestConfigPathForInstance(t *testing.T) {
	assert.Equal(t, "/etc/telemetron/config.yaml", configPathForInstance(""))
	assert.Equal(t, "/etc/telemetron/config-roundhouse.yaml", configPathForInstance("roundhouse"))
}

func TestDetectSetupPassesInstance(t *testing.T) {
	// When multiple detections exist, non-openclaw modes get instance names
	assert.Equal(t, "", instanceForModeInContext("openclaw", 2))
	assert.Equal(t, "roundhouse", instanceForModeInContext("roundhouse", 2))
	assert.Equal(t, "", instanceForModeInContext("roundhouse", 1)) // single = always primary
}

func TestInstanceModeMatches_WithInstance(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config-roundhouse.yaml")
	writeTestConfig(t, path, "roundhouse")
	assert.True(t, modeMatchesFile(path, "roundhouse"))
	assert.False(t, modeMatchesFile(path, "openclaw"))
}

func TestValidateInstance(t *testing.T) {
	assert.NoError(t, config.ValidateInstance(""))          // primary
	assert.NoError(t, config.ValidateInstance("roundhouse")) // valid
	assert.NoError(t, config.ValidateInstance("pi"))         // valid
	assert.NoError(t, config.ValidateInstance("my-pack"))    // valid
	assert.Error(t, config.ValidateInstance("../../tmp/evil"))  // path traversal
	assert.Error(t, config.ValidateInstance("has space"))       // invalid chars
	assert.Error(t, config.ValidateInstance("-leading-dash"))   // must start with alnum
	assert.Error(t, config.ValidateInstance("HAS_UPPER"))       // no uppercase
}

func writeTestConfig(t *testing.T, path, mode string) {
	t.Helper()
	content := "mode: " + mode + "\nendpoint: https://example.com\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
