// SPDX-License-Identifier: Apache-2.0

package machineid

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"runtime"
	"strings"
)

// Compute returns the canonical machine_id string for this host.
// Format: "sha256:" + 64 lowercase hex chars.
// Reads /etc/machine-id and os.Hostname(). On macOS, reads /var/db/dbus/machine-id
// or /etc/machine-id (whichever exists); if neither, returns an error.
func Compute() (string, error) {
	machineID, err := readMachineID()
	if err != nil {
		return "", err
	}
	hostname, err := os.Hostname()
	if err != nil {
		return "", fmt.Errorf("read hostname: %w", err)
	}
	return ComputeFrom(machineID, hostname), nil
}

// ComputeFrom is an exported deterministic form for testing and for matching
// behavior with lowkey's install.sh exactly.
func ComputeFrom(etcMachineID, hostname string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(etcMachineID) + ":" + hostname))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func readMachineID() (string, error) {
	paths := []string{"/etc/machine-id"}
	if runtime.GOOS == "darwin" {
		paths = []string{"/var/db/dbus/machine-id", "/etc/machine-id"}
	}
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err == nil {
			return strings.TrimSpace(string(data)), nil
		}
		if !os.IsNotExist(err) {
			return "", fmt.Errorf("read %s: %w", path, err)
		}
	}
	return "", fmt.Errorf("machine-id file not found in %v", paths)
}
