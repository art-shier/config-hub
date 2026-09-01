package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSetFetchesUnfilteredRevisionThenMutates(t *testing.T) {
	tests := []struct {
		name           string
		args           []string
		key            string
		value          string
		serviceChanged bool
		service        string
		message        string
	}{
		{
			name:    "value with equals and default message",
			args:    []string{"set", "--project", "project", "--env", "production", "SETTING=left=right"},
			key:     "SETTING",
			value:   "left=right",
			message: "Set SETTING via CLI",
		},
		{
			name:           "empty value and explicitly empty service",
			args:           []string{"set", "--project", "project", "--env", "production", "--service=", "EMPTY="},
			key:            "EMPTY",
			value:          "",
			serviceChanged: true,
			service:        "",
			message:        "Set EMPTY via CLI",
		},
		{
			name:    "custom message with omitted service",
			args:    []string{"set", "--project", "project", "--env", "production", "--message", "scripted change", "CHANGE=value"},
			key:     "CHANGE",
			value:   "value",
			message: "scripted change",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server, observed := mutationFixtureServer(t, func(mutation MutationRequest) bool {
				return mutation.BaseRevision == 7 && mutation.Message == test.message &&
					mutation.Operation.Type == "set" && mutation.Operation.Key == test.key &&
					mutation.Operation.Value != nil && *mutation.Operation.Value == test.value &&
					pointerStringMatches(mutation.Operation.Service, test.serviceChanged, test.service)
			})
			defer server.Close()

			code, stdout, stderr := executeMutationCommand(test.args, server.URL, true, nil)
			if code != 0 || stdout != "revision 8\n" || stderr != "" || !observed.exactOrdering || !observed.mutationMatches || observed.requestCount != 2 {
				t.Fatalf("exit_ok=%v stdout_ok=%v stderr_empty=%v exact_ordering=%v mutation_matches=%v request_count=%d", code == 0, stdout == "revision 8\n", stderr == "", observed.exactOrdering, observed.mutationMatches, observed.requestCount)
			}
		})
	}
}

func TestUnsetFetchesUnfilteredRevisionThenMutates(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		key     string
		message string
	}{
		{
			name:    "default message",
			args:    []string{"unset", "--project", "project", "--env", "production", "REMOVE_ME"},
			key:     "REMOVE_ME",
			message: "Unset REMOVE_ME via CLI",
		},
		{
			name:    "custom message",
			args:    []string{"unset", "--project", "project", "--env", "production", "--message", "scripted removal", "REMOVE_ME"},
			key:     "REMOVE_ME",
			message: "scripted removal",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server, observed := mutationFixtureServer(t, func(mutation MutationRequest) bool {
				return mutation.BaseRevision == 7 && mutation.Message == test.message &&
					mutation.Operation.Type == "unset" && mutation.Operation.Key == test.key &&
					mutation.Operation.Value == nil && mutation.Operation.Service == nil
			})
			defer server.Close()

			code, stdout, stderr := executeMutationCommand(test.args, server.URL, true, nil)
			if code != 0 || stdout != "revision 8\n" || stderr != "" || !observed.exactOrdering || !observed.mutationMatches || observed.requestCount != 2 {
				t.Fatalf("exit_ok=%v stdout_ok=%v stderr_empty=%v exact_ordering=%v mutation_matches=%v request_count=%d", code == 0, stdout == "revision 8\n", stderr == "", observed.exactOrdering, observed.mutationMatches, observed.requestCount)
			}
		})
	}
}

