// SPDX-License-Identifier: Apache-2.0

// Package telemetry implements the user-facing opt-out contract for clawtello.
//
// clawtello respects three opt-out signals, matching the lowkey installer
// family convention:
//
//  1. DO_NOT_TRACK=1 — the https://consoledonottrack.com community standard.
//  2. CLAWTELLO_TELEMETRY=0 — tool-specific, LOWKEY-style env override.
//  3. ~/.clawtello/telemetry-off — marker file (works for service users
//     whose env cannot be changed easily).
//
// When any signal is present the exporter must not run: no config load,
// no token read, no network sockets.
package telemetry

import (
	"os"
	"path/filepath"
	"strings"
)

// optOutEnvVars map an env var name to the value that means "opted out".
// "*" means any truthy value triggers opt-out.
var optOutEnvVars = []struct {
	name string
	// when trigger is "truthy", any truthy value opts out.
	// when trigger is "false0", only "0" / "false" / "no" / "off" opts out
	// (matches lowkey's LOWKEY_TELEMETRY=0 pattern).
	trigger string
}{
	{"DO_NOT_TRACK", "truthy"},
	{"CLAWTELLO_TELEMETRY", "false0"},
}

// markerFileRel is the relative path (under $HOME) of the opt-out marker.
const markerFileRel = ".clawtello/telemetry-off"

// IsDisabled reports whether telemetry is opted out.
// When disabled, source identifies the signal ("env:DO_NOT_TRACK",
// "env:CLAWTELLO_TELEMETRY", or "file:<path>") and detail is the raw
// value (for env vars) or path (for file).
func IsDisabled() (disabled bool, source, detail string) {
	return isDisabled(os.Getenv, fileExists, os.UserHomeDir)
}

// isDisabled is the testable form of IsDisabled. It takes injectable
// env/file/home lookups.
func isDisabled(
	getenv func(string) string,
	exists func(string) bool,
	userHome func() (string, error),
) (bool, string, string) {
	for _, v := range optOutEnvVars {
		raw := getenv(v.name)
		switch v.trigger {
		case "truthy":
			if isTruthy(raw) {
				return true, "env:" + v.name, raw
			}
		case "false0":
			if isFalsy(raw) {
				return true, "env:" + v.name, raw
			}
		}
	}
	if home, err := userHome(); err == nil && home != "" {
		path := filepath.Join(home, markerFileRel)
		if exists(path) {
			return true, "file:" + path, ""
		}
	}
	return false, "", ""
}

func isTruthy(raw string) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// isFalsy matches lowkey's LOWKEY_TELEMETRY=0 pattern. The variable is
// only an opt-out when it's explicitly set to a negative value; unset
// or any other value means telemetry remains enabled.
func isFalsy(raw string) bool {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return false
	}
	switch strings.ToLower(trimmed) {
	case "0", "false", "no", "off":
		return true
	}
	return false
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
