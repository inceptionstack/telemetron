// SPDX-License-Identifier: Apache-2.0

package openclaw

import (
	"encoding/json"
	"os"
	"os/user"
	"path/filepath"
	"strings"
)

// openclawConfig is the minimal structure of ~/.openclaw/openclaw.json.
type openclawConfig struct {
	Meta struct {
		LastTouchedVersion string `json:"lastTouchedVersion"`
	} `json:"meta"`
}

// DetectVersion reads the installed OpenClaw version from the config file.
// It scans home directories for the current user, SUDO_USER, and
// TELEMETRON_RUN_AS (matching the pattern used for tier file detection).
// Returns empty string on any failure.
func DetectVersion() string {
	for _, home := range candidateHomes() {
		v := readVersionFromHome(home)
		if v != "" {
			return v
		}
	}
	return ""
}

func readVersionFromHome(home string) string {
	path := filepath.Join(home, ".openclaw", "openclaw.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	var cfg openclawConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return ""
	}
	return strings.TrimSpace(cfg.Meta.LastTouchedVersion)
}

func candidateHomes() []string {
	var homes []string
	if h, err := os.UserHomeDir(); err == nil {
		homes = append(homes, h)
	}
	for _, envKey := range []string{"SUDO_USER", "TELEMETRON_RUN_AS"} {
		u := strings.TrimSpace(os.Getenv(envKey))
		if u != "" && u != "root" {
			if lu, err := user.Lookup(u); err == nil {
				homes = append(homes, lu.HomeDir)
			}
		}
	}
	return homes
}
