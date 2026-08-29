package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"testing/synctest"
	"time"

	"confighub.local/internal/config"
	"confighub.local/internal/database"
)

func TestNewHTTPServerAppliesTimeouts(t *testing.T) {
	httpServer := newHTTPServer("127.0.0.1:8080", http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}), io.Discard)

	if httpServer.ReadHeaderTimeout != 5*time.Second {
		t.Errorf("ReadHeaderTimeout=%s, want 5s", httpServer.ReadHeaderTimeout)
	}
	if httpServer.ReadTimeout != 15*time.Second {
		t.Errorf("ReadTimeout=%s, want 15s", httpServer.ReadTimeout)
	}
	if httpServer.WriteTimeout != 30*time.Second {
		t.Errorf("WriteTimeout=%s, want 30s", httpServer.WriteTimeout)
	}
	if httpServer.IdleTimeout != time.Minute {
		t.Errorf("IdleTimeout=%s, want 1m", httpServer.IdleTimeout)
	}
}

func TestRunJoinAndCloseWaitsForLifecycleAndReloadLoop(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		releaseRun := make(chan struct{})
		joined := make(chan struct{})
		stopCalled := make(chan struct{})
		closeCalled := make(chan struct{})
		wantRunErr := errors.New("run failed")
		wantCloseErr := errors.New("close failed")
		results := make(chan [2]error, 1)
		done := make(chan struct{})
		go func() {
			runErr, closeErr := runJoinAndClose(
				func() error {
					<-releaseRun
					return wantRunErr
				},
				func() { close(stopCalled) },
				joined,
				func() error {
					close(closeCalled)
					return wantCloseErr
				},
			)
			results <- [2]error{runErr, closeErr}
			close(done)
		}()
		synctest.Wait()
		closedBeforeRun := channelIsClosed(closeCalled)

		close(releaseRun)
		synctest.Wait()
		stoppedAfterRun := channelIsClosed(stopCalled)
		closedBeforeJoin := channelIsClosed(closeCalled)
		returnedBeforeJoin := channelIsClosed(done)

		close(joined)
		<-done
		got := <-results
		if closedBeforeRun {
			t.Error("resource closed before lifecycle Run returned")
		}
		if !stoppedAfterRun {
			t.Error("reload stop callback was not invoked after lifecycle Run returned")
		}
		if closedBeforeJoin || returnedBeforeJoin {
			t.Error("resource closed or helper returned before reload loop joined")
		}
		if !channelIsClosed(closeCalled) {
			t.Error("resource was not closed after lifecycle and reload loop completed")
		}
		if !errors.Is(got[0], wantRunErr) || !errors.Is(got[1], wantCloseErr) {
			t.Errorf("errors=(%v, %v), want (%v, %v)", got[0], got[1], wantRunErr, wantCloseErr)
		}
	})
}

