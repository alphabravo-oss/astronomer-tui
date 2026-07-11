//revive:disable:exported package-comments
package session

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

type State struct {
	Context       string `json:"context"`
	Namespace     string `json:"namespace"`
	AllNamespaces bool   `json:"allNamespaces,omitempty"`
	LastView      string `json:"lastView"`
	LastFilter    string `json:"lastFilter"`
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
	if err := os.Chmod(filepath.Dir(s.path), 0o700); err != nil { // #nosec G302 -- this is a private directory permission, not a file permission.
		return err
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	return atomicPrivateWrite(s.path, data)
}

func atomicPrivateWrite(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".astrotui-state-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer func() { _ = os.Remove(name) }()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}
