package cli

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestResolveConnectionConfigUsesExplicitFlagsBeforeEnvironmentAndFiles(t *testing.T) {
	snapshot := localConnectionSnapshot()
	tokenPath := writeTokenFile(t, "preferred", "ch_file\n", 0o600)
	root, serverFlag, tokenFile := resolverTestRoot(t, stringPointer("https://flag.example"), stringPointer(tokenPath))

	resolved, err := resolveConnectionConfig(root, snapshot, serverFlag, tokenFile, mapEnvironment(map[string]string{
		"CONFIGHUB_URL":   "ftp://invalid-environment.example",
		"CONFIGHUB_TOKEN": "invalid environment token",
	}))
	if err != nil {
		t.Fatal(err)
	}
	assertResolvedValue(t, resolved.Server, "https://flag.example", sourceServerFlag)
	assertResolvedValue(t, resolved.Token, "ch_file", sourceTokenFile)
}

func TestResolveConnectionConfigUsesEnvironmentBeforeFiles(t *testing.T) {
	root, serverFlag, tokenFile := resolverTestRoot(t, nil, nil)
	resolved, err := resolveConnectionConfig(root, localConnectionSnapshot(), serverFlag, tokenFile, mapEnvironment(map[string]string{
		"CONFIGHUB_URL":   "https://environment.example",
		"CONFIGHUB_TOKEN": "ch_environment",
	}))
	if err != nil {
		t.Fatal(err)
	}
	assertResolvedValue(t, resolved.Server, "https://environment.example", sourceEnvironment)
	assertResolvedValue(t, resolved.Token, "ch_environment", sourceEnvironment)
}

func TestResolveConnectionConfigKeepsFileValuesWithoutRuntimeOverrides(t *testing.T) {
	root, serverFlag, tokenFile := resolverTestRoot(t, nil, nil)
	snapshot := localConnectionSnapshot()
	resolved, err := resolveConnectionConfig(root, snapshot, serverFlag, tokenFile, mapEnvironment(nil))
	if err != nil {
		t.Fatal(err)
	}
	if resolved != snapshot {
		t.Fatalf("resolved=%+v want=%+v", resolved, snapshot)
	}
}

func TestResolveConnectionConfigTreatsEmptyEnvironmentAsAbsent(t *testing.T) {
	root, serverFlag, tokenFile := resolverTestRoot(t, nil, nil)
	resolved, err := resolveConnectionConfig(root, localConnectionSnapshot(), serverFlag, tokenFile, mapEnvironment(map[string]string{
		"CONFIGHUB_URL": "", "CONFIGHUB_TOKEN": "",
	}))
	if err != nil {
		t.Fatal(err)
	}
	assertResolvedValue(t, resolved.Server, "https://local.example", sourceLocal)
	assertResolvedValue(t, resolved.Token, "ch_local", sourceLocal)
}

func TestResolveConnectionConfigRejectsInvalidEnvironmentWithoutFallback(t *testing.T) {
	tests := []struct {
		name        string
		environment map[string]string
		secret      string
	}{
		{
			name:        "server",
			environment: map[string]string{"CONFIGHUB_URL": "ftp://PRIVATE_SERVER_PATH"},
			secret:      "PRIVATE_SERVER_PATH",
		},
		{
			name:        "token",
			environment: map[string]string{"CONFIGHUB_TOKEN": "PRIVATE TOKEN"},
			secret:      "PRIVATE TOKEN",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root, serverFlag, tokenFile := resolverTestRoot(t, nil, nil)
			_, err := resolveConnectionConfig(root, localConnectionSnapshot(), serverFlag, tokenFile, mapEnvironment(test.environment))
			if err == nil {
				t.Fatal("resolve succeeded")
			}
			if strings.Contains(err.Error(), test.secret) {
				t.Fatalf("error leaked secret: %q", err)
			}
		})
	}
}

func TestResolveConnectionConfigRejectsExplicitEmptyServerWithoutFallback(t *testing.T) {
	root, serverFlag, tokenFile := resolverTestRoot(t, stringPointer(""), nil)
	_, err := resolveConnectionConfig(root, localConnectionSnapshot(), serverFlag, tokenFile, mapEnvironment(nil))
	if err == nil {
		t.Fatal("resolve succeeded")
	}
}

func TestResolveConnectionConfigRejectsInvalidTokenFileWithoutFallback(t *testing.T) {
	root, serverFlag, tokenFile := resolverTestRoot(t, nil, stringPointer(t.TempDir()))
	_, err := resolveConnectionConfig(root, localConnectionSnapshot(), serverFlag, tokenFile, mapEnvironment(nil))
	if err == nil {
		t.Fatal("resolve succeeded")
	}
}

func TestRequireConnectionConfigRejectsMissingFields(t *testing.T) {
	tests := []configSnapshot{
		{},
		{Server: configValue{Value: "https://config.example.com", Present: true}},
		{Token: configValue{Value: "ch_token", Present: true}},
	}
	for _, snapshot := range tests {
		if _, _, err := requireConnectionConfig(snapshot); err == nil {
			t.Fatalf("snapshot=%+v succeeded", snapshot)
		}
	}
}

func localConnectionSnapshot() configSnapshot {
	return configSnapshot{
		Server: configValue{Value: "https://local.example", Present: true, Source: configSource{Kind: sourceLocal, Path: "/local"}},
		Token:  configValue{Value: "ch_local", Present: true, Source: configSource{Kind: sourceLocal, Path: "/local"}},
	}
}

func resolverTestRoot(t *testing.T, serverValue, tokenFileValue *string) (*cobra.Command, string, string) {
	t.Helper()
	root := &cobra.Command{Use: "test"}
	var serverFlag, tokenFile string
	root.PersistentFlags().StringVar(&serverFlag, "server", "", "")
	root.PersistentFlags().StringVar(&tokenFile, "token-file", "", "")
	if serverValue != nil {
		if err := root.PersistentFlags().Set("server", *serverValue); err != nil {
			t.Fatal(err)
		}
	}
	if tokenFileValue != nil {
		if err := root.PersistentFlags().Set("token-file", *tokenFileValue); err != nil {
			t.Fatal(err)
		}
	}
	return root, serverFlag, tokenFile
}

func assertResolvedValue(t *testing.T, got configValue, value string, source configSourceKind) {
	t.Helper()
	if !got.Present || got.Value != value || got.Source.Kind != source {
		t.Fatalf("value=%+v want value=%q source=%q", got, value, source)
	}
}
