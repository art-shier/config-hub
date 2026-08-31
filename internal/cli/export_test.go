package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
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

func TestWriteExportDoesNotWriteDotenvContainingNUL(t *testing.T) {
	writer := new(countingWriter)
	response := ConfigResponse{Values: map[string]string{"KEY": "CONFIG\x00SECRET"}}
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

func TestWriteExportClassifiesEncodingAndOutputFailures(t *testing.T) {
	t.Run("encoding", func(t *testing.T) {
		err := WriteExport(io.Discard, "dotenv", ConfigResponse{Values: map[string]string{"KEY": "before\x00after"}})
		if !errors.Is(err, errExportEncoding) {
			t.Fatalf("error=%v want encoding category", err)
		}
	})
	t.Run("writer error", func(t *testing.T) {
		err := WriteExport(writerFunc(func([]byte) (int, error) {
			return 0, io.ErrClosedPipe
		}), "json", ConfigResponse{Values: map[string]string{}})
		if !errors.Is(err, errOutputWrite) || !errors.Is(err, io.ErrClosedPipe) {
			t.Fatalf("error=%v want output and closed-pipe categories", err)
		}
	})
	t.Run("short write", func(t *testing.T) {
		err := WriteExport(writerFunc(func(value []byte) (int, error) {
			return len(value) - 1, nil
		}), "json", ConfigResponse{Values: map[string]string{}})
		if !errors.Is(err, errOutputWrite) || !errors.Is(err, io.ErrShortWrite) {
			t.Fatalf("error=%v want output and short-write categories", err)
		}
	})
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

func TestExecuteExportUsesLayeredConfigurationFiles(t *testing.T) {
	const token = "ch_project_token"
	var authorization string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		authorization = request.Header.Get("Authorization")
		_, _ = io.WriteString(w, `{"project":"shop","environment":"production","revision":13,"values":{"PORT":"8080"}}`)
	}))
	defer server.Close()
	snapshot := configSnapshot{
		Server: configValue{Value: server.URL, Present: true, Source: configSource{Kind: sourceGlobal, Path: "/global/config.yaml"}},
		Token:  configValue{Value: token, Present: true, Source: configSource{Kind: sourceLocal, Path: "/project/.confighub.yaml"}},
	}
	var stdout, stderr bytes.Buffer

	code := execute(context.Background(), []string{
		"export", "--project", "shop", "--env", "production", "--format", "json",
	}, mapEnvironment(nil), &stdout, &stderr, func() (configSnapshot, error) {
		return snapshot, nil
	})

	if code != 0 || stderr.Len() != 0 {
		t.Fatalf("exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if authorization != "Bearer "+token {
		t.Fatalf("Authorization=%q", authorization)
	}
	if !strings.Contains(stdout.String(), `"revision": 13`) {
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

func TestExecuteRejectsInvalidExplicitServerPortWithoutFallbackOrLeak(t *testing.T) {
	const invalidURL = "https://localhost:65536/private-sensitive-server-path"
	var attempts atomic.Int32
	fallback := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		attempts.Add(1)
	}))
	defer fallback.Close()
	var stdout, stderr bytes.Buffer
	code := Execute(context.Background(), []string{
		"export", "--server", invalidURL, "--project", "shop", "--env", "production", "--format", "json",
	}, mapEnvironment(map[string]string{"CONFIGHUB_URL": fallback.URL, "CONFIGHUB_TOKEN": "token"}), &stdout, &stderr)
	if code != 2 || attempts.Load() != 0 || stdout.Len() != 0 {
		t.Fatalf("exit=%d attempts=%d stdout=%q stderr=%q", code, attempts.Load(), stdout.String(), stderr.String())
	}
	if strings.Contains(stderr.String(), invalidURL) || strings.Contains(stderr.String(), "private-sensitive-server-path") {
		t.Fatalf("stderr leaked invalid server URL: %q", stderr.String())
	}
}

