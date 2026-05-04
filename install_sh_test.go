package main

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestInstallScriptGuardsUnsetHomeAndAvoidsSetU(t *testing.T) {
	data, err := os.ReadFile("install.sh")
	if err != nil {
		t.Fatal(err)
	}
	script := string(data)

	if strings.Contains(script, "set -eu") {
		t.Fatal("install.sh must not use set -u")
	}
	if !strings.Contains(script, "set -e") {
		t.Fatal("install.sh must retain set -e")
	}
	if !strings.Contains(script, "${HOME:-}") {
		t.Fatal("install.sh must guard HOME with a default")
	}

	cmd := exec.Command("sh", "-n", "install.sh")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("install.sh syntax check failed: %v\n%s", err, out)
	}

	helpCmd := exec.Command("sh", "install.sh", "--help")
	out, err := helpCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("install.sh --help failed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "TELEMETRON_TOKEN_SECRET") {
		t.Fatalf("install.sh --help must document TELEMETRON_TOKEN_SECRET, got %q", out)
	}
	if !strings.Contains(strings.ToLower(string(out)), "anonymous auto-enroll") {
		t.Fatalf("install.sh --help must document anonymous auto-enroll, got %q", out)
	}
}

// TestInstallScriptAnonymousEnrollBranch verifies that the installer
// recognizes the zero-token-sources branch added in v0.3.0. Without
// this branch, the classic one-liner
//
//	curl ... | TELEMETRON_ENDPOINT=... sudo -E sh
//
// would exit non-zero with 'no token source was provided', forcing
// users to pre-provision credentials for a one-line install.
//
// We don't actually run the installer (it would try to fetch a GitHub
// release) — we inspect the script for the anonymous_enroll guard
// and the zero-source short-circuit that delegates to `telemetron
// setup` without staging a token.
func TestInstallScriptAnonymousEnrollBranch(t *testing.T) {
	data, err := os.ReadFile("install.sh")
	if err != nil {
		t.Fatal(err)
	}
	script := string(data)

	if !strings.Contains(script, "anonymous_enroll=1") {
		t.Fatal("install.sh must set anonymous_enroll=1 when no token source is configured")
	}
	if !strings.Contains(script, "anonymous auto-enroll short-circuit") {
		t.Fatal("install.sh must contain the anonymous auto-enroll short-circuit block")
	}
	// The short-circuit must NOT pass --token-file; the binary writes
	// the enrolled token itself. If this regresses to including
	// --token-file, setup will fail with 'token_read_failed' because
	// no token has been staged on the clean host.
	idx := strings.Index(script, "anonymous auto-enroll short-circuit")
	if idx < 0 {
		t.Fatal("could not find anonymous-enroll block")
	}
	block := script[idx:]
	if end := strings.Index(block, "-------- resolve the token value"); end >= 0 {
		block = block[:end]
	}
	if strings.Contains(block, "--token-file") {
		t.Fatalf("anonymous-enroll branch must not pass --token-file; got block:\n%s", block)
	}
	if !strings.Contains(block, "--endpoint \"$SETUP_ENDPOINT\"") {
		t.Fatalf("anonymous-enroll branch must pass --endpoint; got block:\n%s", block)
	}
}
