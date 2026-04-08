package state

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// State tracks runtime rotation state (persisted between invocations).
type State struct {
	path         string
	LastUsedName string `json:"last_used_name"`
	Model        string `json:"model,omitempty"`
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
		if quarantineErr := quarantineCorruptStateFile(path); quarantineErr != nil {
			return nil, fmt.Errorf("parsing state: %w", err)
		}
		return s, nil
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

	tmp, err := os.CreateTemp(dir, filepath.Base(s.path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("creating temp state file: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)

	if err := tmp.Chmod(0600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("setting temp state permissions: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("writing temp state file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("closing temp state file: %w", err)
	}
	if err := os.Rename(tmpPath, s.path); err != nil {
		return fmt.Errorf("replacing state file: %w", err)
	}
	return nil
}

func quarantineCorruptStateFile(path string) error {
	dir := filepath.Dir(path)
	base := filepath.Base(path)
	stamp := time.Now().UTC().Format("20060102T150405Z")
	dst := filepath.Join(dir, fmt.Sprintf("%s.corrupt-%s", base, stamp))
	for i := 0; i < 1000; i++ {
		target := dst
		if i > 0 {
			target = fmt.Sprintf("%s-%02d", dst, i)
		}
		if err := os.Rename(path, target); err == nil {
			return nil
		} else if os.IsExist(err) {
			continue
		} else {
			return err
		}
	}
	return fmt.Errorf("exhausted corrupt state quarantine attempts for %s", path)
}