func TestMutationCommandsRejectLocalInputWithoutRequests(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer server.Close()
	invalidUTF8 := string([]byte{0xff})
	tests := []struct {
		name        string
		args        []string
		credentials bool
	}{
		{name: "set missing argument", args: []string{"set", "--project", "project", "--env", "production"}, credentials: true},
		{name: "set extra argument", args: []string{"set", "--project", "project", "--env", "production", "KEY=value", "extra"}, credentials: true},
		{name: "unset missing argument", args: []string{"unset", "--project", "project", "--env", "production"}, credentials: true},
		{name: "unset extra argument", args: []string{"unset", "--project", "project", "--env", "production", "KEY", "extra"}, credentials: true},
		{name: "set missing equals", args: []string{"set", "--project", "project", "--env", "production", "KEY"}, credentials: true},
		{name: "set invalid key", args: []string{"set", "--project", "project", "--env", "production", "bad-key=value"}, credentials: true},
		{name: "set oversized key", args: []string{"set", "--project", "project", "--env", "production", strings.Repeat("K", 129) + "=value"}, credentials: true},
		{name: "unset invalid key", args: []string{"unset", "--project", "project", "--env", "production", "bad-key"}, credentials: true},
		{name: "unset oversized key", args: []string{"unset", "--project", "project", "--env", "production", strings.Repeat("K", 129)}, credentials: true},
		{name: "invalid service whitespace", args: []string{"set", "--project", "project", "--env", "production", "--service", " service", "KEY=value"}, credentials: true},
		{name: "invalid service UTF8", args: []string{"set", "--project", "project", "--env", "production", "--service", invalidUTF8, "KEY=value"}, credentials: true},
		{name: "invalid message whitespace", args: []string{"set", "--project", "project", "--env", "production", "--message", " message", "KEY=value"}, credentials: true},
		{name: "invalid message UTF8", args: []string{"set", "--project", "project", "--env", "production", "--message", invalidUTF8, "KEY=value"}, credentials: true},
		{name: "oversized message", args: []string{"set", "--project", "project", "--env", "production", "--message", strings.Repeat("m", 1025), "KEY=value"}, credentials: true},
		{name: "invalid value UTF8", args: []string{"set", "--project", "project", "--env", "production", "KEY=" + invalidUTF8}, credentials: true},
		{name: "oversized value", args: []string{"set", "--project", "project", "--env", "production", "KEY=" + strings.Repeat("v", (1<<20)+1)}, credentials: true},
		{name: "missing credentials", args: []string{"set", "--project", "project", "--env", "production", "KEY=value"}, credentials: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			requests := 0
			server.Config.Handler = http.HandlerFunc(func(http.ResponseWriter, *http.Request) { requests++ })
			code, stdout, stderr := executeMutationCommand(test.args, server.URL, test.credentials, &bytes.Buffer{})
			if code != 2 || stdout != "" || stderr == "" || requests != 0 {
				t.Fatalf("exit_ok=%v stdout_empty=%v stderr_present=%v request_count=%d", code == 2, stdout == "", stderr != "", requests)
			}
		})
	}
}

func TestMutationCommandConflictDoesNotRetry(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests++
		if request.Method == http.MethodGet {
			writeMutationFetchResponse(writer)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusConflict)
		_, _ = io.WriteString(writer, `{"error":{"code":"revision_conflict","message":"private server detail","request_id":"req-42"}}`)
	}))
	defer server.Close()

	code, stdout, stderr := executeMutationCommand([]string{"set", "--project", "project", "--env", "production", "KEY=value"}, server.URL, true, nil)
	want := "confighub: API request failed: status 409, code revision_conflict, request_id req-42\n"
	if code != 1 || stdout != "" || stderr != want || requests != 2 || strings.Contains(stderr, "private") {
		t.Fatalf("exit_ok=%v stdout_empty=%v diagnostic_ok=%v request_count=%d secret_absent=%v", code == 1, stdout == "", stderr == want, requests, !strings.Contains(stderr, "private"))
	}
}

