package status

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

type Snapshot struct {
	LastFlushAt     time.Time `json:"last_flush_at"`
	LastFlushMetric int       `json:"last_flush_metric"`
	LastFlushBytes  int       `json:"last_flush_bytes"`
	LastHTTPStatus  int       `json:"last_http_status"`
	LastHeartbeatAt time.Time `json:"last_heartbeat_at"`
	DroppedBatches  int       `json:"dropped_batches"`
}

type Store struct {
	path string
}

func New(path string) *Store {
	return &Store{path: path}
}

func (s *Store) Path() string {
	return s.path
}

func (s *Store) Read() (Snapshot, error) {
	var snap Snapshot
	data, err := os.ReadFile(s.path)
	if err != nil {
		return snap, err
	}
	err = json.Unmarshal(data, &snap)
	return snap, err
}

func (s *Store) Write(snap Snapshot) error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	data, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}
