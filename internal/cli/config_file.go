package cli

import (
	"bytes"
	"errors"
	"io"

	"gopkg.in/yaml.v3"
)

const (
	maxCLIConfigBytes   = 16 * 1024
	maxConfigTokenBytes = 4096
)

var errInvalidCLIConfig = errors.New("invalid CLI configuration")

type cliFileConfig struct {
	Server *string
	Token  *string
}

type configSourceKind string

const (
	sourceNone        configSourceKind = "none"
	sourceGlobal      configSourceKind = "global"
	sourceLocal       configSourceKind = "local"
	sourceEnvironment configSourceKind = "environment"
	sourceServerFlag  configSourceKind = "--server"
	sourceTokenFile   configSourceKind = "--token-file"
)

type configSource struct {
	Kind configSourceKind
	Path string
}

type configValue struct {
	Value   string
	Present bool
	Source  configSource
}

type configFileState string

const (
	configMissing     configFileState = "missing"
	configLoaded      configFileState = "loaded"
	configUnavailable configFileState = "unavailable"
)

type configFileStatus struct {
	Path  string
	State configFileState
}

type configSnapshot struct {
	Server configValue
	Token  configValue
	Global configFileStatus
	Local  configFileStatus
}

func decodeCLIConfig(contents []byte) (cliFileConfig, error) {
	if len(contents) > maxCLIConfigBytes {
		return cliFileConfig{}, errInvalidCLIConfig
	}
	decoder := yaml.NewDecoder(bytes.NewReader(contents))
	var document yaml.Node
	if err := decoder.Decode(&document); err != nil {
		if errors.Is(err, io.EOF) {
			return cliFileConfig{}, nil
		}
		return cliFileConfig{}, errInvalidCLIConfig
	}
	var extra yaml.Node
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return cliFileConfig{}, errInvalidCLIConfig
	}
	if len(document.Content) != 1 || document.Content[0].Kind != yaml.MappingNode {
		return cliFileConfig{}, errInvalidCLIConfig
	}

	root := document.Content[0]
	seen := make(map[string]bool, 2)
	config := cliFileConfig{}
	for index := 0; index < len(root.Content); index += 2 {
		key, value := root.Content[index], root.Content[index+1]
		if key.Kind != yaml.ScalarNode || key.Tag != "!!str" ||
			value.Kind != yaml.ScalarNode || value.Tag != "!!str" || seen[key.Value] {
			return cliFileConfig{}, errInvalidCLIConfig
		}
		seen[key.Value] = true
		fieldValue := value.Value
		switch key.Value {
		case "server":
			config.Server = &fieldValue
		case "token":
			config.Token = &fieldValue
		default:
			return cliFileConfig{}, errInvalidCLIConfig
		}
	}

	if config.Server != nil {
		if *config.Server == "" {
			return cliFileConfig{}, errInvalidCLIConfig
		}
		if _, err := validateBaseURL(*config.Server); err != nil {
			return cliFileConfig{}, errInvalidCLIConfig
		}
	}
	if config.Token != nil &&
		(len(*config.Token) > maxConfigTokenBytes || !validToken(*config.Token)) {
		return cliFileConfig{}, errInvalidCLIConfig
	}
	return config, nil
}

func mergeCLIConfig(snapshot *configSnapshot, layer cliFileConfig, source configSource) {
	if layer.Server != nil {
		snapshot.Server = configValue{Value: *layer.Server, Present: true, Source: source}
	}
	if layer.Token != nil {
		snapshot.Token = configValue{Value: *layer.Token, Present: true, Source: source}
	}
}
