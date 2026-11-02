package httpdebug

import (
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Old versions saved packets and TLS secrets beside the HTTP request store.
// Remove only those journals and their warning file, without reading their
// contents, following directory symlinks or touching retained HTTP records.
func removeRetiredWireFiles(directory string) error {
	directory = filepath.Join(directory, "wire")
	info, err := os.Lstat(directory)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return nil
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		return err
	}
	retained := false
	for _, entry := range entries {
		name := entry.Name()
		id := strings.TrimSuffix(name, ".wire")
		_, idErr := hex.DecodeString(id)
		owned := name == "warning.txt" || strings.HasSuffix(name, ".wire") && len(id) == 64 && idErr == nil
		if entry.IsDir() || !owned {
			retained = true
			continue
		}
		if err := os.Remove(filepath.Join(directory, name)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove retired capture data: %w", err)
		}
	}
	if !retained {
		if err := os.Remove(directory); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove retired capture directory: %w", err)
		}
	}
	return nil
}
