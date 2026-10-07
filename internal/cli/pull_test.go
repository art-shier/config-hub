package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestPullWritesAndProtectsFile(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_ = json.NewEncoder(writer).Encode(ConfigResponse{Project: "shop", Environment: "production", Revision: 1, Values: map[string]string{"PORT": "8080"}})
	}))
	defer server.Close()
	directory := filepath.Join(t.TempDir(), "nested")
	args := []string{"pull", "--project", "shop", "--env", "production", "--dir", directory, "--format", "json"}
	code, _, stderr := executeMutationCommand(args, server.URL, true, nil)
	if code != 0 {
		t.Fatalf("code=%d stderr=%s", code, stderr)
	}
	data, err := os.ReadFile(filepath.Join(directory, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	var values map[string]string
	if err := json.Unmarshal(data, &values); err != nil || values["PORT"] != "8080" || len(values) != 1 {
		t.Fatalf("invalid values: %s", data)
	}
	code, _, _ = executeMutationCommand(args, server.URL, true, nil)
	if code == 0 {
		t.Fatal("overwrote without force")
	}
	code, _, stderr = executeMutationCommand(append(args, "--force"), server.URL, true, nil)
	if code != 0 {
		t.Fatalf("force: %s", stderr)
	}
	entries, _ := os.ReadDir(directory)
	if len(entries) != 1 {
		t.Fatal("temporary files remain")
	}
}

func TestPullEncodings(t *testing.T) {
	values := map[string]string{"PORT": "8080", "FLAG": "true", "TEXT": "hello\nworld\"'\\", "EMPTY": ""}
	for _, format := range []string{"json", "jsonc", "yaml", "yml", "env", "dotenv"} {
		t.Run(format, func(t *testing.T) {
			data, err := encodePull(format, values)
			if err != nil {
				t.Fatal(err)
			}
			var decoded map[string]string
			switch format {
			case "json", "jsonc":
				err = json.Unmarshal(data, &decoded)
			case "yaml", "yml":
				err = yaml.Unmarshal(data, &decoded)
			default:
				expected := "EMPTY=\"\"\nFLAG=\"true\"\nPORT=\"8080\"\nTEXT=\"hello\\nworld\\\"'\\\\\"\n"
				if string(data) != expected {
					t.Fatalf("got %q want %q", data, expected)
				}
				return
			}
			if err != nil || !reflect.DeepEqual(decoded, values) {
				t.Fatalf("values=%v err=%v", decoded, err)
			}
		})
	}
	for _, invalid := range []map[string]string{{"BAD-KEY": "value"}, {"KEY": "nul\x00value"}} {
		if _, err := encodePull("env", invalid); err == nil {
			t.Fatal("accepted invalid dotenv")
		}
	}
}

func TestPullFailuresLeaveFilesUntouched(t *testing.T) {
	for _, invalidEncoding := range []bool{false, true} {
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			if !invalidEncoding {
				writer.WriteHeader(http.StatusInternalServerError)
				return
			}
			_ = json.NewEncoder(writer).Encode(ConfigResponse{Project: "shop", Environment: "production", Revision: 1, Values: map[string]string{"BAD-KEY": "secret"}})
		}))
		directory := t.TempDir()
		target := filepath.Join(directory, ".env")
		if err := os.WriteFile(target, []byte("original"), 0600); err != nil {
			t.Fatal(err)
		}
		code, stdout, _ := executeMutationCommand([]string{"pull", "--project", "shop", "--env", "production", "--dir", directory, "--format", "env", "--force"}, server.URL, true, nil)
		server.Close()
		data, _ := os.ReadFile(target)
		if code != 1 || string(data) != "original" || strings.Contains(stdout, "secret") {
			t.Fatalf("code=%d data=%q stdout=%q", code, data, stdout)
		}
	}
}

func TestPullArgumentsAndService(t *testing.T) {
	for _, extra := range [][]string{{"--format", "xml"}, {"--filename", "../outside.json"}, {"--filename", "sub\\outside.json"}, {"--filename", "."}, {"--filename", ""}, {"--filename", "file:stream"}, {"--dir", ""}, {"--project", "../shop"}} {
		args := append([]string{"pull", "--project", "shop", "--env", "production", "--dir", t.TempDir()}, extra...)
		code, _, _ := executeMutationCommand(args, "http://127.0.0.1:1", true, nil)
		if code != 2 {
			t.Fatalf("args=%v code=%d", args, code)
		}
	}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/api/v1/projects/shop/environments/production/config" || request.URL.Query().Get("service") != "api" || request.Header.Get("Authorization") != "Bearer ch_machine_token" {
			t.Errorf("unexpected request: %s", request.URL)
		}
		_ = json.NewEncoder(writer).Encode(ConfigResponse{Project: "shop", Environment: "production", Revision: 1, Values: map[string]string{"PORT": "8080"}})
	}))
	defer server.Close()
	for _, format := range []string{"json", "jsonc", "env", "dotenv", "yaml", "yml"} {
		directory := t.TempDir()
		code, stdout, stderr := executeMutationCommand([]string{"pull", "--project", "shop", "--env", "production", "--service", "api", "--dir", directory, "--format", format, "--filename", "settings"}, server.URL, true, nil)
		if code != 0 {
			t.Fatalf("format=%s stderr=%s", format, stderr)
		}
		if _, err := os.Stat(filepath.Join(directory, "settings")); err != nil || strings.Contains(stdout, "8080") {
			t.Fatalf("err=%v stdout=%s", err, stdout)
		}
	}
}

func TestPullDefaultFilenames(t *testing.T) {
	for format, expected := range map[string]string{"json": "config.json", "jsonc": "config.jsonc", "yaml": "config.yaml", "yml": "config.yml", "env": ".env", "dotenv": ".env"} {
		name, valid := pullFilename(format)
		if !valid || name != expected {
			t.Fatalf("format=%s name=%s", format, name)
		}
	}
}