func TestRunCommandMapsUsageConfigAndBackupExitCodes(t *testing.T) {
	if code := runCommand(context.Background(), nil, new(bytes.Buffer)); code != 2 {
		t.Fatalf("empty command exit=%d", code)
	}
	logs := new(bytes.Buffer)
	secretPath := "/private/config/users-password-token.yaml"
	if code := runCommand(context.Background(), []string{"serve", "--config", secretPath}, logs); code != 2 {
		t.Fatalf("config error exit=%d logs=%s", code, logs.String())
	}
	if strings.Contains(logs.String(), secretPath) || strings.Contains(logs.String(), "password") || strings.Contains(logs.String(), "token") {
		t.Fatalf("config error leaked sensitive path: %s", logs.String())
	}

	configPath := writeCommandConfig(t, "users: []\n")
	source, err := database.Open(filepath.Join(filepath.Dir(configPath), "data", "confighub.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := source.DB().Exec(`INSERT INTO machine_identities
		(id, name, enabled, created_at, updated_at)
		VALUES ('backup-command', 'backup command', 1, 1, 1)`); err != nil {
		t.Fatal(err)
	}
	if err := source.Close(); err != nil {
		t.Fatal(err)
	}
	logs.Reset()
	output := filepath.Join(t.TempDir(), "private-backup.db")
	if code := runCommand(context.Background(), []string{"backup", "--config", configPath, "--output", output}, logs); code != 0 {
		t.Fatalf("backup exit=%d logs=%s", code, logs.String())
	}
	backup, err := database.OpenReadOnly(output)
	if err != nil {
		t.Fatal(err)
	}
	defer backup.Close()
	var count int
	if err := backup.QueryRow(`SELECT count(*) FROM machine_identities WHERE id = 'backup-command'`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("backup row count=%d err=%v", count, err)
	}
	if logs.Len() != 0 {
		t.Fatalf("successful backup wrote unexpected logs: %s", logs.String())
	}

	logs.Reset()
	if code := runCommand(context.Background(), []string{"backup", "--config", configPath, "--output", output}, logs); code != 1 {
		t.Fatalf("existing backup exit=%d logs=%s", code, logs.String())
	}
	if !strings.Contains(logs.String(), "backup failed") || strings.Contains(logs.String(), configPath) || strings.Contains(logs.String(), output) {
		t.Fatalf("unsafe or unclear backup failure: %s", logs.String())
	}
}

func TestBackupCommandParsesAndDelegatesValidatedConfiguration(t *testing.T) {
	configPath := writeCommandConfig(t, "users: []\n")
	output := filepath.Join(t.TempDir(), "delegated.db")
	original := runBackup
	defer func() { runBackup = original }()
	called := false
	runBackup = func(ctx context.Context, cfg config.Config, gotOutput string) error {
		called = true
		if ctx == nil {
			t.Fatal("nil context")
		}
		if cfg.Database.Path != filepath.Join(filepath.Dir(configPath), "data", "confighub.db") {
			t.Fatalf("database path=%q", cfg.Database.Path)
		}
		if gotOutput != output {
			t.Fatalf("output=%q want %q", gotOutput, output)
		}
		return nil
	}
	if code := runCommand(context.Background(), []string{"backup", "--output", output, "--config", configPath}, io.Discard); code != 0 {
		t.Fatalf("exit=%d", code)
	}
	if !called {
		t.Fatal("backup operation was not called")
	}
}

func TestBackupCommandRejectsIncompleteAndUnexpectedArguments(t *testing.T) {
	for _, args := range [][]string{
		{"backup"},
		{"backup", "--config", "config.yaml"},
		{"backup", "--output", "backup.db"},
		{"backup", "--config", "config.yaml", "--output", "backup.db", "extra"},
		{"backup", "--unknown"},
	} {
		logs := new(bytes.Buffer)
		if code := runCommand(context.Background(), args, logs); code != 2 {
			t.Errorf("args=%q exit=%d logs=%s", args, code, logs.String())
		}
		if !strings.Contains(logs.String(), "confighub-server backup") {
			t.Errorf("args=%q missing usage: %s", args, logs.String())
		}
	}
}

func TestServeInitialUserSyncFailureIsRedactedRuntimeError(t *testing.T) {
	secret := "DO_NOT_LOG_THIS_PASSWORD_TOKEN_VALUE"
	users := "users:\n  - username: admin\n    display_name: Admin\n    password: " + secret + "\n    role: admin\n    enabled: true\n    unexpected: value\n"
	configPath := writeCommandConfig(t, users)
	logs := new(bytes.Buffer)

	if code := runCommand(context.Background(), []string{"serve", "--config", configPath}, logs); code != 1 {
		t.Fatalf("initial sync failure exit=%d logs=%s", code, logs.String())
	}
	if strings.Contains(logs.String(), secret) || strings.Contains(logs.String(), configPath) || strings.Contains(logs.String(), "users.yaml") {
		t.Fatalf("initial sync error leaked sensitive material: %s", logs.String())
	}
}

func TestServeMapsRouterOriginValidationToConfigExitCode(t *testing.T) {
	configPath := writeCommandConfigWithPublicURL(t, "users: []\n", "https://config.example.com/unexpected-path")
	logs := new(bytes.Buffer)

	if code := runCommand(context.Background(), []string{"serve", "--config", configPath}, logs); code != 2 {
		t.Fatalf("router config failure exit=%d logs=%s", code, logs.String())
	}
	if !strings.Contains(logs.String(), "invalid configuration") || strings.Contains(logs.String(), configPath) {
		t.Fatalf("unsafe or unclear config error: %s", logs.String())
	}
}

func TestReloadSignalLoopCanBeJoinedBeforeResourceCleanup(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	signals := make(chan os.Signal, 1)
	started := make(chan struct{})
	release := make(chan struct{})
	done := make(chan struct{})
	go func() {
		reloadOnSignals(ctx, signals, func(context.Context) error {
			close(started)
			<-release
			return nil
		})
		close(done)
	}()

	signals <- syscall.SIGHUP
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("reload did not start")
	}
	cancel()
	select {
	case <-done:
		t.Fatal("reload loop exited before its in-flight reload completed")
	default:
	}
	close(release)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("reload loop could not be joined")
	}
}

func writeCommandConfig(t *testing.T, users string) string {
	return writeCommandConfigWithPublicURL(t, users, "https://config.example.com")
}

func channelIsClosed(channel <-chan struct{}) bool {
	select {
	case <-channel:
		return true
	default:
		return false
	}
}

func writeCommandConfigWithPublicURL(t *testing.T, users, publicURL string) string {
	t.Helper()
	dir := t.TempDir()
	writeCommandFile(t, filepath.Join(dir, "users.yaml"), users)
	writeCommandFile(t, filepath.Join(dir, "session.key"), "01234567890123456789012345678901\n")
	config := "server:\n  listen: 127.0.0.1:8080\n  public_url: " + publicURL + "\n" +
		"database:\n  path: ./data/confighub.db\n" +
		"auth:\n  users_file: ./users.yaml\n  session_key_file: ./session.key\n  session_ttl: 1h\n" +
		"backup:\n  directory: ./backups\n"
	path := filepath.Join(dir, "config.yaml")
	writeCommandFile(t, path, config)
	return path
}

func writeCommandFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
}
