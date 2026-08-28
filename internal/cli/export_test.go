package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

func TestWriteExportWritesStableIndentedJSONWithTrailingNewline(t *testing.T) {
	response := ConfigResponse{
		Project:     "shop",
		Environment: "production",
		Revision:    13,
		Values: map[string]string{
			"Z_LAST":  "last",
			"A_FIRST": "line one\nline two",
		},
	}
	var output bytes.Buffer
	if err := WriteExport(&output, "json", response); err != nil {
		t.Fatal(err)
	}
	want := "{\n" +
		"  \"project\": \"shop\",\n" +
		"  \"environment\": \"production\",\n" +
		"  \"revision\": 13,\n" +
		"  \"values\": {\n" +
		"    \"A_FIRST\": \"line one\\nline two\",\n" +
		"    \"Z_LAST\": \"last\"\n" +
		"  }\n" +
		"}\n"
	if output.String() != want {
		t.Fatalf("output=%q want=%q", output.String(), want)
	}
}

func TestWriteExportWritesDotenv(t *testing.T) {
	var output bytes.Buffer
	response := ConfigResponse{Values: map[string]string{"B": "two", "A": "one"}}
	if err := WriteExport(&output, "dotenv", response); err != nil {
		t.Fatal(err)
	}
	if got, want := output.String(), "A='one'\nB='two'\n"; got != want {
		t.Fatalf("output=%q want=%q", got, want)
	}
}

func TestWriteExportDoesNotWriteWhenEncodingFails(t *testing.T) {
	writer := new(countingWriter)
	response := ConfigResponse{Values: map[string]string{"BAD-NAME": "CONFIG_SECRET"}}
	if err := WriteExport(writer, "dotenv", response); err == nil {
		t.Fatal("WriteExport succeeded")
	}
	if writer.calls != 0 || writer.output.Len() != 0 {
		t.Fatalf("writer calls=%d output=%q", writer.calls, writer.output.String())
	}
}

func TestWriteExportRejectsUnknownFormatBeforeWriting(t *testing.T) {
	writer := new(countingWriter)
	if err := WriteExport(writer, "yaml", ConfigResponse{Values: map[string]string{}}); err == nil {
		t.Fatal("WriteExport succeeded")
	}
	if writer.calls != 0 {
		t.Fatalf("writer calls=%d", writer.calls)
	}
}

func TestWriteExportReturnsWriterErrors(t *testing.T) {
	wantErr := io.ErrClosedPipe
	writer := writerFunc(func([]byte) (int, error) { return 0, wantErr })
	if err := WriteExport(writer, "json", ConfigResponse{Values: map[string]string{}}); !errors.Is(err, wantErr) {
		t.Fatalf("error=%v want=%v", err, wantErr)
	}
}

func TestExecuteExportUsesExplicitServerAndTokenFileBeforeEnvironment(t *testing.T) {
	const fileToken = "file-token"
	var gotAuthorization, gotQuery string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuthorization = r.Header.Get("Authorization")
		gotQuery = r.URL.RawQuery
		_, _ = io.WriteString(w, `{"project":"shop","environment":"production","revision":13,"values":{"PORT":"8080"}}`)
	}))
	defer server.Close()
	tokenPath := writeTokenFile(t, "preferred-token", fileToken+"\r\n", 0o600)
	getenv := mapEnvironment(map[string]string{
		"CONFIGHUB_URL":   "http://localhost.evil",
		"CONFIGHUB_TOKEN": "lower-priority-token",
	})
	var stdout, stderr bytes.Buffer

	code := Execute(context.Background(), []string{
		"export", "--server", server.URL, "--token-file", tokenPath,
		"--project", "shop", "--env", "production", "--service", "api worker", "--format", "json",
	}, getenv, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("exit=%d stderr=%q", code, stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr=%q", stderr.String())
	}
	if gotAuthorization != "Bearer "+fileToken {
		t.Fatalf("Authorization=%q", gotAuthorization)
	}
	if gotQuery != "service=api+worker" {
		t.Fatalf("query=%q", gotQuery)
	}
	if !strings.Contains(stdout.String(), `"revision": 13`) || !strings.HasSuffix(stdout.String(), "\n") {
		t.Fatalf("stdout=%q", stdout.String())
	}
}

