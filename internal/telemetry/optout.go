// SPDX-License-Identifier: Apache-2.0

// Package telemetry implements the user-facing opt-out contract for clawtello.
//
// clawtello respects the https://consoledonottrack.com convention
// (DO_NOT_TRACK) and a tool-specific override (CLAWTELLO_DISABLE).
// When either is set to a truthy value the exporter must not run: no
// config load, no token read, no network sockets.
package telemetry

import (
	"os"
	"strings"
)

// optOutVars are the environment variables that disable clawtello.
// The order is deliberate: DO_NOT_TRACK is the shared community standard
// and is checked first so it wins on conflict.
var optOutVars = []string{
	"DO_NOT_TRACK",
	"CLAWTELLO_DISABLE",
}

// IsDisabled reports whether telemetry is opted out via an env var.
// When disabled, the returned name is the env var that triggered the opt-out
// and value is the raw value for diagnostic logging. When enabled, name and
// value are empty.
func IsDisabled() (disabled bool, name, value string) {
	return isDisabled(os.Getenv)
}

// isDisabled is the testable form of IsDisabled.
func isDisabled(lookup func(string) string) (bool, string, string) {
	for _, v := range optOutVars {
		raw := lookup(v)
		if isTruthy(raw) {
			return true, v, raw
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
