package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestRunCommandMapsUsageConfigAndUnavailableBackupExitCodes(t *testing.T) {
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
	logs.Reset()
	output := filepath.Join(t.TempDir(), "private-backup.db")
	if code := runCommand(context.Background(), []string{"backup", "--config", configPath, "--output", output}, logs); code != 1 {
		t.Fatalf("unavailable backup exit=%d logs=%s", code, logs.String())
	}
	if !strings.Contains(logs.String(), "backup is not available") || strings.Contains(logs.String(), configPath) || strings.Contains(logs.String(), output) {
		t.Fatalf("unsafe or unclear backup error: %s", logs.String())
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
