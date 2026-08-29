package database

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

func prepareDatabasePath(path string) error {
	return withSafeDatabaseDirectory(path, func(directoryFD int, name string) error {
		return ensurePrivateDatabaseFileAt(directoryFD, name)
	})
}

func hardenDatabaseFiles(path string) error {
	return withSafeDatabaseDirectory(path, func(directoryFD int, name string) error {
		for _, file := range []struct {
			name string
			kind string
		}{
			{name: name, kind: "database"},
			{name: name + "-wal", kind: "database WAL"},
			{name: name + "-shm", kind: "database SHM"},
		} {
			if err := hardenExistingDatabaseFileAt(directoryFD, file.name, file.kind); err != nil {
				return err
			}
		}
		return nil
	})
}

func withSafeDatabaseDirectory(path string, operation func(int, string) error) error {
	name := filepath.Base(path)
	if name == "" || name == "." || name == string(filepath.Separator) || filepath.Base(name) != name {
		return errors.New("database filename is invalid")
	}
	directoryFD, err := openSafeDatabaseDirectory(filepath.Dir(path))
	if err != nil {
		return err
	}
	operationErr := operation(directoryFD, name)
	closeErr := unix.Close(directoryFD)
	if operationErr != nil {
		return operationErr
	}
	if closeErr != nil {
		return errors.New("close database directory")
	}
	return nil
}

func openSafeDatabaseDirectory(path string) (int, error) {
	if err := os.MkdirAll(path, 0o700); err != nil {
		return -1, errors.New("create database directory")
	}
	expectedUID := uint32(os.Geteuid())
	var pathStat unix.Stat_t
	if err := unix.Lstat(path, &pathStat); err != nil {
		return -1, errors.New("inspect database directory")
	}
	if !isSafeDatabaseDirectoryStat(&pathStat, expectedUID) {
		return -1, errors.New("database directory must be private and owned by the current user")
	}

	directoryFD, err := unix.Open(path, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return -1, errors.New("open database directory without following links")
	}
	var openedStat unix.Stat_t
	if err := unix.Fstat(directoryFD, &openedStat); err != nil {
		_ = unix.Close(directoryFD)
		return -1, errors.New("inspect opened database directory")
	}
	if !isSafeDatabaseDirectoryStat(&openedStat, expectedUID) || !sameDatabaseFileIdentity(&pathStat, &openedStat) {
		_ = unix.Close(directoryFD)
		return -1, errors.New("database directory identity or permissions changed")
	}
	return directoryFD, nil
}

func isSafeDatabaseDirectoryStat(stat *unix.Stat_t, expectedUID uint32) bool {
	return stat != nil &&
		stat.Mode&unix.S_IFMT == unix.S_IFDIR &&
		stat.Uid == expectedUID &&
		stat.Mode&(unix.S_IRWXG|unix.S_IRWXO|unix.S_ISUID|unix.S_ISGID|unix.S_ISVTX) == 0
}

func ensurePrivateDatabaseFileAt(directoryFD int, name string) error {
	expectedUID := uint32(os.Geteuid())
	var before unix.Stat_t
	statErr := unix.Fstatat(directoryFD, name, &before, unix.AT_SYMLINK_NOFOLLOW)
	existed := statErr == nil
	if statErr != nil && !errors.Is(statErr, unix.ENOENT) {
		return errors.New("inspect database file")
	}
	if existed && !isOwnedRegularDatabaseFileStat(&before, expectedUID) {
		return errors.New("database file must be regular and owned by the current user")
	}

	flags := unix.O_RDWR | unix.O_NOFOLLOW | unix.O_CLOEXEC
	if !existed {
		flags |= unix.O_CREAT | unix.O_EXCL
	}
	fileFD, err := unix.Openat(directoryFD, name, flags, 0o600)
	if err != nil {
		return errors.New("open database file without following links")
	}
	var opened unix.Stat_t
	if err := unix.Fstat(fileFD, &opened); err != nil {
		_ = unix.Close(fileFD)
		return errors.New("inspect opened database file")
	}
	if !isOwnedRegularDatabaseFileStat(&opened, expectedUID) || existed && !sameDatabaseFileIdentity(&before, &opened) {
		_ = unix.Close(fileFD)
		return errors.New("database file identity, type, or owner changed")
	}
	if err := unix.Fchmod(fileFD, 0o600); err != nil {
		_ = unix.Close(fileFD)
		return errors.New("restrict database file permissions")
	}
	if err := unix.Close(fileFD); err != nil {
		return errors.New("close database file")
	}
	return nil
}

func hardenExistingDatabaseFileAt(directoryFD int, name, kind string) error {
	fileFD, err := unix.Openat(directoryFD, name, unix.O_RDWR|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if errors.Is(err, unix.ENOENT) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("open %s without following links", kind)
	}
	var stat unix.Stat_t
	if err := unix.Fstat(fileFD, &stat); err != nil {
		_ = unix.Close(fileFD)
		return fmt.Errorf("inspect %s", kind)
	}
	if !isOwnedRegularDatabaseFileStat(&stat, uint32(os.Geteuid())) {
		_ = unix.Close(fileFD)
		return fmt.Errorf("%s must be regular and owned by the current user", kind)
	}
	if err := unix.Fchmod(fileFD, 0o600); err != nil {
		_ = unix.Close(fileFD)
		return fmt.Errorf("restrict %s permissions", kind)
	}
	if err := unix.Close(fileFD); err != nil {
		return fmt.Errorf("close %s", kind)
	}
	return nil
}

func isOwnedRegularDatabaseFileStat(stat *unix.Stat_t, expectedUID uint32) bool {
	return stat != nil && stat.Mode&unix.S_IFMT == unix.S_IFREG && stat.Uid == expectedUID
}

func sameDatabaseFileIdentity(left, right *unix.Stat_t) bool {
	return left != nil && right != nil && left.Dev == right.Dev && left.Ino == right.Ino
}
