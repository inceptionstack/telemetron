// SPDX-License-Identifier: Apache-2.0

package updater

import "os"

// Canonical filesystem paths for telemetron's managed installation.
// All path constants live here so both the updater and the service
// layer reference a single source of truth.
const (
	// LegacyBinaryPath is where `telemetron install` originally placed the binary.
	LegacyBinaryPath = "/usr/local/bin/telemetron"

	// ManagedBinDir is the directory for the self-updatable binary.
	ManagedBinDir = "/var/lib/telemetron/bin"

	// ManagedBinaryPath is the full path to the managed binary.
	ManagedBinaryPath = ManagedBinDir + "/telemetron"

	// DefaultStatePath is the path for persistent update state.
	DefaultStatePath = "/var/lib/telemetron/update-state.json"
)

// ResolveBinaryPath returns ManagedBinaryPath if it exists on disk,
// otherwise falls back to LegacyBinaryPath.
// This is used by the service layer to set ExecStart in the unit file.
var ResolveBinaryPath = resolveBinaryPathDefault

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func resolveBinaryPathDefault() string {
	// Import cycle prevention: we use os.Stat directly here rather than
	// the service layer's filesystem interface. This is acceptable because
	// path resolution only runs during install (never at runtime).
	// Tests can replace ResolveBinaryPath via the package var.
	if fileExists(ManagedBinaryPath) {
		return ManagedBinaryPath
	}
	return LegacyBinaryPath
}
