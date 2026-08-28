package cli

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

func TestExecuteRunPassesArgumentsAndFetchesSelectedConfiguration(t *testing.T) {
	const token = "run-token"
	var gotAuthorization, gotService string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		gotAuthorization = request.Header.Get("Authorization")
		gotService = request.URL.Query().Get("service")
		_, _ = io.WriteString(w, `{"project":"shop","environment":"production","revision":4,"values":{"REMOTE_ONLY":"yes"}}`)
	}))
	defer server.Close()
	var stdout, stderr bytes.Buffer
	code := Execute(context.Background(), []string{
		"run", "--server", server.URL, "--token-file", writeTokenFile(t, "run-token", token+"\n", 0o600),
		"--project", "shop", "--env", "production", "--service", "api worker", "--",
		helperBinary(t), "args", "first argument", "--child-flag", "value",
	}, mapEnvironment(map[string]string{}), &stdout, &stderr)
	if code != 0 || stderr.Len() != 0 {
		t.Fatalf("exit=%d stderr=%q", code, stderr.String())
	}
	if got := stdout.String(); got != "first argument\n--child-flag\nvalue\n" {
		t.Fatalf("stdout=%q", got)
	}
	if gotAuthorization != "Bearer "+token || gotService != "api worker" {
		t.Fatalf("Authorization=%q service=%q", gotAuthorization, gotService)
	}
}