func TestExecuteRejectsMalformedExplicitIPLiteralWithoutFallbackOrLeak(t *testing.T) {
	const invalidURL = "https://[not:ipv6]/private-sensitive-server-path"
	var attempts atomic.Int32
	fallback := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		attempts.Add(1)
	}))
	defer fallback.Close()
	var stdout, stderr bytes.Buffer
	code := Execute(context.Background(), []string{
		"export", "--server", invalidURL, "--project", "shop", "--env", "production", "--format", "json",
	}, mapEnvironment(map[string]string{"CONFIGHUB_URL": fallback.URL, "CONFIGHUB_TOKEN": "token"}), &stdout, &stderr)
	if code != 2 || attempts.Load() != 0 || stdout.Len() != 0 {
		t.Fatalf("exit=%d attempts=%d stdout=%q stderr=%q", code, attempts.Load(), stdout.String(), stderr.String())
	}
	if strings.Contains(stderr.String(), invalidURL) || strings.Contains(stderr.String(), "private-sensitive-server-path") {
		t.Fatalf("stderr leaked invalid server URL: %q", stderr.String())
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

func TestExecuteRejectsInvalidServiceAsLocalInputBeforeRequest(t *testing.T) {
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		attempts.Add(1)
		_, _ = io.WriteString(w, `{"project":"shop","environment":"production","revision":1,"values":{}}`)
	}))
	defer server.Close()
	for _, test := range []struct {
		name    string
		service string
	}{
		{name: "invalid UTF-8", service: string([]byte{0xff})},
		{name: "too long", service: strings.Repeat("s", 129)},
	} {
		t.Run(test.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := Execute(context.Background(), []string{
				"export", "--project", "shop", "--env", "production", "--service", test.service, "--format", "json",
			}, mapEnvironment(map[string]string{
				"CONFIGHUB_URL": server.URL, "CONFIGHUB_TOKEN": "token",
			}), &stdout, &stderr)
			if code != 2 || stdout.Len() != 0 {
				t.Fatalf("service bytes=%d exit=%d stdout=%q stderr=%q", len(test.service), code, stdout.String(), stderr.String())
			}
			if strings.Contains(stderr.String(), test.service) {
				t.Fatalf("stderr leaked invalid service: %q", stderr.String())
			}
		})
	}
	if attempts.Load() != 0 {
		t.Fatalf("invalid services caused %d requests", attempts.Load())
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
		{name: "empty", content: "", mode: 0o600},
		{name: "embedded space", content: "file token\n", mode: 0o600},
		{name: "control byte", content: "file\x00token\n", mode: 0o600},
		{name: "multiple lines", content: "file-token\nsecond-token\n", mode: 0o600},
		{name: "too large", content: strings.Repeat("x", maxTokenFileBytes+1), mode: 0o600},
		{name: "directory", mode: 0o700, dir: true},
	}
	if runtime.GOOS != "windows" {
		tests = append(tests,
			struct {
				name    string
				content string
				mode    os.FileMode
				dir     bool
			}{name: "group readable", content: "file-token\n", mode: 0o640},
			struct {
				name    string
				content string
				mode    os.FileMode
				dir     bool
			}{name: "other readable", content: "file-token\n", mode: 0o604},
		)
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

func TestReadTokenFileRejectsSymlink(t *testing.T) {
	target := writeTokenFile(t, "token-target", "file-token\n", 0o600)
	symlink := filepath.Join(t.TempDir(), "token-link")
	if err := os.Symlink(target, symlink); err != nil {
		if runtime.GOOS == "windows" {
			t.Skipf("creating a file symlink requires an unavailable Windows privilege: %v", err)
		}
		t.Fatal(err)
	}
	if _, err := readTokenFile(symlink); err == nil {
		t.Fatal("readTokenFile accepted a symlink")
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

func TestExecuteReportsSafeAPIDiagnostics(t *testing.T) {
	const token = "RUNTIME_TOKEN_SECRET"
	tests := []struct {
		name      string
		status    int
		body      string
		want      string
		forbidden []string
	}{
		{
			name:   "safe code and request ID",
			status: http.StatusUnprocessableEntity,
			body:   `{"error":{"code":"validation_failed","message":"REMOTE_MESSAGE_SECRET","request_id":"req-42","fields":{"service":"REMOTE_FIELD_SECRET"}}}`,
			want:   "confighub: API request failed: status 422, code validation_failed, request_id req-42\n",
			forbidden: []string{
				"REMOTE_MESSAGE_SECRET", "REMOTE_FIELD_SECRET",
			},
		},
		{
			name:   "unsafe diagnostic characters",
			status: http.StatusInternalServerError,
			body:   `{"error":{"code":"unsafe/code","message":"REMOTE_MESSAGE_SECRET","request_id":"unsafe request id","fields":{"secret":"REMOTE_FIELD_SECRET"}}}`,
			want:   "confighub: API request failed: status 500\n",
			forbidden: []string{
				"unsafe/code", "unsafe request id", "REMOTE_MESSAGE_SECRET", "REMOTE_FIELD_SECRET",
			},
		},
		{
			name:   "diagnostic fields starting with punctuation",
			status: http.StatusInternalServerError,
			body:   `{"error":{"code":".hidden","message":"REMOTE_MESSAGE_SECRET","request_id":"-option","fields":{}}}`,
			want:   "confighub: API request failed: status 500\n",
			forbidden: []string{
				".hidden", "-option", "REMOTE_MESSAGE_SECRET",
			},
		},
		{
			name:   "overlong diagnostic fields",
			status: http.StatusBadGateway,
			body: `{"error":{"code":"` + strings.Repeat("c", 65) + `","message":"REMOTE_MESSAGE_SECRET","request_id":"` + strings.Repeat("r", 129) +
				`","fields":{"secret":"REMOTE_FIELD_SECRET"}}}`,
			want: "confighub: API request failed: status 502\n",
			forbidden: []string{
				strings.Repeat("c", 65), strings.Repeat("r", 129), "REMOTE_MESSAGE_SECRET", "REMOTE_FIELD_SECRET",
			},
		},
		{
			name:      "malformed envelope",
			status:    http.StatusServiceUnavailable,
			body:      "MALFORMED_API_BODY_SECRET",
			want:      "confighub: API request failed: status 503\n",
			forbidden: []string{"MALFORMED_API_BODY_SECRET"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(test.status)
				_, _ = io.WriteString(w, test.body)
			}))
			defer server.Close()
			var stdout, stderr bytes.Buffer
			code := Execute(context.Background(), []string{
				"export", "--project", "shop", "--env", "production", "--format", "json",
			}, mapEnvironment(map[string]string{
				"CONFIGHUB_URL": server.URL + "/private-sensitive-url-path", "CONFIGHUB_TOKEN": token,
			}), &stdout, &stderr)
			if code != 1 || stdout.Len() != 0 || stderr.String() != test.want {
				t.Fatalf("exit=%d stdout=%q stderr=%q want=%q", code, stdout.String(), stderr.String(), test.want)
			}
			for _, secret := range append(test.forbidden, token, "private-sensitive-url-path") {
				if strings.Contains(stderr.String(), secret) {
					t.Fatalf("stderr leaked %q: %q", secret, stderr.String())
				}
			}
		})
	}
}