func TestMutationCommandRuntimeErrorsAreSafe(t *testing.T) {
	t.Run("network", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
		serverURL := server.URL
		server.Close()
		code, stdout, stderr := executeMutationCommand([]string{"set", "--project", "project", "--env", "production", "KEY=value"}, serverURL, true, nil)
		if code != 1 || stdout != "" || stderr != "confighub: network request failed\n" {
			t.Fatalf("exit_ok=%v stdout_empty=%v diagnostic_ok=%v", code == 1, stdout == "", stderr == "confighub: network request failed\n")
		}
	})
	t.Run("API response does not leak", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			writer.Header().Set("Content-Type", "application/json")
			writer.WriteHeader(http.StatusBadGateway)
			_, _ = io.WriteString(writer, `{"error":{"code":"upstream_failure","message":"private value","request_id":"req-77"}}`)
		}))
		defer server.Close()
		code, stdout, stderr := executeMutationCommand([]string{"unset", "--project", "project", "--env", "production", "KEY"}, server.URL, true, nil)
		want := "confighub: API request failed: status 502, code upstream_failure, request_id req-77\n"
		if code != 1 || stdout != "" || stderr != want || strings.Contains(stderr, "private") {
			t.Fatalf("exit_ok=%v stdout_empty=%v diagnostic_ok=%v secret_absent=%v", code == 1, stdout == "", stderr == want, !strings.Contains(stderr, "private"))
		}
	})
	t.Run("stdout short write", func(t *testing.T) {
		server, observed := mutationFixtureServer(t, func(MutationRequest) bool { return true })
		defer server.Close()
		code, stdout, stderr := executeMutationCommand([]string{"unset", "--project", "project", "--env", "production", "KEY"}, server.URL, true, shortMutationWriter{})
		if code != 1 || stdout != "" || stderr != "confighub: stdout write failed\n" || observed.requestCount != 2 {
			t.Fatalf("exit_ok=%v stdout_empty=%v diagnostic_ok=%v request_count=%d", code == 1, stdout == "", stderr == "confighub: stdout write failed\n", observed.requestCount)
		}
	})
}

type mutationFixtureObservation struct {
	requestCount    int
	exactOrdering   bool
	mutationMatches bool
}

func mutationFixtureServer(t *testing.T, matchesMutation func(MutationRequest) bool) (*httptest.Server, *mutationFixtureObservation) {
	t.Helper()
	observed := &mutationFixtureObservation{}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		observed.requestCount++
		switch observed.requestCount {
		case 1:
			observed.exactOrdering = request.Method == http.MethodGet && request.URL.Path == "/api/v1/projects/project/environments/production/config" && request.URL.RawQuery == ""
			writeMutationFetchResponse(writer)
		case 2:
			observed.exactOrdering = observed.exactOrdering && request.Method == http.MethodPatch && request.URL.Path == "/api/v1/projects/project/environments/production/config" && request.URL.RawQuery == ""
			var mutation MutationRequest
			decodeErr := json.NewDecoder(request.Body).Decode(&mutation)
			observed.mutationMatches = decodeErr == nil && matchesMutation(mutation)
			writer.Header().Set("Content-Type", "application/json")
			writer.WriteHeader(http.StatusCreated)
			_, _ = io.WriteString(writer, `{"project":"project","environment":"production","revision":8,"created":true}`)
		default:
			observed.exactOrdering = false
			writer.WriteHeader(http.StatusInternalServerError)
		}
	}))
	return server, observed
}

func writeMutationFetchResponse(writer http.ResponseWriter) {
	writer.Header().Set("Content-Type", "application/json")
	_, _ = io.WriteString(writer, `{"project":"project","environment":"production","revision":7,"values":{}}`)
}

func pointerStringMatches(pointer *string, wantPresent bool, want string) bool {
	return (pointer != nil) == wantPresent && (pointer == nil || *pointer == want)
}

func executeMutationCommand(args []string, serverURL string, credentials bool, stdout io.Writer) (int, string, string) {
	snapshot := configSnapshot{Server: configValue{Value: serverURL, Present: true}}
	if credentials {
		snapshot.Token = configValue{Value: "ch_machine_token", Present: true}
	}
	var capturedStdout, stderr bytes.Buffer
	writer := stdout
	if writer == nil {
		writer = &capturedStdout
	}
	code := execute(context.Background(), args, mapEnvironment(nil), writer, &stderr, func() (configSnapshot, error) {
		return snapshot, nil
	})
	return code, capturedStdout.String(), stderr.String()
}

type shortMutationWriter struct{}

func (shortMutationWriter) Write(data []byte) (int, error) { return len(data) - 1, nil }
