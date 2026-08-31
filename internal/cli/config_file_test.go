package cli

import (
	"errors"
	"strings"
	"testing"
)

func TestDecodeCLIConfigAcceptsOnlyOptionalServerAndToken(t *testing.T) {
	tests := []struct {
		name       string
		contents   string
		wantServer *string
		wantToken  *string
		wantErr    bool
	}{
		{name: "empty", contents: ""},
		{name: "empty mapping", contents: "{}\n"},
		{
			name:       "both",
			contents:   "server: https://config.example.com\ntoken: ch_project_a\n",
			wantServer: stringPointer("https://config.example.com"),
			wantToken:  stringPointer("ch_project_a"),
		},
		{
			name:       "server only",
			contents:   "server: https://config.example.com\n",
			wantServer: stringPointer("https://config.example.com"),
		},
		{name: "token only", contents: "token: ch_project_a\n", wantToken: stringPointer("ch_project_a")},
		{name: "unknown", contents: "profile: project-a\n", wantErr: true},
		{
			name:     "duplicate",
			contents: "server: https://one.example\nserver: https://two.example\n",
			wantErr:  true,
		},
		{
			name:     "second document",
			contents: "server: https://one.example\n---\ntoken: ch_secret\n",
			wantErr:  true,
		},
		{name: "sequence root", contents: "- server\n", wantErr: true},
		{name: "empty server", contents: "server: ''\n", wantErr: true},
		{name: "empty token", contents: "token: ''\n", wantErr: true},
		{name: "non-string server", contents: "server: 123\n", wantErr: true},
		{name: "non-string token", contents: "token: 123\n", wantErr: true},
		{name: "token whitespace", contents: "token: 'ch secret'\n", wantErr: true},
		{name: "invalid server", contents: "server: ftp://config.example.com\n", wantErr: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config, err := decodeCLIConfig([]byte(test.contents))
			if test.wantErr {
				if !errors.Is(err, errInvalidCLIConfig) {
					t.Fatalf("error=%v, want invalid CLI config", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !equalStringPointers(config.Server, test.wantServer) ||
				!equalStringPointers(config.Token, test.wantToken) {
				t.Fatalf("config=%+v wantServer=%v wantToken=%v", config, test.wantServer, test.wantToken)
			}
		})
	}
}

func TestDecodeCLIConfigEnforcesTokenSize(t *testing.T) {
	tests := []struct {
		name    string
		size    int
		wantErr bool
	}{
		{name: "maximum", size: 4096},
		{name: "over maximum", size: 4097, wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config, err := decodeCLIConfig([]byte("token: " + strings.Repeat("a", test.size) + "\n"))
			if test.wantErr {
				if !errors.Is(err, errInvalidCLIConfig) {
					t.Fatalf("error=%v, want invalid CLI config", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if config.Token == nil || len(*config.Token) != test.size {
				t.Fatalf("token length=%d want=%d", tokenPointerLength(config.Token), test.size)
			}
		})
	}
}

func TestDecodeCLIConfigRejectsOversizedDocument(t *testing.T) {
	contents := []byte("#" + strings.Repeat("a", 16*1024) + "\n")
	_, err := decodeCLIConfig(contents)
	if !errors.Is(err, errInvalidCLIConfig) {
		t.Fatalf("error=%v, want invalid CLI config", err)
	}
}

func TestMergeCLIConfigOverlaysOnlyPresentFields(t *testing.T) {
	snapshot := configSnapshot{}
	mergeCLIConfig(&snapshot, cliFileConfig{
		Server: stringPointer("https://config.example.com"),
		Token:  stringPointer("ch_global"),
	}, configSource{Kind: sourceGlobal, Path: "/config/global.yaml"})
	mergeCLIConfig(&snapshot, cliFileConfig{
		Token: stringPointer("ch_local"),
	}, configSource{Kind: sourceLocal, Path: "/workspace/project/.confighub.yaml"})

	if snapshot.Server.Value != "https://config.example.com" ||
		!snapshot.Server.Present || snapshot.Server.Source.Kind != sourceGlobal ||
		snapshot.Server.Source.Path != "/config/global.yaml" {
		t.Fatalf("server=%+v", snapshot.Server)
	}
	if snapshot.Token.Value != "ch_local" ||
		!snapshot.Token.Present || snapshot.Token.Source.Kind != sourceLocal ||
		snapshot.Token.Source.Path != "/workspace/project/.confighub.yaml" {
		t.Fatalf("token=%+v", snapshot.Token)
	}
}

func stringPointer(value string) *string { return &value }

func equalStringPointers(got, want *string) bool {
	if got == nil || want == nil {
		return got == nil && want == nil
	}
	return *got == *want
}

func tokenPointerLength(value *string) int {
	if value == nil {
		return -1
	}
	return len(*value)
}
