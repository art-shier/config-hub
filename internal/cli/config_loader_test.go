package cli

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadCLIConfigDiscoversAndMergesGlobalAndCurrentDirectory(t *testing.T) {
	workingDir := t.TempDir()
	userConfigDir := t.TempDir()
	globalPath := filepath.Join(userConfigDir, "confighub", "config.yaml")
	localPath := filepath.Join(workingDir, ".confighub.yaml")
	writeCLIConfig(t, globalPath, "server: https://config.example.com\n", 0o644)
	writeCLIConfig(t, localPath, "token: ch_project_a\n", 0o600)

	snapshot, err := loadCLIConfig(testConfigLocations(workingDir, userConfigDir))
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Server.Value != "https://config.example.com" ||
		snapshot.Server.Source.Kind != sourceGlobal || snapshot.Server.Source.Path != globalPath {
		t.Fatalf("server=%+v", snapshot.Server)
	}
	if snapshot.Token.Value != "ch_project_a" ||
		snapshot.Token.Source.Kind != sourceLocal || snapshot.Token.Source.Path != localPath {
		t.Fatalf("token=%+v", snapshot.Token)
	}
	assertConfigFileStatus(t, snapshot.Global, globalPath, configLoaded)
	assertConfigFileStatus(t, snapshot.Local, localPath, configLoaded)
}

func TestLoadCLIConfigUsesGlobalOnly(t *testing.T) {
	workingDir := t.TempDir()
	userConfigDir := t.TempDir()
	globalPath := filepath.Join(userConfigDir, "confighub", "config.yaml")
	localPath := filepath.Join(workingDir, ".confighub.yaml")
	writeCLIConfig(t, globalPath, "server: https://config.example.com\ntoken: ch_global\n", 0o600)

	snapshot, err := loadCLIConfig(testConfigLocations(workingDir, userConfigDir))
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Server.Source.Kind != sourceGlobal || snapshot.Token.Source.Kind != sourceGlobal {
		t.Fatalf("server=%+v token=%+v", snapshot.Server, snapshot.Token)
	}
	assertConfigFileStatus(t, snapshot.Global, globalPath, configLoaded)
	assertConfigFileStatus(t, snapshot.Local, localPath, configMissing)
}

func TestLoadCLIConfigUsesLocalOnly(t *testing.T) {
	workingDir := t.TempDir()
	userConfigDir := t.TempDir()
	globalPath := filepath.Join(userConfigDir, "confighub", "config.yaml")
	localPath := filepath.Join(workingDir, ".confighub.yaml")
	writeCLIConfig(t, localPath, "server: https://local.example.com\ntoken: ch_local\n", 0o600)

	snapshot, err := loadCLIConfig(testConfigLocations(workingDir, userConfigDir))
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Server.Source.Kind != sourceLocal || snapshot.Token.Source.Kind != sourceLocal {
		t.Fatalf("server=%+v token=%+v", snapshot.Server, snapshot.Token)
	}
	assertConfigFileStatus(t, snapshot.Global, globalPath, configMissing)
	assertConfigFileStatus(t, snapshot.Local, localPath, configLoaded)
}

func TestLoadCLIConfigMarksBothMissing(t *testing.T) {
	workingDir := t.TempDir()
	userConfigDir := t.TempDir()
	globalPath := filepath.Join(userConfigDir, "confighub", "config.yaml")
	localPath := filepath.Join(workingDir, ".confighub.yaml")

	snapshot, err := loadCLIConfig(testConfigLocations(workingDir, userConfigDir))
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Server.Present || snapshot.Token.Present {
		t.Fatalf("snapshot=%+v", snapshot)
	}
	assertConfigFileStatus(t, snapshot.Global, globalPath, configMissing)
	assertConfigFileStatus(t, snapshot.Local, localPath, configMissing)
}

func TestLoadCLIConfigMarksGlobalUnavailable(t *testing.T) {
	workingDir := t.TempDir()
	localPath := filepath.Join(workingDir, ".confighub.yaml")
	writeCLIConfig(t, localPath, "token: ch_local\n", 0o600)
	locations := configLocations{
		Getwd: func() (string, error) { return workingDir, nil },
		UserConfigDir: func() (string, error) {
			return "", errors.New("user config unavailable")
		},
	}

	snapshot, err := loadCLIConfig(locations)
	if err != nil {
		t.Fatal(err)
	}
	assertConfigFileStatus(t, snapshot.Global, "", configUnavailable)
	assertConfigFileStatus(t, snapshot.Local, localPath, configLoaded)
	if snapshot.Token.Value != "ch_local" || snapshot.Token.Source.Kind != sourceLocal {
		t.Fatalf("token=%+v", snapshot.Token)
	}
}

func TestLoadCLIConfigRejectsInvalidGlobalBeforeLocalOverride(t *testing.T) {
	workingDir := t.TempDir()
	userConfigDir := t.TempDir()
	globalPath := filepath.Join(userConfigDir, "confighub", "config.yaml")
	localPath := filepath.Join(workingDir, ".confighub.yaml")
	writeCLIConfig(t, globalPath, "token: 'GLOBAL SECRET WITH SPACES'\n", 0o600)
	writeCLIConfig(t, localPath, "server: https://local.example.com\ntoken: ch_local\n", 0o600)

	_, err := loadCLIConfig(testConfigLocations(workingDir, userConfigDir))
	assertConfigLoadError(t, err, sourceGlobal, globalPath, "GLOBAL SECRET WITH SPACES")
}

func TestLoadCLIConfigRejectsInvalidLocal(t *testing.T) {
	workingDir := t.TempDir()
	userConfigDir := t.TempDir()
	localPath := filepath.Join(workingDir, ".confighub.yaml")
	writeCLIConfig(t, localPath, "unexpected: LOCAL_SECRET\n", 0o600)

	_, err := loadCLIConfig(testConfigLocations(workingDir, userConfigDir))
	assertConfigLoadError(t, err, sourceLocal, localPath, "LOCAL_SECRET")
}

func TestLoadCLIConfigRejectsOversizedFile(t *testing.T) {
	workingDir := t.TempDir()
	userConfigDir := t.TempDir()
	localPath := filepath.Join(workingDir, ".confighub.yaml")
	writeCLIConfig(t, localPath, "#"+strings.Repeat("x", maxCLIConfigBytes)+"\n", 0o600)

	_, err := loadCLIConfig(testConfigLocations(workingDir, userConfigDir))
	assertConfigLoadError(t, err, sourceLocal, localPath, "")
}

func testConfigLocations(workingDir, userConfigDir string) configLocations {
	return configLocations{
		Getwd:         func() (string, error) { return workingDir, nil },
		UserConfigDir: func() (string, error) { return userConfigDir, nil },
	}
}

func writeCLIConfig(t *testing.T, path, contents string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
}

func assertConfigFileStatus(t *testing.T, got configFileStatus, path string, state configFileState) {
	t.Helper()
	if got.Path != path || got.State != state {
		t.Fatalf("status=%+v want path=%q state=%q", got, path, state)
	}
}

func assertConfigLoadError(t *testing.T, err error, layer configSourceKind, path string, secret string) {
	t.Helper()
	var loadErr *configLoadError
	if !errors.As(err, &loadErr) {
		t.Fatalf("error=%v, want config load error", err)
	}
	if loadErr.Layer != layer || loadErr.Path != path {
		t.Fatalf("error=%+v want layer=%q path=%q", loadErr, layer, path)
	}
	if secret != "" && strings.Contains(err.Error(), secret) {
		t.Fatalf("error leaked secret: %q", err)
	}
}
