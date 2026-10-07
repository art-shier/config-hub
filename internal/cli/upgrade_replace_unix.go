//go:build !windows

package cli

import "os"

func replaceUpgradeExecutable(staged, target string) (string, error) {
	return "", os.Rename(staged, target)
}
