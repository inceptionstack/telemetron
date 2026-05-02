package openclaw

import (
	"encoding/json"
	"os"

	"github.com/inceptionstack/loki-otl/internal/fsatomic"
)

type FileState struct {
	Offset          int64  `json:"offset"`
	SessionStarted  bool   `json:"session_started"`
	SessionType     string `json:"session_type,omitempty"`
	LastModelFamily string `json:"last_model_family,omitempty"`
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
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	return fsatomic.WriteFile(path, data)
}
