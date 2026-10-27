package supervise

import (
	"encoding/json"
	"os"
	"path/filepath"
)

type enabledStore struct {
	path      string
	overrides map[string]bool
}

func loadEnabledStore(path string) *enabledStore {
	store := &enabledStore{path: path, overrides: map[string]bool{}}
	if data, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(data, &store.overrides)
	}

	return store
}

func (e *enabledStore) resolve(name string, fallback bool) bool {
	if enabled, ok := e.overrides[name]; ok {
		return enabled
	}

	return fallback
}

func (e *enabledStore) set(name string, enabled bool) error {
	e.overrides[name] = enabled
	data, err := json.MarshalIndent(e.overrides, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(e.path), 0o755); err != nil {
		return err
	}
	staging := e.path + ".tmp"
	if err := os.WriteFile(staging, data, 0o644); err != nil {
		return err
	}

	return os.Rename(staging, e.path)
}
