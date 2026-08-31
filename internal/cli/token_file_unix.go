//go:build !windows

package cli

import (
	"os"
	"syscall"
)

func openTokenFile(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK|syscall.O_NOFOLLOW, 0)
}

func tokenFilePermissionsValid(mode os.FileMode) bool {
	return mode.Perm()&0o077 == 0
}
