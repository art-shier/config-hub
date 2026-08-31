//go:build !windows

package cli

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestLoadCLIConfigRejectsReadableTokenConfig(t *testing.T) {
	workingDir := t.TempDir()
	userConfigDir := t.TempDir()
	localPath := filepath.Join(workingDir, ".confighub.yaml")
	writeCLIConfig(t, localPath, "token: ch_secret\n", 0o640)

	_, err := loadCLIConfig(testConfigLocations(workingDir, userConfigDir))
	assertConfigLoadError(t, err, sourceLocal, localPath, "ch_secret")
}

func TestLoadCLIConfigAllowsReadableServerOnlyConfig(t *testing.T) {
	workingDir := t.TempDir()
	userConfigDir := t.TempDir()
	writeCLIConfig(t, filepath.Join(workingDir, ".confighub.yaml"), "server: https://config.example.com\n", 0o644)

	snapshot, err := loadCLIConfig(testConfigLocations(workingDir, userConfigDir))
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Server.Value != "https://config.example.com" {
		t.Fatalf("server=%+v", snapshot.Server)
	}
}

func TestLoadCLIConfigAllowsRestrictedTokenConfig(t *testing.T) {
	workingDir := t.TempDir()
	userConfigDir := t.TempDir()
	writeCLIConfig(t, filepath.Join(workingDir, ".confighub.yaml"), "token: ch_secret\n", 0o600)

	snapshot, err := loadCLIConfig(testConfigLocations(workingDir, userConfigDir))
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Token.Value != "ch_secret" {
		t.Fatalf("token=%+v", snapshot.Token)
	}
}

func TestLoadCLIConfigRejectsSymlink(t *testing.T) {
	workingDir := t.TempDir()
	userConfigDir := t.TempDir()
	target := filepath.Join(workingDir, "target.yaml")
	localPath := filepath.Join(workingDir, ".confighub.yaml")
	writeCLIConfig(t, target, "token: ch_secret\n", 0o600)
	if err := os.Symlink(target, localPath); err != nil {
		t.Fatal(err)
	}

	_, err := loadCLIConfig(testConfigLocations(workingDir, userConfigDir))
	assertConfigLoadError(t, err, sourceLocal, localPath, "ch_secret")
}

func TestLoadCLIConfigRejectsFIFOWithoutBlocking(t *testing.T) {
	workingDir := t.TempDir()
	userConfigDir := t.TempDir()
	localPath := filepath.Join(workingDir, ".confighub.yaml")
	if err := syscall.Mkfifo(localPath, 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := loadCLIConfig(testConfigLocations(workingDir, userConfigDir))
	assertConfigLoadError(t, err, sourceLocal, localPath, "")
}

func TestLoadCLIConfigDoesNotSearchParents(t *testing.T) {
	parent := t.TempDir()
	child := filepath.Join(parent, "child")
	if err := os.Mkdir(child, 0o700); err != nil {
		t.Fatal(err)
	}
	writeCLIConfig(t, filepath.Join(parent, ".confighub.yaml"), "token: ch_parent\n", 0o600)
	userConfigDir := t.TempDir()

	snapshot, err := loadCLIConfig(testConfigLocations(child, userConfigDir))
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Token.Present || snapshot.Local.State != configMissing ||
		snapshot.Local.Path != filepath.Join(child, ".confighub.yaml") {
		t.Fatalf("snapshot=%+v", snapshot)
	}
}
