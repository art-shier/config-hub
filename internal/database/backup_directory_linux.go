package database

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

type backupDirectory struct {
	fd   int
	path string
	stat unix.Stat_t
}

type directoryWalkOps struct {
	mkdirat func(int, string, uint32) error
	fsync   func(int) error
}

func (ops directoryWalkOps) withDefaults() directoryWalkOps {
	if ops.mkdirat == nil {
		ops.mkdirat = unix.Mkdirat
	}
	if ops.fsync == nil {
		ops.fsync = unix.Fsync
	}
	return ops
}

func openBackupDirectory(path string, create bool) (*backupDirectory, error) {
	return openBackupDirectoryWithOps(path, create, directoryWalkOps{})
}

func openBackupDirectoryWithOps(path string, create bool, ops directoryWalkOps) (*backupDirectory, error) {
	fd, stat, err := walkDirectoryPathWithOps(path, create, ops)
	if err != nil {
		return nil, errors.New("open backup directory")
	}
	if !isSafeBackupDirectoryStat(&stat) {
		_ = unix.Close(fd)
		return nil, errors.New("backup directory permissions or owner are unsafe")
	}
	return &backupDirectory{fd: fd, path: filepath.Clean(path), stat: stat}, nil
}

func walkDirectoryPath(path string, create bool) (int, unix.Stat_t, error) {
	return walkDirectoryPathWithOps(path, create, directoryWalkOps{})
}

