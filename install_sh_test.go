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
}
