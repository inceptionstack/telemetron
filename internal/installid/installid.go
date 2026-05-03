// SPDX-License-Identifier: Apache-2.0

package installid

import (
	"crypto/rand"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"github.com/inceptionstack/telemetron/internal/fsatomic"
)

var uuidV4Re = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

var installIDPathLocks sync.Map

// ReadOrGenerate reads /etc/telemetron/install-id if it exists; otherwise
// generates a new UUIDv4, writes it to /etc/telemetron/install-id with 0644 perms
// (intentionally world-readable; see docs/privacy.md for rationale),
// and returns it. Directory /etc/telemetron is created 0755 if absent.
func ReadOrGenerate(path string) (string, error) {
	lockAny, _ := installIDPathLocks.LoadOrStore(path, &sync.Mutex{})
	lock := lockAny.(*sync.Mutex)
	lock.Lock()
	defer lock.Unlock()

	installID, err := Read(path)
	if err == nil {
		return installID, nil
	}
	if !os.IsNotExist(err) {
		return "", err
	}

	installID, err = generate()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", fmt.Errorf("create install-id dir: %w", err)
	}
	if err := fsatomic.WriteFile(path, []byte(installID), fsatomic.WithMode(0o644)); err != nil {
		return "", fmt.Errorf("write install-id: %w", err)
	}
	return installID, nil
}

// Read is the read-only form; returns os.ErrNotExist if the file is missing.
func Read(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	installID := strings.TrimSpace(string(data))
	if !Validate(installID) {
		return "", fmt.Errorf("invalid install-id contents in %s", path)
	}
	return installID, nil
}

// Validate verifies a string is a well-formed lowercase UUIDv4.
func Validate(s string) bool {
	return uuidV4Re.MatchString(s)
}

func generate() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("generate install-id: %w", err)
	}
	raw[6] = (raw[6] & 0x0f) | 0x40
	raw[8] = (raw[8] & 0x3f) | 0x80

	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		raw[0:4],
		raw[4:6],
		raw[6:8],
		raw[8:10],
		raw[10:16],
	), nil
}