func TestExecuteRunReturnsExactChildExitCodeWithoutDiagnostic(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"project":"shop","environment":"production","revision":1,"values":{}}`)
	}))
	defer server.Close()
	var stdout, stderr bytes.Buffer
	code := Execute(context.Background(), []string{
		"run", "--project", "shop", "--env", "production", "--", helperBinary(t), "exit", "37",
	}, mapEnvironment(map[string]string{"CONFIGHUB_URL": server.URL, "CONFIGHUB_TOKEN": "token"}), &stdout, &stderr)
	if code != 37 || stdout.Len() != 0 || stderr.Len() != 0 {
		t.Fatalf("exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestExecuteRunRequiresCommandBoundaryBeforeFetching(t *testing.T) {
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		attempts.Add(1)
		_, _ = io.WriteString(w, `{"project":"shop","environment":"production","revision":1,"values":{}}`)
	}))
	defer server.Close()
	helper := helperBinary(t)
	for _, test := range []struct {
		name string
		args []string
	}{
		{
			name: "missing boundary",
			args: []string{"run", "--project", "shop", "--env", "production", helper, "args"},
		},
		{
			name: "missing command after boundary",
			args: []string{"run", "--project", "shop", "--env", "production", "--"},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := Execute(context.Background(), test.args, mapEnvironment(map[string]string{
				"CONFIGHUB_URL": server.URL, "CONFIGHUB_TOKEN": "token",
			}), &stdout, &stderr)
			if code != 2 || stdout.Len() != 0 || stderr.String() != "confighub: invalid command usage\n" {
				t.Fatalf("exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
			}
		})
	}
	if got := attempts.Load(); got != 0 {
		t.Fatalf("invalid command boundaries caused %d fetches", got)
	}
}

func TestExecuteRunReportsRunFailureWithoutSensitiveOutput(t *testing.T) {
	t.Run("invalid remote environment", func(t *testing.T) {
		const secret = "REMOTE_CONFIG_SECRET"
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, `{"project":"shop","environment":"production","revision":1,"values":{"KEY":"`+secret+`\u0000"}}`)
		}))
		defer server.Close()
		marker := filepath.Join(t.TempDir(), "child-started")
		var stdout, stderr bytes.Buffer
		code := Execute(context.Background(), []string{
			"run", "--project", "shop", "--env", "production", "--", helperBinary(t), "touch", marker,
		}, mapEnvironment(map[string]string{"CONFIGHUB_URL": server.URL, "CONFIGHUB_TOKEN": "token"}), &stdout, &stderr)
		if code != 1 || stdout.Len() != 0 || stderr.String() != "confighub: run failed\n" {
			t.Fatalf("exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
		}
		if strings.Contains(stderr.String(), secret) {
			t.Fatalf("stderr leaked configuration: %q", stderr.String())
		}
		if _, statErr := os.Stat(marker); !errors.Is(statErr, os.ErrNotExist) {
			t.Fatalf("child marker exists or could not be checked: %v", statErr)
		}
	})

	t.Run("start failure", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, `{"project":"shop","environment":"production","revision":1,"values":{}}`)
		}))
		defer server.Close()
		commandPath := filepath.Join(t.TempDir(), "sensitive-command-path")
		var stdout, stderr bytes.Buffer
		code := Execute(context.Background(), []string{
			"run", "--project", "shop", "--env", "production", "--", commandPath,
		}, mapEnvironment(map[string]string{"CONFIGHUB_URL": server.URL, "CONFIGHUB_TOKEN": "token"}), &stdout, &stderr)
		if code != 1 || stdout.Len() != 0 || stderr.String() != "confighub: run failed\n" {
			t.Fatalf("exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
		}
		if strings.Contains(stderr.String(), commandPath) || strings.Contains(stderr.String(), "sensitive-command-path") {
			t.Fatalf("stderr leaked command path: %q", stderr.String())
		}
	})
}

func TestExecuteRunDoesNotStartChildWhenFetchFails(t *testing.T) {
	const responseSecret = "RUN_RESPONSE_SECRET"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, `{"error":{"code":"internal_error","message":"`+responseSecret+`","request_id":"req-run","fields":{}}}`)
	}))
	defer server.Close()
	marker := filepath.Join(t.TempDir(), "child-started")
	var stdout, stderr bytes.Buffer
	code := Execute(context.Background(), []string{
		"run", "--project", "shop", "--env", "production", "--", helperBinary(t), "touch", marker,
	}, mapEnvironment(map[string]string{"CONFIGHUB_URL": server.URL, "CONFIGHUB_TOKEN": "token"}), &stdout, &stderr)
	if code != 1 || stdout.Len() != 0 || stderr.String() != "confighub: API request failed: status 500, code internal_error, request_id req-run\n" {
		t.Fatalf("exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if strings.Contains(stderr.String(), responseSecret) {
		t.Fatalf("stderr leaked response: %q", stderr.String())
	}
	if _, statErr := os.Stat(marker); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("child marker exists or could not be checked: %v", statErr)
	}
}

func TestExecuteRunReportsChildCancellationAsRunOperation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"project":"shop","environment":"production","revision":1,"values":{}}`)
	}))
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stdout, childOutput, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer stdout.Close()
	defer childOutput.Close()
	var stderr bytes.Buffer
	finished := make(chan int, 1)
	helper := helperBinary(t)
	go func() {
		finished <- Execute(ctx, []string{
			"run", "--project", "shop", "--env", "production", "--", helper, "wait-signal", "23",
		}, mapEnvironment(map[string]string{"CONFIGHUB_URL": server.URL, "CONFIGHUB_TOKEN": "token"}), childOutput, &stderr)
	}()

	ready := make(chan string, 1)
	go func() {
		scanner := bufio.NewScanner(stdout)
		if scanner.Scan() {
			ready <- scanner.Text()
			return
		}
		ready <- ""
	}()
	select {
	case line := <-ready:
		if !strings.HasPrefix(line, "ready ") {
			t.Fatalf("readiness=%q", line)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for child readiness")
	}
	cancel()
	select {
	case code := <-finished:
		if code != 1 || stderr.String() != "confighub: run canceled\n" {
			t.Fatalf("exit=%d stderr=%q", code, stderr.String())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Execute did not return after cancellation")
	}
}

func TestExecuteRunRejectsInvalidSelectionBeforeRequest(t *testing.T) {
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		attempts.Add(1)
	}))
	defer server.Close()
	for _, args := range [][]string{
		{"run", "--project", "../shop", "--env", "production", "--", "unused"},
		{"run", "--project", "shop", "--env", "Production", "--", "unused"},
		{"run", "--project", "shop", "--env", "production", "--service", strings.Repeat("s", 129), "--", "unused"},
	} {
		var stdout, stderr bytes.Buffer
		code := Execute(context.Background(), args, mapEnvironment(map[string]string{
			"CONFIGHUB_URL": server.URL, "CONFIGHUB_TOKEN": "token",
		}), &stdout, &stderr)
		if code != 2 || stdout.Len() != 0 || stderr.String() != "confighub: invalid command usage\n" {
			t.Fatalf("args=%v exit=%d stdout=%q stderr=%q", args, code, stdout.String(), stderr.String())
		}
	}
	if got := attempts.Load(); got != 0 {
		t.Fatalf("invalid selections caused %d requests", got)
	}
}

