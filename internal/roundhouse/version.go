// SPDX-License-Identifier: Apache-2.0

package roundhouse

import (
	"context"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"
	"time"
)

// DetectVersion returns the installed roundhouse version.
// Scans candidate home dirs for mise/npm installs (matching openclaw's pattern),
// then falls back to `roundhouse --version` on PATH.
func DetectVersion() string {
	for _, home := range candidateHomes() {
		if v := versionFromMise(home); v != "" {
			return v
		}
	}
	if v := versionFromCLI(); v != "" {
		return v
	}
	return versionFromGlobalNPM()
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
		if u := strings.TrimSpace(os.Getenv(envKey)); u != "" && u != "root" {
			if lu, err := user.Lookup(u); err == nil {
				add(lu.HomeDir)
			}
		}
	}
	return homes
}

// versionFromMise checks mise node installs for the roundhouse package.
func versionFromMise(home string) string {
	nodeDir := filepath.Join(home, ".local", "share", "mise", "installs", "node")
	entries, err := os.ReadDir(nodeDir)
	if err != nil {
		return ""
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		pkgJSON := filepath.Join(nodeDir, e.Name(), "lib", "node_modules",
			"@inceptionstack", "roundhouse", "package.json")
		if v := readVersionFromPackageJSON(pkgJSON); v != "" {
			return v
		}
	}
	return ""
}

func versionFromCLI() string {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "roundhouse", "--version").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func versionFromGlobalNPM() string {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "npm", "root", "-g").Output()
	if err != nil {
		return ""
	}
	root := strings.TrimSpace(string(out))
	pkgJSON := filepath.Join(root, "@inceptionstack", "roundhouse", "package.json")
	return readVersionFromPackageJSON(pkgJSON)
}

func readVersionFromPackageJSON(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	// Simple extraction — avoid full JSON parse for a single field
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, `"version"`) {
			parts := strings.SplitN(line, ":", 2)
			if len(parts) == 2 {
				v := strings.Trim(strings.TrimSpace(parts[1]), `",`)
				return v
			}
		}
	}
	return ""
}
