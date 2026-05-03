// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
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
	DefaultEnrollEndpoint = "https://telemetry.loki.run/v1/enroll"
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

func explicitTokenSourceConfigured(r resolvedSetup) bool {
	return r.tokenFile != "" || r.tokenFromEnv != "" || strings.TrimSpace(os.Getenv("TELEMETRON_TOKEN_SECRET")) != ""
}

func autoEnrollDisabled() bool {
	return strings.TrimSpace(os.Getenv("TELEMETRON_NO_AUTO_ENROLL")) == "1"
}

func shouldAttemptAutoEnroll(r resolvedSetup) bool {
	return !explicitTokenSourceConfigured(r) && !existingTokenFilePresent()
}

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
	if existingTokenFilePresent() {
		data, err := os.ReadFile(cfg.TokenFile)
		if err != nil {
			return "", "", err
		}
		return strings.TrimSpace(string(data)), "existing", nil
	}
	if autoEnrollDisabled() {
		return "", "", errors.New("auto-enroll disabled")
	}

	installID, err := readOrGenerateInstallID(setupInstallIDPath)
	if err != nil {
		return "", "", fmt.Errorf("prepare install-id: %w", err)
	}
	machineIDValue, err := computeMachineID()
	if err != nil {
		return "", "", fmt.Errorf("compute machine_id: %w", err)
	}

	client := newEnrollClient(firstNonEmpty(strings.TrimSpace(os.Getenv("TELEMETRON_ENROLL_ENDPOINT")), DefaultEnrollEndpoint), nil)
	resp, err := client.Enroll(ctx, enroll.EnrollRequest{
		InstallID:         installID,
		MachineID:         machineIDValue,
		OS:                runtime.GOOS,
		Arch:              runtime.GOARCH,
		Source:            "telemetron-standalone",
		TelemetronVersion: version,
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