func TestRunCommandHelpAndFlagsPreserveCredentialBoundary(t *testing.T) {
	command := newRootCommand(context.Background(), os.Getenv, io.Discard, io.Discard)
	run, _, err := command.Find([]string{"run"})
	if err != nil {
		t.Fatal(err)
	}
	if run.Flags().Lookup("project") == nil || run.Flags().Lookup("env") == nil || run.Flags().Lookup("service") == nil {
		t.Fatal("run selection flags are incomplete")
	}
	if run.Flags().Lookup("token") != nil || run.PersistentFlags().Lookup("token") != nil {
		t.Fatal("run defines a plaintext --token flag")
	}

	var stdout, stderr bytes.Buffer
	code := Execute(context.Background(), []string{"run", "--help"}, os.Getenv, &stdout, &stderr)
	if code != 0 || stdout.Len() != 0 {
		t.Fatalf("exit=%d stdout=%q", code, stdout.String())
	}
	for _, expected := range []string{"--project", "--env", "--service", "--token-file", "-- command"} {
		if !strings.Contains(stderr.String(), expected) {
			t.Fatalf("help does not contain %q: %q", expected, stderr.String())
		}
	}
}

func TestRunRejectsMissingFetch(t *testing.T) {
	code, err := (Runner{}).Run(context.Background(), []string{"unused"}, nil, io.Discard, io.Discard)
	if err == nil || code == 0 {
		t.Fatalf("code=%d err=%v", code, err)
	}
}

func TestRunRejectsEmptyCommandBeforeFetching(t *testing.T) {
	fetched := false
	runner := Runner{Fetch: func(context.Context) (map[string]string, error) {
		fetched = true
		return map[string]string{}, nil
	}}
	code, err := runner.Run(context.Background(), nil, nil, io.Discard, io.Discard)
	if err == nil || code == 0 {
		t.Fatalf("code=%d err=%v", code, err)
	}
	if fetched {
		t.Fatal("configuration fetched for an empty command")
	}
}

func TestRunRemoteValuesOverrideParent(t *testing.T) {
	runner := Runner{Fetch: func(context.Context) (map[string]string, error) {
		return map[string]string{"PORT": "9090", "REMOTE_ONLY": "yes"}, nil
	}}
	var out bytes.Buffer
	code, err := runner.Run(context.Background(), []string{helperBinary(t)}, []string{"PORT=8080", "LOCAL_ONLY=yes"}, &out, io.Discard)
	if err != nil || code != 0 {
		t.Fatalf("code=%d err=%v", code, err)
	}
	if got := out.String(); got != "LOCAL_ONLY=yes\nPORT=9090\nREMOTE_ONLY=yes\n" {
		t.Fatalf("output=%q", got)
	}
}

func TestRunLeavesUnrelatedParentVariablesUntouched(t *testing.T) {
	runner := Runner{Fetch: func(context.Context) (map[string]string, error) {
		return map[string]string{"REMOTE_ONLY": "yes"}, nil
	}}
	var out bytes.Buffer
	code, err := runner.Run(context.Background(), []string{helperBinary(t)}, []string{"PATH=/usr/bin", "LOCAL_ONLY=left=alone"}, &out, io.Discard)
	if err != nil || code != 0 {
		t.Fatalf("code=%d err=%v", code, err)
	}
	if got := out.String(); got != "LOCAL_ONLY=left=alone\nPATH=/usr/bin\nREMOTE_ONLY=yes\n" {
		t.Fatalf("output=%q", got)
	}
}

