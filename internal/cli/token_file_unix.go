//go:build !windows

package cli

import (
	"os"
	"syscall"
)

func openRestrictedFile(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK|syscall.O_NOFOLLOW, 0)
}

func openTokenFile(path string) (*os.File, error) { return openRestrictedFile(path) }

func tokenFilePermissionsValid(mode os.FileMode) bool {
	return mode.Perm()&0o077 == 0
}