func walkDirectoryPathWithOps(path string, create bool, ops directoryWalkOps) (int, unix.Stat_t, error) {
	var zero unix.Stat_t
	ops = ops.withDefaults()
	cleanPath := filepath.Clean(path)
	if !filepath.IsAbs(cleanPath) {
		return -1, zero, unix.EINVAL
	}
	currentFD, err := unix.Open(string(filepath.Separator), unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return -1, zero, err
	}
	components := strings.Split(strings.TrimPrefix(cleanPath, string(filepath.Separator)), string(filepath.Separator))
	for _, component := range components {
		if component == "" {
			continue
		}
		nextFD, openErr := unix.Openat(currentFD, component, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		if errors.Is(openErr, unix.ENOENT) && create {
			mkdirErr := ops.mkdirat(currentFD, component, 0o700)
			if mkdirErr != nil && !errors.Is(mkdirErr, unix.EEXIST) {
				_ = unix.Close(currentFD)
				return -1, zero, mkdirErr
			}
			if err := ops.fsync(currentFD); err != nil {
				_ = unix.Close(currentFD)
				return -1, zero, err
			}
			nextFD, openErr = unix.Openat(currentFD, component, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		}
		if openErr != nil {
			_ = unix.Close(currentFD)
			return -1, zero, openErr
		}
		_ = unix.Close(currentFD)
		currentFD = nextFD
	}
	var stat unix.Stat_t
	if err := unix.Fstat(currentFD, &stat); err != nil {
		_ = unix.Close(currentFD)
		return -1, zero, err
	}
	return currentFD, stat, nil
}

func isSafeBackupDirectoryStat(stat *unix.Stat_t) bool {
	return stat != nil &&
		stat.Mode&unix.S_IFMT == unix.S_IFDIR &&
		stat.Uid == uint32(os.Geteuid()) &&
		stat.Mode&(unix.S_IRWXG|unix.S_IRWXO|unix.S_ISUID|unix.S_ISGID|unix.S_ISVTX) == 0
}

func (directory *backupDirectory) close() {
	if directory != nil && directory.fd >= 0 {
		_ = unix.Close(directory.fd)
		directory.fd = -1
	}
}

func (directory *backupDirectory) matchesRequestedPath() bool {
	var retained unix.Stat_t
	if err := unix.Fstat(directory.fd, &retained); err != nil || !isSafeBackupDirectoryStat(&retained) || !sameDirectoryIdentity(&retained, &directory.stat) {
		return false
	}
	pathFD, pathStat, err := walkDirectoryPath(directory.path, false)
	if err != nil {
		return false
	}
	_ = unix.Close(pathFD)
	return isSafeBackupDirectoryStat(&pathStat) && sameDirectoryIdentity(&retained, &pathStat)
}

func sameDirectoryIdentity(left, right *unix.Stat_t) bool {
	return left != nil && right != nil && left.Dev == right.Dev && left.Ino == right.Ino
}

func (directory *backupDirectory) ensureNameAbsent(name string) error {
	if !isBackupBasename(name) {
		return unix.EINVAL
	}
	var stat unix.Stat_t
	err := unix.Fstatat(directory.fd, name, &stat, unix.AT_SYMLINK_NOFOLLOW)
	if err == nil {
		return os.ErrExist
	}
	if errors.Is(err, unix.ENOENT) {
		return nil
	}
	return err
}

func (directory *backupDirectory) createTemporary() (string, string, error) {
	for attempt := 0; attempt < 100; attempt++ {
		random := make([]byte, 16)
		if _, err := rand.Read(random); err != nil {
			return "", "", errors.New("generate temporary backup name")
		}
		name := backupTempPrefix + hex.EncodeToString(random) + backupTempSuffix
		fd, err := unix.Openat(directory.fd, name, unix.O_RDWR|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0o600)
		if errors.Is(err, unix.EEXIST) {
			continue
		}
		if err != nil {
			return "", "", errors.New("create temporary backup")
		}
		if err := unix.Close(fd); err != nil {
			_ = unix.Unlinkat(directory.fd, name, 0)
			return "", "", errors.New("close temporary backup")
		}
		path := "/proc/self/fd/" + strconv.Itoa(directory.fd) + "/" + name
		return name, path, nil
	}
	return "", "", errors.New("create unique temporary backup")
}

func (directory *backupDirectory) cleanupTemporary(name string) {
	if name == "" {
		return
	}
	for _, candidate := range []string{name, name + "-wal", name + "-shm", name + "-journal"} {
		_ = directory.unlink(candidate)
	}
}

func (directory *backupDirectory) cleanupTemporarySidecars(name string) error {
	for _, candidate := range []string{name + "-wal", name + "-shm", name + "-journal"} {
		if err := directory.unlink(candidate); err != nil {
			return err
		}
	}
	return nil
}

func (directory *backupDirectory) unlink(name string) error {
	if !isBackupBasename(name) {
		return unix.EINVAL
	}
	err := unix.Unlinkat(directory.fd, name, 0)
	if errors.Is(err, unix.ENOENT) {
		return nil
	}
	return err
}

func (directory *backupDirectory) syncFile(name string) error {
	if !isBackupBasename(name) {
		return errors.New("invalid backup filename")
	}
	fd, err := unix.Openat(directory.fd, name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return errors.New("open backup for synchronization")
	}
	defer unix.Close(fd)
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil || stat.Mode&unix.S_IFMT != unix.S_IFREG || stat.Uid != uint32(os.Geteuid()) {
		return errors.New("verify backup for synchronization")
	}
	if err := unix.Fchmod(fd, 0o600); err != nil {
		return errors.New("restrict backup permissions")
	}
	if err := unix.Fsync(fd); err != nil {
		return errors.New("synchronize backup")
	}
	return nil
}

func (directory *backupDirectory) sync() error {
	if err := unix.Fsync(directory.fd); err != nil {
		return errors.New("synchronize backup directory")
	}
	return nil
}

func (directory *backupDirectory) publish(temporaryName, finalName string) error {
	if !isBackupBasename(temporaryName) || !isBackupBasename(finalName) {
		return unix.EINVAL
	}
	return unix.Renameat2(directory.fd, temporaryName, directory.fd, finalName, unix.RENAME_NOREPLACE)
}

func isBackupBasename(name string) bool {
	return name != "" && name != "." && name != ".." && filepath.Base(name) == name && !strings.ContainsRune(name, filepath.Separator)
}
