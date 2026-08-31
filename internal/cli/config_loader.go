package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

const localCLIConfigName = ".confighub.yaml"

type configLocations struct {
	Getwd         func() (string, error)
	UserConfigDir func() (string, error)
}

type configLoadError struct {
	Layer configSourceKind
	Path  string
}

func (e *configLoadError) Error() string {
	if e.Path == "" {
		return fmt.Sprintf("invalid %s configuration", e.Layer)
	}
	return fmt.Sprintf("invalid %s configuration: %s", e.Layer, e.Path)
}

func defaultConfigLocations() configLocations {
	return configLocations{Getwd: os.Getwd, UserConfigDir: os.UserConfigDir}
}

func loadCLIConfig(locations configLocations) (configSnapshot, error) {
	if locations.Getwd == nil {
		locations.Getwd = os.Getwd
	}
	if locations.UserConfigDir == nil {
		locations.UserConfigDir = os.UserConfigDir
	}

	workingDir, err := locations.Getwd()
	if err != nil {
		return configSnapshot{}, &configLoadError{Layer: sourceLocal}
	}
	localPath, err := filepath.Abs(filepath.Join(workingDir, localCLIConfigName))
	if err != nil {
		return configSnapshot{}, &configLoadError{Layer: sourceLocal}
	}

	snapshot := configSnapshot{
		Global: configFileStatus{State: configUnavailable},
		Local:  configFileStatus{Path: localPath, State: configMissing},
	}
	userConfigDir, userConfigErr := locations.UserConfigDir()
	if userConfigErr == nil {
		globalPath, absoluteErr := filepath.Abs(filepath.Join(userConfigDir, "confighub", "config.yaml"))
		if absoluteErr != nil {
			return configSnapshot{}, &configLoadError{Layer: sourceGlobal}
		}
		snapshot.Global = configFileStatus{Path: globalPath, State: configMissing}
		globalConfig, state, loadErr := loadCLIConfigFile(globalPath, sourceGlobal)
		if loadErr != nil {
			return configSnapshot{}, loadErr
		}
		snapshot.Global.State = state
		if state == configLoaded {
			mergeCLIConfig(&snapshot, globalConfig, configSource{Kind: sourceGlobal, Path: globalPath})
		}
	}

	localConfig, state, loadErr := loadCLIConfigFile(localPath, sourceLocal)
	if loadErr != nil {
		return configSnapshot{}, loadErr
	}
	snapshot.Local.State = state
	if state == configLoaded {
		mergeCLIConfig(&snapshot, localConfig, configSource{Kind: sourceLocal, Path: localPath})
	}
	return snapshot, nil
}

func loadCLIConfigFile(path string, layer configSourceKind) (cliFileConfig, configFileState, error) {
	file, err := openRestrictedFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return cliFileConfig{}, configMissing, nil
	}
	if err != nil {
		return cliFileConfig{}, "", &configLoadError{Layer: layer, Path: path}
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return cliFileConfig{}, "", &configLoadError{Layer: layer, Path: path}
	}
	contents, err := io.ReadAll(io.LimitReader(file, maxCLIConfigBytes+1))
	if err != nil || len(contents) > maxCLIConfigBytes {
		return cliFileConfig{}, "", &configLoadError{Layer: layer, Path: path}
	}
	config, err := decodeCLIConfig(contents)
	if err != nil || config.Token != nil && !tokenFilePermissionsValid(info.Mode()) {
		return cliFileConfig{}, "", &configLoadError{Layer: layer, Path: path}
	}
	return config, configLoaded, nil
}
