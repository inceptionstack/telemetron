// SPDX-License-Identifier: Apache-2.0

package updater

import (
	"encoding/json"
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/inceptionstack/telemetron/internal/fsatomic"
)

// State persists update check state across restarts.
type State struct {
	LastCheck         time.Time `json:"last_check"`
	LastUpdate        time.Time `json:"last_update,omitempty"`
	CurrentVersion    string    `json:"current_version,omitempty"`
	PreviousVersion   string    `json:"previous_version,omitempty"`
	UpdatePending     bool      `json:"update_pending"`
	UpdateStarted     bool      `json:"update_started"`
	PendingVersion    string    `json:"pending_version,omitempty"`
	RolledBackVersion string    `json:"rolled_back_version,omitempty"`
}

// StateFile manages the on-disk update state with in-memory caching.
type StateFile struct {
	path   string
	mu     sync.Mutex
	state  State
	logger *slog.Logger
}

// NewStateFile creates a StateFile at the given path.
func NewStateFile(path string, logger *slog.Logger) *StateFile {
	return &StateFile{path: path, logger: logger}
}

// Load reads the state from disk into memory. Missing or corrupt files
// are silently treated as zero state.
func (sf *StateFile) Load() {
	data, err := os.ReadFile(sf.path)
	if err != nil {
		return
	}
	sf.mu.Lock()
	defer sf.mu.Unlock()
	_ = json.Unmarshal(data, &sf.state)
}

// Get returns a snapshot of the current in-memory state.
func (sf *StateFile) Get() State {
	sf.mu.Lock()
	defer sf.mu.Unlock()
	return sf.state
}

// Update applies a mutation to the in-memory state and persists it.
// On write failure the in-memory state is reverted to the pre-call value.
func (sf *StateFile) Update(fn func(s *State)) error {
	sf.mu.Lock()
	defer sf.mu.Unlock()
	prev := sf.state
	fn(&sf.state)
	if err := sf.writeLocked(); err != nil {
		sf.state = prev
		return err
	}
	return nil
}

// UpdateBestEffort applies a mutation and persists. On write failure
// the in-memory change is reverted and a warning is logged.
func (sf *StateFile) UpdateBestEffort(fn func(s *State)) {
	if err := sf.Update(fn); err != nil {
		sf.logger.Warn("state file write failed (best-effort)",
			slog.String("path", sf.path),
			slog.String("error", err.Error()))
	}
}

// ClearPending resets the pending-update flags. Best-effort: logs on error.
func (sf *StateFile) ClearPending(rolledBackVersion string) {
	sf.UpdateBestEffort(func(s *State) {
		s.UpdatePending = false
		s.UpdateStarted = false
		s.PendingVersion = ""
		s.RolledBackVersion = rolledBackVersion
	})
}

func (sf *StateFile) writeLocked() error {
	data, err := json.MarshalIndent(sf.state, "", "  ")
	if err != nil {
		return err
	}
	return fsatomic.WriteFile(sf.path, data)
}