func TestRunUsesLastDuplicateParentValueBeforeRemoteOverride(t *testing.T) {
	runner := Runner{Fetch: func(context.Context) (map[string]string, error) {
		return map[string]string{"REMOTE": "remote"}, nil
	}}
	var out bytes.Buffer
	code, err := runner.Run(context.Background(), []string{helperBinary(t)}, []string{
		"DUPLICATE=first", "REMOTE=parent", "DUPLICATE=second",
	}, &out, io.Discard)
	if err != nil || code != 0 {
		t.Fatalf("code=%d err=%v", code, err)
	}
	if got := out.String(); got != "DUPLICATE=second\nREMOTE=remote\n" {
		t.Fatalf("output=%q", got)
	}
}

func TestRunDoesNotMutateParentProcessEnvironment(t *testing.T) {
	const key = "CONFIGHUB_RUN_PROCESS_SCOPE"
	t.Setenv(key, "parent")
	runner := Runner{Fetch: func(context.Context) (map[string]string, error) {
		return map[string]string{key: "child"}, nil
	}}
	var out bytes.Buffer
	code, err := runner.Run(context.Background(), []string{helperBinary(t)}, []string{key + "=parent"}, &out, io.Discard)
	if err != nil || code != 0 {
		t.Fatalf("code=%d err=%v", code, err)
	}
	if got := out.String(); got != key+"=child\n" {
		t.Fatalf("child output=%q", got)
	}
	if got := os.Getenv(key); got != "parent" {
		t.Fatalf("parent value=%q", got)
	}
}

func TestRunDoesNotStartChildWhenFetchFails(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "child-started")
	runner := Runner{Fetch: func(context.Context) (map[string]string, error) {
		return map[string]string{"STALE": "value"}, errors.New("fetch failed")
	}}
	code, err := runner.Run(context.Background(), []string{helperBinary(t), "touch", marker}, os.Environ(), io.Discard, io.Discard)
	if err == nil || code == 0 {
		t.Fatalf("code=%d err=%v", code, err)
	}
	if _, statErr := os.Stat(marker); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("child marker exists or could not be checked: %v", statErr)
	}
}

func TestRunRejectsInvalidRemoteEnvironmentBeforeStartingChild(t *testing.T) {
	for _, test := range []struct {
		name   string
		values map[string]string
	}{
		{name: "invalid key", values: map[string]string{"BAD-NAME": "secret"}},
		{name: "NUL in value", values: map[string]string{"VALID_KEY": "before\x00after"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			marker := filepath.Join(t.TempDir(), "child-started")
			runner := Runner{Fetch: func(context.Context) (map[string]string, error) {
				return test.values, nil
			}}
			code, err := runner.Run(context.Background(), []string{helperBinary(t), "touch", marker}, os.Environ(), io.Discard, io.Discard)
			if err == nil || code == 0 || err.Error() != "invalid child environment" {
				t.Fatalf("code=%d err=%v", code, err)
			}
			if _, statErr := os.Stat(marker); !errors.Is(statErr, os.ErrNotExist) {
				t.Fatalf("child marker exists or could not be checked: %v", statErr)
			}
		})
	}
}

func TestRunRejectsMalformedParentEnvironmentBeforeStartingChild(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "child-started")
	fetched := false
	runner := Runner{Fetch: func(context.Context) (map[string]string, error) {
		fetched = true
		return map[string]string{}, nil
	}}
	code, err := runner.Run(context.Background(), []string{helperBinary(t), "touch", marker}, []string{"MALFORMED"}, io.Discard, io.Discard)
	if !fetched {
		t.Fatal("configuration was not fetched before environment validation")
	}
	if err == nil || code == 0 || err.Error() != "invalid parent environment" {
		t.Fatalf("code=%d err=%v", code, err)
	}
	if _, statErr := os.Stat(marker); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("child marker exists or could not be checked: %v", statErr)
	}
}

func TestRunReturnsExactChildExitCode(t *testing.T) {
	runner := Runner{Fetch: func(context.Context) (map[string]string, error) {
		return map[string]string{}, nil
	}}
	helper := helperBinary(t)
	code, err := runner.Run(context.Background(), []string{helper, "exit", "37"}, os.Environ(), io.Discard, io.Discard)
	if err != nil || code != 37 {
		t.Fatalf("code=%d err=%v", code, err)
	}
}

