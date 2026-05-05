// SPDX-License-Identifier: Apache-2.0

package openclaw

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"
	"time"
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
// Falls back to running `openclaw --version` if the config file has no meta block
// (common on fresh installs before the gateway first runs).
// Returns empty string on any failure.
func DetectVersion() string {
	for _, home := range candidateHomes() {
		v := readVersionFromHome(home)
		if v != "" {
			return v
		}
	}
	return detectVersionCLI()
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

// detectVersionCLI runs `openclaw --version` and parses the output.
// Expected format: "OpenClaw 2026.5.3-1 (commit)" → returns "2026.5.3-1".
// Checks well-known mise/npm install locations since telemetron runs as root
// but openclaw is a Node.js script installed under the user's mise prefix.
// The node binary must also be on PATH for the shebang (#!/usr/bin/env node)
// to resolve, so we prepend the node bin dir to PATH for each candidate.
func detectVersionCLI() string {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for _, home := range candidateHomes() {
		for _, nodeVer := range findNodeVersions(home) {
			binDir := filepath.Join(home, ".local", "share", "mise", "installs", "node", nodeVer, "bin")
			ocBin := filepath.Join(binDir, "openclaw")
			if _, err := os.Stat(ocBin); err != nil {
				continue
			}
			cmd := exec.CommandContext(ctx, ocBin, "--version")
			cmd.Env = append(os.Environ(), "PATH="+binDir+":"+os.Getenv("PATH"))
			out, err := cmd.Output()
			if err != nil {
				slog.Error("openclaw binary found but --version failed", "path", ocBin, "error", err)
				continue
			}
			v := parseVersionOutput(string(out))
			if v != "" {
				return v
			}
		}
	}
	return ""
}

// findNodeVersions returns installed node version dirs under mise.
func findNodeVersions(home string) []string {
	dir := filepath.Join(home, ".local", "share", "mise", "installs", "node")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var versions []string
	for _, e := range entries {
		if e.IsDir() {
			versions = append(versions, e.Name())
		}
	}
	return versions
}

// parseVersionOutput extracts the version from "OpenClaw 2026.5.3-1 (hash)".
func parseVersionOutput(s string) string {
	// Format: "OpenClaw <version> (<commit>)" or just "<version>"
	s = strings.TrimSpace(s)
	parts := strings.Fields(s)
	if len(parts) >= 2 && strings.EqualFold(parts[0], "OpenClaw") {
		return parts[1]
	}
	if len(parts) == 1 {
		return parts[0]
	}
	return ""
}
