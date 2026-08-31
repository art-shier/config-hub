package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestConfigPathDisplaysLoadedMissingAndUnavailablePaths(t *testing.T) {
	tests := []struct {
		name     string
		snapshot configSnapshot
		want     string
	}{
		{
			name: "loaded and missing",
			snapshot: configSnapshot{
				Global: configFileStatus{Path: "/home/alice/.config/confighub/config.yaml", State: configLoaded},
				Local:  configFileStatus{Path: "/workspace/project/.confighub.yaml", State: configMissing},
			},
			want: "global: /home/alice/.config/confighub/config.yaml (loaded)\n" +
				"local: /workspace/project/.confighub.yaml (missing)\n",
		},
		{
			name: "unavailable global",
			snapshot: configSnapshot{
				Global: configFileStatus{State: configUnavailable},
				Local:  configFileStatus{Path: "/workspace/project/.confighub.yaml", State: configLoaded},
			},
			want: "global: <unavailable> (unavailable)\n" +
				"local: /workspace/project/.confighub.yaml (loaded)\n",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			code, stdout, stderr := executeWithConfigSnapshot([]string{"config", "path"}, test.snapshot, nil, mapEnvironment(nil))
			if code != 0 || stdout != test.want || stderr != "" {
				t.Fatalf("exit=%d stdout=%q stderr=%q", code, stdout, stderr)
			}
		})
	}
}