func TestRunReturnsConventionalExitCodeWhenChildIsSignaled(t *testing.T) {
	runner := Runner{Fetch: func(context.Context) (map[string]string, error) {
		return map[string]string{}, nil
	}}
	code, err := runner.Run(context.Background(), []string{helperBinary(t), "signal-self"}, os.Environ(), io.Discard, io.Discard)
	if err != nil || code != 128+int(syscall.SIGTERM) {
		t.Fatalf("code=%d err=%v", code, err)
	}
}

func TestRunCancellationTerminatesChildProcessGroup(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stdout, childOutput, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer stdout.Close()
	defer childOutput.Close()
	runner := Runner{Fetch: func(context.Context) (map[string]string, error) {
		return map[string]string{}, nil
	}}
	helper := helperBinary(t)
	type result struct {
		code int
		err  error
	}
	finished := make(chan result, 1)
	go func() {
		code, err := runner.Run(ctx, []string{helper, "wait-group"}, os.Environ(), childOutput, io.Discard)
		finished <- result{code: code, err: err}
	}()

	scanner := bufio.NewScanner(stdout)
	ready := make(chan string, 1)
	go func() {
		if scanner.Scan() {
			ready <- scanner.Text()
			return
		}
		ready <- ""
	}()
	var childGroup int
	select {
	case line := <-ready:
		fields := strings.Fields(line)
		if len(fields) != 3 || fields[0] != "ready" {
			t.Fatalf("readiness=%q", line)
		}
		grandchildPID, parseErr := strconv.Atoi(fields[1])
		if parseErr != nil {
			t.Fatal(parseErr)
		}
		childGroup, parseErr = strconv.Atoi(fields[2])
		if parseErr != nil || childGroup <= 0 || childGroup == grandchildPID {
			t.Fatalf("PID/group in %q: %v", line, parseErr)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for grandchild readiness")
	}
	defer func() { _ = syscall.Kill(-childGroup, syscall.SIGKILL) }()

	cancel()
	select {
	case got := <-finished:
		if got.code != 1 || !errors.Is(got.err, context.Canceled) {
			t.Fatalf("code=%d err=%v", got.code, got.err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Runner did not return after cancellation")
	}
	deadline := time.Now().Add(time.Second)
	for {
		err := syscall.Kill(-childGroup, 0)
		if errors.Is(err, syscall.ESRCH) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("child process group %d remained after cancellation: %v", childGroup, err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestRunForwardsSIGTERMToChildProcessGroup(t *testing.T) {
	testRunForwardsSignalToChildProcessGroup(t, syscall.SIGTERM, "terminated")
}

func TestRunForwardsSIGINTToChildProcessGroup(t *testing.T) {
	testRunForwardsSignalToChildProcessGroup(t, syscall.SIGINT, "interrupt")
}

func TestRunRemovesSignalSubscriptionAfterChildExit(t *testing.T) {
	command := exec.Command(os.Args[0], "-test.run=^TestRunSignalCleanupIsolatedProcess$")
	command.Env = append(os.Environ(),
		"CONFIGHUB_RUN_SIGNAL_CLEANUP_TEST=1",
		"CONFIGHUB_RUN_HELPER="+helperBinary(t),
	)
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	command.Stderr = &stderr
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	wait := make(chan error, 1)
	go func() { wait <- command.Wait() }()
	waited := false
	defer func() {
		if waited {
			return
		}
		_ = syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
		<-wait
	}()

	ready := make(chan string, 1)
	go func() {
		scanner := bufio.NewScanner(stdout)
		if scanner.Scan() {
			ready <- scanner.Text()
			return
		}
		ready <- ""
	}()
	select {
	case line := <-ready:
		if line != "runner-finished" {
			t.Fatalf("readiness=%q stderr=%q", line, stderr.String())
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for runner completion; stderr=%q", stderr.String())
	}
	if err := command.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-wait:
		waited = true
		var exitError *exec.ExitError
		if !errors.As(err, &exitError) {
			t.Fatalf("isolated process error=%v stderr=%q", err, stderr.String())
		}
		status, ok := exitError.Sys().(syscall.WaitStatus)
		if !ok || !status.Signaled() || status.Signal() != syscall.SIGTERM {
			t.Fatalf("isolated process status=%v stderr=%q", exitError.Sys(), stderr.String())
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("signal subscription remained active; stderr=%q", stderr.String())
	}
}

func testRunForwardsSignalToChildProcessGroup(t *testing.T, forwarded syscall.Signal, signalName string) {
	t.Helper()
	helper := helperBinary(t)
	command := exec.Command(os.Args[0], "-test.run=^TestRunSignalIsolatedProcess$")
	command.Env = append(os.Environ(),
		"CONFIGHUB_RUN_SIGNAL_TEST=1",
		"CONFIGHUB_RUN_HELPER="+helper,
		"CONFIGHUB_RUN_SIGNAL="+strconv.Itoa(int(forwarded)),
	)
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	command.Stderr = &stderr
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	wait := make(chan error, 1)
	go func() { wait <- command.Wait() }()
	waited := false
	var childPID int
	defer func() {
		if waited {
			return
		}
		_ = syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
		if childPID > 0 {
			_ = syscall.Kill(-childPID, syscall.SIGKILL)
		}
		<-wait
	}()

	lines := make(chan string, 8)
	scanDone := make(chan error, 1)
	go func() {
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			lines <- scanner.Text()
		}
		close(lines)
		scanDone <- scanner.Err()
	}()

	select {
	case line := <-lines:
		fields := strings.Fields(line)
		if len(fields) != 3 || fields[0] != "ready" {
			t.Fatalf("first output line=%q stderr=%q", line, stderr.String())
		}
		childPID, err = strconv.Atoi(fields[1])
		if err != nil || childPID <= 0 {
			t.Fatalf("child PID in %q: %v", line, err)
		}
		childGroup, groupErr := strconv.Atoi(fields[2])
		if groupErr != nil || childGroup != childPID {
			t.Fatalf("child PID/group in %q: %v", line, groupErr)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for child readiness; stderr=%q", stderr.String())
	}

	if err := command.Process.Signal(forwarded); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-wait:
		waited = true
		if err != nil {
			t.Fatalf("isolated runner failed: %v stderr=%q", err, stderr.String())
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for isolated runner; stderr=%q", stderr.String())
	}
	if err := <-scanDone; err != nil {
		t.Fatal(err)
	}
	var remaining []string
	for line := range lines {
		remaining = append(remaining, line)
	}
	if got := strings.Join(remaining, "\n"); !strings.Contains(got, signalName) {
		t.Fatalf("output after readiness=%q", got)
	}
}

func TestRunSignalIsolatedProcess(t *testing.T) {
	if os.Getenv("CONFIGHUB_RUN_SIGNAL_TEST") != "1" {
		return
	}
	forwarded, err := strconv.Atoi(os.Getenv("CONFIGHUB_RUN_SIGNAL"))
	if err != nil {
		t.Fatal(err)
	}
	runner := Runner{Fetch: func(context.Context) (map[string]string, error) {
		return map[string]string{}, nil
	}}
	code, err := runner.Run(context.Background(), []string{os.Getenv("CONFIGHUB_RUN_HELPER"), "wait-signal", "23"}, os.Environ(), os.Stdout, os.Stderr)
	if err != nil || code != 23 {
		t.Fatalf("signal=%d code=%d err=%v", forwarded, code, err)
	}
}

func TestRunSignalCleanupIsolatedProcess(t *testing.T) {
	if os.Getenv("CONFIGHUB_RUN_SIGNAL_CLEANUP_TEST") != "1" {
		return
	}
	runner := Runner{Fetch: func(context.Context) (map[string]string, error) {
		return map[string]string{}, nil
	}}
	code, err := runner.Run(context.Background(), []string{os.Getenv("CONFIGHUB_RUN_HELPER"), "exit", "0"}, os.Environ(), os.Stdout, os.Stderr)
	if err != nil || code != 0 {
		t.Fatalf("code=%d err=%v", code, err)
	}
	fmt.Println("runner-finished")
	select {}
}

func helperBinary(t *testing.T) string {
	t.Helper()
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("could not locate test source")
	}
	binary := filepath.Join(t.TempDir(), "printenv")
	command := exec.Command("go", "build", "-o", binary, filepath.Join(filepath.Dir(currentFile), "testdata", "printenv.go"))
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build helper: %v\n%s", err, output)
	}
	return binary
}