func TestExecuteReportsRuntimeFailureCategoriesWithoutSecrets(t *testing.T) {
	const token = "RUNTIME_TOKEN_SECRET"
	run := func(ctx context.Context, serverURL, format string, stdout io.Writer) (int, string) {
		var stderr bytes.Buffer
		code := Execute(ctx, []string{
			"export", "--project", "shop", "--env", "production", "--format", format,
		}, mapEnvironment(map[string]string{"CONFIGHUB_URL": serverURL, "CONFIGHUB_TOKEN": token}), stdout, &stderr)
		return code, stderr.String()
	}
	assertFailure := func(t *testing.T, code int, stdout *bytes.Buffer, stderr, want string, secrets ...string) {
		t.Helper()
		if code != 1 || stdout.Len() != 0 || stderr != want {
			t.Fatalf("exit=%d stdout=%q stderr=%q want=%q", code, stdout.String(), stderr, want)
		}
		for _, secret := range append(secrets, token) {
			if strings.Contains(stderr, secret) {
				t.Fatalf("stderr leaked %q: %q", secret, stderr)
			}
		}
	}

	t.Run("network transport", func(t *testing.T) {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		serverURL := "http://" + listener.Addr().String() + "/private-network-path"
		if err := listener.Close(); err != nil {
			t.Fatal(err)
		}
		var stdout bytes.Buffer
		code, stderr := run(context.Background(), serverURL, "json", &stdout)
		assertFailure(t, code, &stdout, stderr, "confighub: network request failed\n", "private-network-path")
	})

	t.Run("timeout", func(t *testing.T) {
		var attempts atomic.Int32
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			attempts.Add(1)
			_, _ = io.WriteString(w, `{"project":"shop","environment":"production","revision":1,"values":{}}`)
		}))
		defer server.Close()
		ctx, cancel := context.WithDeadline(context.Background(), time.Unix(0, 0))
		defer cancel()
		var stdout bytes.Buffer
		code, stderr := run(ctx, server.URL, "json", &stdout)
		assertFailure(t, code, &stdout, stderr, "confighub: request timed out\n")
		if attempts.Load() != 0 {
			t.Fatalf("expired context caused %d requests", attempts.Load())
		}
	})

	t.Run("canceled", func(t *testing.T) {
		var attempts atomic.Int32
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			attempts.Add(1)
			_, _ = io.WriteString(w, `{"project":"shop","environment":"production","revision":1,"values":{}}`)
		}))
		defer server.Close()
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		var stdout bytes.Buffer
		code, stderr := run(ctx, server.URL, "json", &stdout)
		assertFailure(t, code, &stdout, stderr, "confighub: request canceled\n")
		if attempts.Load() != 0 {
			t.Fatalf("canceled context caused %d requests", attempts.Load())
		}
	})

	t.Run("invalid response", func(t *testing.T) {
		const configSecret = "INVALID_RESPONSE_CONFIG_SECRET"
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, `{"project":"shop","environment":"production","revision":1,"values":{"KEY":"`+configSecret+`"}`)
		}))
		defer server.Close()
		var stdout bytes.Buffer
		code, stderr := run(context.Background(), server.URL+"/private-response-path", "json", &stdout)
		assertFailure(t, code, &stdout, stderr, "confighub: invalid server response\n", configSecret, "private-response-path")
	})

	t.Run("response too large", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, strings.Repeat("x", maxResponseBodyBytes)+"OVERSIZED_RESPONSE_SECRET")
		}))
		defer server.Close()
		var stdout bytes.Buffer
		code, stderr := run(context.Background(), server.URL, "json", &stdout)
		assertFailure(t, code, &stdout, stderr, "confighub: response too large\n", "OVERSIZED_RESPONSE_SECRET")
	})

	t.Run("response read", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Length", "1000")
			_, _ = io.WriteString(w, "TRUNCATED_RESPONSE_SECRET")
		}))
		defer server.Close()
		var stdout bytes.Buffer
		code, stderr := run(context.Background(), server.URL, "json", &stdout)
		assertFailure(t, code, &stdout, stderr, "confighub: response read failed\n", "TRUNCATED_RESPONSE_SECRET")
	})

	t.Run("export encoding", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, `{"project":"shop","environment":"production","revision":1,"values":{"KEY":"before\u0000after"}}`)
		}))
		defer server.Close()
		var stdout bytes.Buffer
		code, stderr := run(context.Background(), server.URL, "dotenv", &stdout)
		assertFailure(t, code, &stdout, stderr, "confighub: export encoding failed\n", "before", "after")
	})

	t.Run("stdout write", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, `{"project":"shop","environment":"production","revision":1,"values":{"KEY":"CONFIG_VALUE_SECRET"}}`)
		}))
		defer server.Close()
		var captured bytes.Buffer
		stdout := writerFunc(func([]byte) (int, error) {
			return 0, errors.New("STDOUT_WRITER_SECRET")
		})
		code, stderr := run(context.Background(), server.URL, "json", stdout)
		assertFailure(t, code, &captured, stderr, "confighub: stdout write failed\n", "STDOUT_WRITER_SECRET", "CONFIG_VALUE_SECRET")
	})
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
