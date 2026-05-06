// SPDX-License-Identifier: Apache-2.0

package agentdetect

import (
	"fmt"
	"os"
	"path/filepath"
)

// DetectRoundhouse looks for roundhouse on disk.
// Checks for ~/.roundhouse/sessions/main/ (active sessions) or
// ~/.roundhouse/ directory (installed, awaiting first session).
// Returns Detection with Mode="roundhouse" if found, empty Detection otherwise.
func DetectRoundhouse(opts Options) (Detection, error) {
	username, err := resolveUser(opts.User)
	if err != nil {
		return Detection{}, err
	}

	home := opts.HomeDirOverride
	if home == "" {
		home, err = lookupHomeDir(username)
		if err != nil {
			return Detection{}, err
		}
	}
	if home == "" {
		return Detection{}, nil
	}

	roundhouseDir := filepath.Join(opts.FSRoot, home, ".roundhouse")
	sessionsDir := filepath.Join(roundhouseDir, "sessions", "main")

	// Prefer sessions dir if it exists (active)
	if isDir(sessionsDir) {
		return Detection{
			Mode:       "roundhouse",
			SessionDir: sessionsDir,
			RunAsUser:  username,
			AgentName:  "main",
		}, nil
	}

	// Fall back: if ~/.roundhouse/ exists (installed but no sessions yet),
	// return the expected sessions path. The collector handles non-existent
	// session dirs gracefully via empty scans.
	if isDir(roundhouseDir) {
		return Detection{
			Mode:       "roundhouse",
			SessionDir: sessionsDir,
			RunAsUser:  username,
			AgentName:  "main",
		}, nil
	}

	return Detection{}, nil
}

// DetectAll runs all known detectors and returns all successful detections.
// Empty detections (Mode="") are excluded unless Ambiguous is populated
// (in which case the caller should handle disambiguation).
// Detector errors are collected in the returned errors slice.
func DetectAll(opts Options) ([]Detection, []error) {
	var results []Detection
	var errs []error

	if d, err := DetectOpenClaw(opts); err != nil {
		errs = append(errs, fmt.Errorf("openclaw: %w", err))
	} else if d.Mode != "" || len(d.Ambiguous) > 0 {
		results = append(results, d)
	}

	if d, err := DetectRoundhouse(opts); err != nil {
		errs = append(errs, fmt.Errorf("roundhouse: %w", err))
	} else if d.Mode != "" {
		results = append(results, d)
	}

	return results, errs
}

func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}
