package status

import (
	"encoding/json"
	"os"
	"time"

	"github.com/inceptionstack/loki-otl/internal/fsatomic"
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
	data, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		return err
	}
	return fsatomic.WriteFile(s.path, data)
}
