//go:build windows

package cli

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

func openRestrictedFile(path string) (*os.File, error) {
	absolutePath, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	volume := filepath.VolumeName(absolutePath)
	if len(volume) != 2 || volume[1] != ':' || strings.Contains(absolutePath[len(volume):], ":") {
		return nil, errors.New("file must be on a local Windows volume")
	}
	pathPointer, err := windows.UTF16PtrFromString(absolutePath)
	if err != nil {
		return nil, err
	}
	handle, err := windows.CreateFile(
		pathPointer,
		windows.GENERIC_READ,
		windows.FILE_SHARE_READ,
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_FLAG_OPEN_REPARSE_POINT|windows.FILE_FLAG_SEQUENTIAL_SCAN,
		0,
	)
	if err != nil {
		return nil, err
	}
	var information windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &information); err != nil {
		_ = windows.CloseHandle(handle)
		return nil, err
	}
	if information.FileAttributes&(windows.FILE_ATTRIBUTE_REPARSE_POINT|windows.FILE_ATTRIBUTE_DIRECTORY) != 0 {
		_ = windows.CloseHandle(handle)
		return nil, errors.New("file must be a regular non-reparse file")
	}
	file := os.NewFile(uintptr(handle), absolutePath)
	if file == nil {
		_ = windows.CloseHandle(handle)
		return nil, errors.New("could not create token file handle")
	}
	return file, nil
}

func openTokenFile(path string) (*os.File, error) { return openRestrictedFile(path) }

func tokenFilePermissionsValid(os.FileMode) bool {
	return true
}
