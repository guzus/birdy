package state

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// State tracks runtime rotation state (persisted between invocations).
type State struct {
	path         string
	LastUsedName string `json:"last_used_name"`
}

func defaultPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot determine home directory: %w", err)
	}
	return filepath.Join(home, ".config", "birdy", "state.json"), nil
}

// Load reads the state file, or returns empty state if it doesn't exist.
func Load() (*State, error) {
	p, err := defaultPath()
	if err != nil {
		return nil, err
	}
	return LoadPath(p)
}

// LoadPath reads the state file from a custom path.
func LoadPath(path string) (*State, error) {
	s := &State{path: path}

	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return s, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading state: %w", err)
	}

	if err := json.Unmarshal(data, s); err != nil {
		return nil, fmt.Errorf("parsing state: %w", err)
	}
	return s, nil
}

// Save persists state to disk.
func (s *State) Save() error {
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("creating config dir: %w", err)
	}

	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling state: %w", err)
	}

	return os.WriteFile(s.path, data, 0600)
}
