package openclaw

import (
	"encoding/json"
	"os"
	"path/filepath"
)

type FileState struct {
	Offset          int64  `json:"offset"`
	SessionStarted  bool   `json:"session_started"`
	SessionType     string `json:"session_type,omitempty"`
	LastModelFamily string `json:"last_model_family,omitempty"`
	LastTopRole     string `json:"last_top_role,omitempty"`
}

type State struct {
	Files map[string]FileState `json:"files"`
}

func LoadState(path string) (State, error) {
	state := State{Files: map[string]FileState{}}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return state, nil
		}
		return state, err
	}
	if err := json.Unmarshal(data, &state); err != nil {
		return state, err
	}
	if state.Files == nil {
		state.Files = map[string]FileState{}
	}
	return state, nil
}

func SaveState(path string, state State) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