func TestExecuteExportFallsBackToEnvironment(t *testing.T) {
	const token = "environment-token"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer "+token {
			t.Errorf("Authorization=%q", got)
		}
		_, _ = io.WriteString(w, `{"project":"shop","environment":"dev","revision":1,"values":{"PORT":"8080"}}`)
	}))
	defer server.Close()
	var stdout, stderr bytes.Buffer
	code := Execute(context.Background(), []string{
		"export", "--project", "shop", "--env", "dev", "--format", "dotenv",
	}, mapEnvironment(map[string]string{"CONFIGHUB_URL": server.URL, "CONFIGHUB_TOKEN": token}), &stdout, &stderr)
	if code != 0 || stdout.String() != "PORT='8080'\n" || stderr.Len() != 0 {
		t.Fatalf("exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestExecuteDoesNotFallbackFromInvalidExplicitServer(t *testing.T) {
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		attempts.Add(1)
	}))
	defer server.Close()
	var stdout, stderr bytes.Buffer
	code := Execute(context.Background(), []string{
		"export", "--server", "", "--project", "shop", "--env", "production", "--format", "json",
	}, mapEnvironment(map[string]string{"CONFIGHUB_URL": server.URL, "CONFIGHUB_TOKEN": "token"}), &stdout, &stderr)
	if code != 2 || attempts.Load() != 0 || stdout.Len() != 0 {
		t.Fatalf("exit=%d attempts=%d stdout=%q", code, attempts.Load(), stdout.String())
	}
}

func TestExecuteMapsInvalidProjectOrEnvironmentToLocalInput(t *testing.T) {
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		attempts.Add(1)
	}))
	defer server.Close()
	for _, args := range [][]string{
		{"export", "--project", "../shop", "--env", "production", "--format", "json"},
		{"export", "--project", "shop", "--env", "Production", "--format", "json"},
	} {
		var stdout, stderr bytes.Buffer
		code := Execute(context.Background(), args, mapEnvironment(map[string]string{
			"CONFIGHUB_URL": server.URL, "CONFIGHUB_TOKEN": "token",
		}), &stdout, &stderr)
		if code != 2 || stdout.Len() != 0 {
			t.Fatalf("args=%v exit=%d stdout=%q stderr=%q", args, code, stdout.String(), stderr.String())
		}
	}
	if got := attempts.Load(); got != 0 {
		t.Fatalf("invalid slugs caused %d requests", got)
	}
}

func TestExecuteDoesNotFallbackFromInvalidExplicitTokenFile(t *testing.T) {
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		attempts.Add(1)
	}))
	defer server.Close()

	tests := []struct {
		name    string
		content string
		mode    os.FileMode
		dir     bool
	}{
		{name: "group readable", content: "file-token\n", mode: 0o640},
		{name: "other readable", content: "file-token\n", mode: 0o604},
		{name: "empty", content: "", mode: 0o600},
		{name: "embedded space", content: "file token\n", mode: 0o600},
		{name: "control byte", content: "file\x00token\n", mode: 0o600},
		{name: "multiple lines", content: "file-token\nsecond-token\n", mode: 0o600},
		{name: "too large", content: strings.Repeat("x", maxTokenFileBytes+1), mode: 0o600},
		{name: "directory", mode: 0o700, dir: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "sensitive-token-file-path")
			if test.dir {
				if err := os.Mkdir(path, test.mode); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := os.WriteFile(path, []byte(test.content), 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.Chmod(path, test.mode); err != nil {
					t.Fatal(err)
				}
			}
			var stdout, stderr bytes.Buffer
			code := Execute(context.Background(), []string{
				"export", "--token-file", path, "--project", "shop", "--env", "production", "--format", "json",
			}, mapEnvironment(map[string]string{"CONFIGHUB_URL": server.URL, "CONFIGHUB_TOKEN": "fallback-token"}), &stdout, &stderr)
			if code != 2 || stdout.Len() != 0 {
				t.Fatalf("exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
			}
			for _, secret := range []string{path, "sensitive-token-file-path", "fallback-token", strings.TrimSpace(test.content)} {
				if secret != "" && strings.Contains(stderr.String(), secret) {
					t.Fatalf("stderr leaked %q: %q", secret, stderr.String())
				}
			}
		})
	}
	if got := attempts.Load(); got != 0 {
		t.Fatalf("invalid token files caused %d fallback requests", got)
	}
}

