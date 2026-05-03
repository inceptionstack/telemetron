// SPDX-License-Identifier: Apache-2.0

// Package telemetry implements the user-facing opt-out contract for clawtello.
//
// clawtello respects its own signals AND the lowkey installer family
// signals, so a single opt-out disables both when clawtello is deployed
// alongside lowkey:
//
//  Shared:
//    DO_NOT_TRACK=1                    (https://consoledonottrack.com)
//
//  clawtello-specific:
//    CLAWTELLO_TELEMETRY=0
//    $HOME/.clawtello/telemetry-off
//
//  Lowkey-family (inherited when deployed via lowkey):
//    LOWKEY_TELEMETRY=0
//    $HOME/.lowkey/telemetry-off
//
// When any signal is present the exporter must not run: no config load,
// no token read, no network sockets.
//
// For systemd deployments, the marker files are checked against the
// *service* user's home (the user that clawtello runs as), since env
// vars set by the lowkey installer in the interactive shell do not
// propagate into systemd units by default. The lowkey installer is
// expected to drop the marker file under the clawtello user's home if
// the operator opted out at install time.
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
	{"LOWKEY_TELEMETRY", "false0"},
}

// markerFilesRel lists relative paths (under $HOME) of opt-out markers
// honored by clawtello. Checked in order; first hit wins.
var markerFilesRel = []string{
	".clawtello/telemetry-off",
	".lowkey/telemetry-off",
}

// IsDisabled reports whether telemetry is opted out.
// When disabled, source identifies the signal ("env:<VAR>" or
// "file:<path>") and detail is the raw value (for env vars) or empty
// (for files).
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
		for _, rel := range markerFilesRel {
			path := filepath.Join(home, rel)
			if exists(path) {
				return true, "file:" + path, ""
			}
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
