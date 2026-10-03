// Package filestate implements ports.StateStore as one small file per key.
package filestate

import (
	"os"
	"path/filepath"
	"strings"
)

type Store struct{ Dir string }

func (s Store) Get(key string) string {
	b, err := os.ReadFile(filepath.Join(s.Dir, key))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// Set stores value; an empty value deletes the key.
func (s Store) Set(key, value string) error {
	path := filepath.Join(s.Dir, key)
	if value == "" {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	if err := os.MkdirAll(s.Dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(value+"\n"), 0o644)
}
