package main

import (
	"os"
	"strings"
	"testing"
)

func TestInstallerDocsBlessExplicitAutoSetup(t *testing.T) {
	plan, err := os.ReadFile("docs/telemetron-setup-plan.md")
	if err != nil {
		t.Fatal(err)
	}
	text := string(plan)
	if !strings.Contains(text, "may auto-run `telemetron setup` only when the operator") {
		t.Fatalf("setup plan must document explicit install.sh auto-setup, got %q", text)
	}

	changelog, err := os.ReadFile("CHANGELOG.md")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(changelog), "Re-running `setup` with unchanged config + token now short-circuits") {
		t.Fatal("CHANGELOG.md must summarize the v0.2.0 installer-fix batch")
	}
}