func TestConfigShowDisplaysSourcesAndMasksToken(t *testing.T) {
	const token = "ch_project_secret_7f2a"
	snapshot := configSnapshot{
		Server: configValue{
			Value: "https://config.example.com", Present: true,
			Source: configSource{Kind: sourceGlobal, Path: "/home/alice/.config/confighub/config.yaml"},
		},
		Token: configValue{
			Value: token, Present: true,
			Source: configSource{Kind: sourceLocal, Path: "/workspace/project/.confighub.yaml"},
		},
	}

	code, stdout, stderr := executeWithConfigSnapshot([]string{"config", "show"}, snapshot, nil, mapEnvironment(nil))
	want := "server: https://config.example.com\n" +
		"server_source: global (/home/alice/.config/confighub/config.yaml)\n" +
		"token: ch_***************7f2a\n" +
		"token_source: local (/workspace/project/.confighub.yaml)\n"
	if code != 0 || stdout != want || stderr != "" {
		t.Fatalf("exit=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if strings.Contains(stdout, token) {
		t.Fatalf("show leaked token: %q", stdout)
	}
}

func TestConfigShowDisplaysUnsetValuesWithoutFailing(t *testing.T) {
	code, stdout, stderr := executeWithConfigSnapshot([]string{"config", "show"}, configSnapshot{}, nil, mapEnvironment(nil))
	want := "server: <unset>\nserver_source: none\ntoken: <unset>\ntoken_source: none\n"
	if code != 0 || stdout != want || stderr != "" {
		t.Fatalf("exit=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}

func TestConfigShowFullyMasksShortToken(t *testing.T) {
	snapshot := configSnapshot{Token: configValue{
		Value: "short", Present: true, Source: configSource{Kind: sourceEnvironment},
	}}
	code, stdout, stderr := executeWithConfigSnapshot([]string{"config", "show"}, snapshot, nil, mapEnvironment(nil))
	if code != 0 || stderr != "" || !strings.Contains(stdout, "token: *****\n") || strings.Contains(stdout, "short") {
		t.Fatalf("exit=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}

func TestConfigGetWritesOnlyRequestedResolvedValue(t *testing.T) {
	snapshot := configSnapshot{
		Server: configValue{Value: "https://config.example.com", Present: true, Source: configSource{Kind: sourceGlobal}},
		Token:  configValue{Value: "ch_complete_secret", Present: true, Source: configSource{Kind: sourceLocal}},
	}
	tests := []struct {
		field string
		want  string
	}{
		{field: "server", want: "https://config.example.com\n"},
		{field: "token", want: "ch_complete_secret\n"},
	}
	for _, test := range tests {
		t.Run(test.field, func(t *testing.T) {
			code, stdout, stderr := executeWithConfigSnapshot([]string{"config", "get", test.field}, snapshot, nil, mapEnvironment(nil))
			if code != 0 || stdout != test.want || stderr != "" {
				t.Fatalf("exit=%d stdout=%q stderr=%q", code, stdout, stderr)
			}
		})
	}
}

func TestConfigGetRejectsUnknownOrUnsetField(t *testing.T) {
	tests := [][]string{
		{"config", "get"},
		{"config", "get", "unknown"},
		{"config", "get", "server", "extra"},
		{"config", "get", "server"},
	}
	for _, args := range tests {
		code, stdout, _ := executeWithConfigSnapshot(args, configSnapshot{}, nil, mapEnvironment(nil))
		if code != 2 || stdout != "" {
			t.Fatalf("args=%v exit=%d stdout=%q", args, code, stdout)
		}
	}
}

func TestConfigCommandsUseRuntimeOverrides(t *testing.T) {
	snapshot := configSnapshot{
		Server: configValue{Value: "https://file.example", Present: true, Source: configSource{Kind: sourceGlobal}},
		Token:  configValue{Value: "ch_file", Present: true, Source: configSource{Kind: sourceGlobal}},
	}
	getenv := mapEnvironment(map[string]string{
		"CONFIGHUB_URL":   "https://environment.example",
		"CONFIGHUB_TOKEN": "ch_environment",
	})
	code, stdout, stderr := executeWithConfigSnapshot([]string{"config", "show"}, snapshot, nil, getenv)
	want := "server: https://environment.example\nserver_source: environment\n" +
		"token: ch_*******ment\ntoken_source: environment\n"
	if code != 0 || stdout != want || stderr != "" {
		t.Fatalf("exit=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}

func TestConfigLoadFailureReportsOnlyLayerAndPath(t *testing.T) {
	path := "/workspace/project/.confighub.yaml"
	loadErr := &configLoadError{Layer: sourceLocal, Path: path}
	code, stdout, stderr := executeWithConfigSnapshot([]string{"config", "show"}, configSnapshot{}, loadErr, mapEnvironment(nil))
	if code != 2 || stdout != "" || stderr != "confighub: invalid local configuration: "+path+"\n" {
		t.Fatalf("exit=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}

func TestConfigOutputFailureIsRuntimeError(t *testing.T) {
	var stderr bytes.Buffer
	snapshot := configSnapshot{Server: configValue{Value: "https://config.example.com", Present: true}}
	code := execute(context.Background(), []string{"config", "get", "server"}, mapEnvironment(nil), failingConfigWriter{}, &stderr, func() (configSnapshot, error) {
		return snapshot, nil
	})
	if code != 1 || stderr.String() != "confighub: stdout write failed\n" {
		t.Fatalf("exit=%d stderr=%q", code, stderr.String())
	}
}

func TestVersionDoesNotLoadCLIConfiguration(t *testing.T) {
	called := false
	var stdout, stderr bytes.Buffer
	code := execute(context.Background(), []string{"version"}, mapEnvironment(nil), &stdout, &stderr, func() (configSnapshot, error) {
		called = true
		return configSnapshot{}, errors.New("must not load")
	})
	if code != 0 || called || stderr.Len() != 0 {
		t.Fatalf("exit=%d called=%v stdout=%q stderr=%q", code, called, stdout.String(), stderr.String())
	}
}

type failingConfigWriter struct{}

func (failingConfigWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

func executeWithConfigSnapshot(
	args []string,
	snapshot configSnapshot,
	loadErr error,
	getenv func(string) string,
) (int, string, string) {
	var stdout, stderr bytes.Buffer
	code := execute(context.Background(), args, getenv, &stdout, &stderr, func() (configSnapshot, error) {
		return snapshot, loadErr
	})
	return code, stdout.String(), stderr.String()
}
