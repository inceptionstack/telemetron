// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/user"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/inceptionstack/telemetron/internal/config"
	"github.com/inceptionstack/telemetron/internal/enroll"
	"github.com/inceptionstack/telemetron/internal/installid"
	"github.com/inceptionstack/telemetron/internal/machineid"
)

const (
	// DefaultEnrollEndpoint is the production endpoint for anonymous enrollment.
	// Override via TELEMETRON_ENROLL_ENDPOINT for testing.
	DefaultEnrollEndpoint = "https://cfw713s6qf.execute-api.us-east-1.amazonaws.com/v1/enroll"
)

var (
	newEnrollClient = func(endpoint string, httpClient *http.Client) *enroll.Client {
		return enroll.NewClient(endpoint, httpClient)
	}
	readInstallID           = installid.Read
	readOrGenerateInstallID = installid.ReadOrGenerate
	computeMachineID        = machineid.Compute
	setupInstallIDPath      = "/etc/telemetron/install-id"
	setupTokenPath          = "/etc/telemetron/token"
)

// installIDPathForInstance returns the install-id file path for an instance.
// Uses config.InstancePaths for platform-aware resolution.
// setupInstallIDPath override is respected for primary (testing hook).
func installIDPathForInstance(instance string) string {
	if instance == "" && setupInstallIDPath != "/etc/telemetron/install-id" {
		return setupInstallIDPath
	}
	paths := config.InstancePaths(runtime.GOOS, instance)
	return paths.InstallIDFile
}

func explicitTokenSourceConfigured(r resolvedSetup) bool {
	return r.tokenFile != "" || r.tokenFromEnv != "" || tokenSecretIDSet()
}

// tokenSecretIDSet reports whether TELEMETRON_TOKEN_SECRET is non-empty.
// The actual secret resolution happens upstream in install.sh, which
// fetches from AWS Secrets Manager and writes /etc/telemetron/token
// before invoking `telemetron setup --token-file /etc/telemetron/token`.
// For the standalone `telemetron setup` path (no install.sh), presence
// of TELEMETRON_TOKEN_SECRET without --token-file or an existing token
// file is an operator error we must hard-fail on — NOT silently
// fall through to anonymous auto-enroll. See review blocker #2
// (2026-05-03).
func tokenSecretIDSet() bool {
	return strings.TrimSpace(os.Getenv("TELEMETRON_TOKEN_SECRET")) != ""
}

func autoEnrollDisabled() bool {
	return strings.TrimSpace(os.Getenv("TELEMETRON_NO_AUTO_ENROLL")) == "1"
}

func shouldAttemptAutoEnroll(r resolvedSetup) bool {
	return !explicitTokenSourceConfigured(r) && !existingTokenFilePresent(r.instance)
}

// ErrTokenSecretNotResolved is returned when TELEMETRON_TOKEN_SECRET is
// set but no token has been staged for setup to consume. Auto-enroll is
// deliberately not attempted in this case — the operator asked for a
// managed token and must get that path or a clean failure, never a
// silent swap to anonymous enrollment.
var ErrTokenSecretNotResolved = errors.New(
	"TELEMETRON_TOKEN_SECRET is set but no token was staged; " +
		"run via install.sh (which fetches the secret) or pass --token-file explicitly",
)

func loadTokenOrEnroll(ctx context.Context, r resolvedSetup, cfg config.Config) (string, string, error) {
	if r.tokenFile != "" {
		data, err := os.ReadFile(r.tokenFile)
		if err != nil {
			return "", "", err
		}
		return strings.TrimSpace(string(data)), "token-file", nil
	}
	if r.tokenFromEnv != "" {
		return strings.TrimSpace(r.tokenFromEnv), "env", nil
	}
	if existingTokenFilePresent(r.instance) {
		// Read from the instance-aware token path.
		// setupTokenPath hook: when overridden for testing, use it for primary.
		var tokenPath string
		if r.instance == "" && setupTokenPath != "/etc/telemetron/token" {
			tokenPath = setupTokenPath
		} else {
			paths := config.InstancePaths(runtime.GOOS, r.instance)
			tokenPath = paths.TokenFile
		}
		data, err := os.ReadFile(tokenPath)
		if err != nil {
			return "", "", err
		}
		return strings.TrimSpace(string(data)), "existing", nil
	}
	// TELEMETRON_TOKEN_SECRET is set but nothing was staged for us.
	// Hard-fail rather than auto-enroll (review blocker #2, 2026-05-03).
	if tokenSecretIDSet() {
		return "", "", ErrTokenSecretNotResolved
	}
	if autoEnrollDisabled() {
		return "", "", errors.New("auto-enroll disabled")
	}

	installIDPath := installIDPathForInstance(r.instance)
	installID, err := readOrGenerateInstallID(installIDPath)
	if err != nil {
		return "", "", fmt.Errorf("prepare install-id: %w", err)
	}
	machineIDValue, err := computeMachineID()
	if err != nil {
		return "", "", fmt.Errorf("compute machine_id: %w", err)
	}

	client := newEnrollClient(firstNonEmpty(strings.TrimSpace(os.Getenv("TELEMETRON_ENROLL_ENDPOINT")), DefaultEnrollEndpoint), nil)
	info := readInstallerInfo()
	resp, err := client.Enroll(ctx, enroll.EnrollRequest{
		InstallID:         installID,
		MachineID:         machineIDValue,
		OS:                runtime.GOOS,
		Arch:              runtime.GOARCH,
		Source:            "telemetron-standalone",
		TelemetronVersion: version,
		Pack:              cfg.Mode,
		Tier:              firstNonEmpty(cfg.Declared.Tier, info.Tier),
	})
	if err != nil {
		if errors.Is(err, enroll.ErrConflict) {
			return "", "", fmt.Errorf("this install ID is already enrolled to a different machine. To re-enroll, an operator must revoke the existing token. See docs/privacy.md: %w", err)
		}
		return "", "", fmt.Errorf("auto-enrollment failed: %w. Set TELEMETRON_NO_AUTO_ENROLL=1 to skip", err)
	}
	if resp.InstallID != installID {
		return "", "", fmt.Errorf("auto-enroll returned mismatched install_id %q", resp.InstallID)
	}
	return resp.Token, "auto-enroll", nil
}

// installerInfo holds metadata written by the lowkey installer.
type installerInfo struct {
	Tier string
}

// readInstallerInfo reads tier from the lowkey installer's tier file.
// Checks ~/.lowkey/tier and ~/.loki/tier (plain text, one line).
// Returns zero-value struct on any failure.
func readInstallerInfo() installerInfo {
	// Check multiple home directories: current user, SUDO_USER, and
	// TELEMETRON_RUN_AS (the service user — when running in UserData as root,
	// SUDO_USER is unset but the tier file lives under the run_as user's home).
	var homes []string
	if h, err := os.UserHomeDir(); err == nil {
		homes = append(homes, h)
	}
	for _, envKey := range []string{"SUDO_USER", "TELEMETRON_RUN_AS"} {
		if u := strings.TrimSpace(os.Getenv(envKey)); u != "" && u != "root" {
			if lu, err := user.Lookup(u); err == nil {
				homes = append(homes, lu.HomeDir)
			}
		}
	}
	for _, home := range homes {
		for _, dir := range []string{".lowkey", ".loki"} {
			path := filepath.Join(home, dir, "tier")
			data, err := os.ReadFile(path)
			if err != nil {
				continue
			}
			tier := strings.TrimSpace(string(data))
			if tier == "internal" || tier == "external" || tier == "test" {
				return installerInfo{Tier: tier}
			}
		}
	}
	return installerInfo{}
}