func TestExecuteRejectsInvalidEnvironmentTokenWithoutLeakingIt(t *testing.T) {
	const token = "ENV TOKEN SECRET"
	var stdout, stderr bytes.Buffer
	code := Execute(context.Background(), []string{
		"export", "--project", "shop", "--env", "production", "--format", "json",
	}, mapEnvironment(map[string]string{"CONFIGHUB_URL": "https://config.example.com", "CONFIGHUB_TOKEN": token}), &stdout, &stderr)
	if code != 2 || stdout.Len() != 0 || strings.Contains(stderr.String(), token) {
		t.Fatalf("exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestReadTokenFileRejectsSymlinkAndFIFOWithoutBlocking(t *testing.T) {
	target := writeTokenFile(t, "token-target", "file-token\n", 0o600)
	symlink := filepath.Join(t.TempDir(), "token-link")
	if err := os.Symlink(target, symlink); err != nil {
		t.Fatal(err)
	}
	if _, err := readTokenFile(symlink); err == nil {
		t.Fatal("readTokenFile accepted a symlink")
	}

	fifo := filepath.Join(t.TempDir(), "token-fifo")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() {
		_, err := readTokenFile(fifo)
		result <- err
	}()
	select {
	case err := <-result:
		if err == nil {
			t.Fatal("readTokenFile accepted a FIFO")
		}
	case <-time.After(time.Second):
		t.Fatal("readTokenFile blocked while opening a FIFO")
	}
}

func TestRootDefinesTokenFileButNoPlaintextTokenFlag(t *testing.T) {
	command := newRootCommand(context.Background(), os.Getenv, io.Discard, io.Discard)
	if command.PersistentFlags().Lookup("token-file") == nil {
		t.Fatal("--token-file is not defined")
	}
	if command.PersistentFlags().Lookup("token") != nil {
		t.Fatal("plaintext --token flag is defined")
	}
	export, _, err := command.Find([]string{"export"})
	if err != nil {
		t.Fatal(err)
	}
	if export.Flags().Lookup("token") != nil || export.PersistentFlags().Lookup("token") != nil {
		t.Fatal("export defines plaintext --token flag")
	}
}

func TestExecuteWritesHelpOnlyToStderr(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := Execute(context.Background(), []string{"export", "--help"}, os.Getenv, &stdout, &stderr)
	if code != 0 || stdout.Len() != 0 {
		t.Fatalf("exit=%d stdout=%q", code, stdout.String())
	}
	if !strings.Contains(stderr.String(), "--token-file") || !strings.Contains(stderr.String(), "--server") {
		t.Fatalf("help=%q", stderr.String())
	}
}

func TestExecuteMapsUsageAndRuntimeErrorsWithoutSensitiveOutput(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := Execute(context.Background(), nil, os.Getenv, &stdout, &stderr); code != 2 || stdout.Len() != 0 || stderr.Len() == 0 {
		t.Fatalf("usage exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}

	const token = "RUNTIME_TOKEN_SECRET"
	const responseSecret = "RESPONSE_BODY_CONFIG_SECRET"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, `{"error":{"code":"internal_error","message":"`+responseSecret+`","request_id":"req_1","fields":{}}}`)
	}))
	defer server.Close()
	stdout.Reset()
	stderr.Reset()
	code := Execute(context.Background(), []string{
		"export", "--project", "shop", "--env", "production", "--format", "json",
	}, mapEnvironment(map[string]string{"CONFIGHUB_URL": server.URL, "CONFIGHUB_TOKEN": token}), &stdout, &stderr)
	if code != 1 || stdout.Len() != 0 {
		t.Fatalf("runtime exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	for _, secret := range []string{token, responseSecret} {
		if strings.Contains(stderr.String(), secret) {
			t.Fatalf("stderr leaked %q: %q", secret, stderr.String())
		}
	}
}

func TestExecuteLeavesStdoutEmptyWhenDotenvEncodingFails(t *testing.T) {
	const configSecret = "CONFIG_VALUE_SECRET"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"project":"shop","environment":"production","revision":1,"values":{"BAD-NAME":"`+configSecret+`"}}`)
	}))
	defer server.Close()
	var stdout, stderr bytes.Buffer
	code := Execute(context.Background(), []string{
		"export", "--project", "shop", "--env", "production", "--format", "dotenv",
	}, mapEnvironment(map[string]string{"CONFIGHUB_URL": server.URL, "CONFIGHUB_TOKEN": "token"}), &stdout, &stderr)
	if code != 1 || stdout.Len() != 0 || strings.Contains(stderr.String(), configSecret) {
		t.Fatalf("exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

type countingWriter struct {
	calls  int
	output bytes.Buffer
}

func (w *countingWriter) Write(value []byte) (int, error) {
	w.calls++
	return w.output.Write(value)
}

type writerFunc func([]byte) (int, error)

func (f writerFunc) Write(value []byte) (int, error) { return f(value) }

func writeTokenFile(t *testing.T, name, content string, mode os.FileMode) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
	return path
}

func mapEnvironment(values map[string]string) func(string) string {
	return func(key string) string { return values[key] }
}
