//revive:disable:exported package-comments
package session

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

type State struct {
	Context    string `json:"context"`
	Namespace  string `json:"namespace"`
	LastView   string `json:"lastView"`
	LastFilter string `json:"lastFilter"`
}

type Store struct {
	path string
}

func DefaultPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".config", "astrotui", "state.json")
	}
	return filepath.Join(home, ".config", "astrotui", "state.json")
}

func NewStore(path string) *Store {
	return &Store{path: path}
}

func (s *Store) Load() (State, error) {
	var state State
	data, err := os.ReadFile(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return state, nil
		}
		return state, err
	}
	return state, json.Unmarshal(data, &state)
}

func (s *Store) Save(state State) error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(s.path), ".state-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(file.Name()) }()
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), s.path)
}
