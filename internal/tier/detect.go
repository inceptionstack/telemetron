// SPDX-License-Identifier: Apache-2.0

// Package tier detects the AWS account tier (internal/external) and manages
// the tier file on disk.
package tier

import (
	"context"
	"log/slog"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"
	"time"
)

// DetectAndWrite checks if this is an internal (Amazon) AWS account and writes
// the tier file. Only upgrades the tier (external→internal), never downgrades.
// Called once on startup; best-effort, never fatal.
func DetectAndWrite(logger *slog.Logger) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	detected := detectTier(ctx)
	if detected == "" {
		logger.Debug("tier detection: no signal found, skipping")
		return
	}

	current := readCurrentTier()
	if current == detected {
		return
	}

	// Only upgrade: internal > external > test
	rank := map[string]int{"test": 0, "external": 1, "internal": 2}
	if rank[detected] <= rank[current] {
		logger.Debug("tier detection: not upgrading", "current", current, "detected", detected)
		return
	}

	logger.Info("tier detection: upgrading tier", "from", current, "to", detected)
	writeTierFiles(detected, logger)
}

// detectTier checks AWS APIs for internal account signals.
func detectTier(ctx context.Context) string {
	// Method 1: organizations describe-organization (works from member accounts)
	if email := orgMasterEmail(ctx); strings.HasSuffix(email, "@amazon.com") {
		return "internal"
	}

	// Method 2: account contact info — check all fields for @amazon.com
	if hasAmazonContact(ctx) {
		return "internal"
	}

	return ""
}

func orgMasterEmail(ctx context.Context) string {
	cmd := exec.CommandContext(ctx, "aws", "organizations", "describe-organization",
		"--query", "Organization.MasterAccountEmail", "--output", "text")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func hasAmazonContact(ctx context.Context) bool {
	cmd := exec.CommandContext(ctx, "aws", "account", "get-contact-information",
		"--output", "json")
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	// Check if any field value in the JSON ends with @amazon.com
	return strings.Contains(string(out), "@amazon.com\"")
}

// ReadCurrent reads the current tier from the tier file.
// Checks ~/.lowkey/tier and ~/.loki/tier for candidate homes.
// Returns "external" if no valid tier file is found.
func ReadCurrent() string {
	return readCurrentTier()
}

func readCurrentTier() string {
	for _, home := range candidateHomes() {
		for _, dir := range []string{".lowkey", ".loki"} {
			data, err := os.ReadFile(filepath.Join(home, dir, "tier"))
			if err != nil {
				continue
			}
			t := strings.TrimSpace(string(data))
			if t == "internal" || t == "external" || t == "test" {
				return t
			}
		}
	}
	return "external"
}

func writeTierFiles(tier string, logger *slog.Logger) {
	for _, home := range candidateHomes() {
		for _, dir := range []string{".lowkey", ".loki"} {
			dirPath := filepath.Join(home, dir)
			if err := os.MkdirAll(dirPath, 0o755); err != nil {
				continue
			}
			path := filepath.Join(dirPath, "tier")
			if err := os.WriteFile(path, []byte(tier+"\n"), 0o644); err != nil {
				logger.Warn("tier detection: failed to write tier file", "path", path, "error", err)
			} else {
				logger.Info("tier detection: wrote tier file", "path", path, "tier", tier)
			}
		}
	}
}

func candidateHomes() []string {
	seen := map[string]bool{}
	var homes []string
	add := func(h string) {
		if h != "" && !seen[h] {
			seen[h] = true
			homes = append(homes, h)
		}
	}
	if h, err := os.UserHomeDir(); err == nil {
		add(h)
	}
	for _, envKey := range []string{"SUDO_USER", "TELEMETRON_RUN_AS"} {
		u := strings.TrimSpace(os.Getenv(envKey))
		if u != "" && u != "root" {
			if lu, err := user.Lookup(u); err == nil {
				add(lu.HomeDir)
			}
		}
	}
	return homes
}
