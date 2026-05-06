// SPDX-License-Identifier: Apache-2.0

package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestInstancePaths_Primary(t *testing.T) {
	paths := InstancePaths("linux", "")
	assert.Equal(t, "/etc/telemetron/config.yaml", paths.ConfigPath)
	assert.Equal(t, "/etc/telemetron/token", paths.TokenFile)
	assert.Equal(t, "/var/lib/telemetron/status.json", paths.StatusFile)
	assert.Equal(t, "", paths.Instance)
}

func TestInstancePaths_Named(t *testing.T) {
	paths := InstancePaths("linux", "roundhouse")
	assert.Equal(t, "/etc/telemetron/config-roundhouse.yaml", paths.ConfigPath)
	assert.Equal(t, "/etc/telemetron/token-roundhouse", paths.TokenFile)
	assert.Equal(t, "/var/lib/telemetron/status-roundhouse.json", paths.StatusFile)
	assert.Equal(t, "roundhouse", paths.Instance)
}

func TestInstancePaths_Darwin(t *testing.T) {
	paths := InstancePaths("darwin", "pi")
	assert.Contains(t, paths.ConfigPath, "config-pi.yaml")
	assert.Contains(t, paths.TokenFile, "token-pi")
	assert.Contains(t, paths.StatusFile, "status-pi.json")
	assert.Equal(t, "pi", paths.Instance)
}

func TestDefaultPaths_BackwardsCompatible(t *testing.T) {
	// DefaultPaths must return primary paths (no instance suffix)
	paths := DefaultPaths("linux")
	assert.Equal(t, "/etc/telemetron/config.yaml", paths.ConfigPath)
	assert.Equal(t, "/etc/telemetron/token", paths.TokenFile)
	assert.Equal(t, "/var/lib/telemetron/status.json", paths.StatusFile)
	assert.Equal(t, "", paths.Instance)
}
