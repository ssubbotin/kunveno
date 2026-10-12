// Package roomstore persists rooms created at runtime as a JSON file.
package roomstore

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/ssubbotin/kunveno/internal/room"
)

// Load returns the saved rooms; a missing file means none.
func Load(path string) ([]room.Spec, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var specs []room.Spec
	if err := json.Unmarshal(data, &specs); err != nil {
		return nil, err
	}
	return specs, nil
}

// Save replaces the file atomically. Mode 0600: the file holds PINs.
func Save(path string, specs []room.Spec) error {
	if specs == nil {
		specs = []room.Spec{}
	}
	data, err := json.MarshalIndent(specs, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".rooms-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
