package cli

import (
	"fmt"
	"os"
	"path/filepath"
)

func replaceUpgradeExecutable(staged, target string) (string, error) {
	backup, err := os.CreateTemp(filepath.Dir(target), ".confighub-previous-*.exe")
	if err != nil {
		return "", err
	}
	backup.Close()
	if err := os.Remove(backup.Name()); err != nil {
		return "", err
	}
	if err := os.Rename(target, backup.Name()); err != nil {
		return "", err
	}
	if err := os.Rename(staged, target); err != nil {
		if restoreErr := os.Rename(backup.Name(), target); restoreErr != nil {
			return "", fmt.Errorf("replacement and restore failed; previous executable remains at %s: %w", backup.Name(), restoreErr)
		}
		return "", err
	}
	if os.Remove(backup.Name()) == nil {
		return "", nil
	}
	return backup.Name(), nil
}
