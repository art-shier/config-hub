package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const validConfig = `server:
  public_url: https://config.example.com
database:
  path: ./data/confighub.db
auth:
  users_file: ./users.yaml
  session_key_file: ./session.key
backup:
  directory: ./backups
`

func TestLoadResolvesPathsAndDefaults(t *testing.T) {
	dir := t.TempDir()
	writeRestricted(t, filepath.Join(dir, "users.yaml"), "users: []\n")
	writeRestricted(t, filepath.Join(dir, "session.key"), "01234567890123456789012345678901\n")
	writeRestricted(t, filepath.Join(dir, "config.yaml"), validConfig)

	cfg, err := Load(filepath.Join(dir, "config.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Server.Listen != "127.0.0.1:8080" {
		t.Fatalf("listen = %q", cfg.Server.Listen)
	}
	if cfg.Database.Path != filepath.Join(dir, "data", "confighub.db") {
		t.Fatalf("database path = %q", cfg.Database.Path)
	}
	if cfg.Auth.UsersFile != filepath.Join(dir, "users.yaml") {
		t.Fatalf("users file = %q", cfg.Auth.UsersFile)
	}
	if cfg.Auth.SessionKeyFile != filepath.Join(dir, "session.key") {
		t.Fatalf("session key file = %q", cfg.Auth.SessionKeyFile)
	}
}

func TestLoadRejectsUnknownYAMLKeys(t *testing.T) {
	dir := writeValidRuntimeFiles(t)
	writeRestricted(t, filepath.Join(dir, "config.yaml"), validConfig+"unexpected: value\n")

	_, err := Load(filepath.Join(dir, "config.yaml"))
	if !errors.Is(err, ErrConfigValidation) {
		t.Fatalf("error = %v, want ErrConfigValidation", err)
	}
}

func TestLoadRejectsNonHTTPSPublicURL(t *testing.T) {
	dir := writeValidRuntimeFiles(t)
	writeRestricted(t, filepath.Join(dir, "config.yaml"), replace(t, validConfig, "https://config.example.com", "http://config.example.com"))

	_, err := Load(filepath.Join(dir, "config.yaml"))
	if !errors.Is(err, ErrConfigValidation) {
		t.Fatalf("error = %v, want ErrConfigValidation", err)
	}
}

func TestLoadRejectsInvalidSessionTTL(t *testing.T) {
	dir := writeValidRuntimeFiles(t)
	writeRestricted(t, filepath.Join(dir, "config.yaml"), replace(t, validConfig, "  session_key_file: ./session.key\n", "  session_key_file: ./session.key\n  session_ttl: 0s\n"))

	_, err := Load(filepath.Join(dir, "config.yaml"))
	if !errors.Is(err, ErrConfigValidation) {
		t.Fatalf("error = %v, want ErrConfigValidation", err)
	}
}

func TestLoadRejectsMissingReferencedFile(t *testing.T) {
	dir := t.TempDir()
	writeRestricted(t, filepath.Join(dir, "config.yaml"), validConfig)

	_, err := Load(filepath.Join(dir, "config.yaml"))
	if !errors.Is(err, ErrConfigRead) {
		t.Fatalf("error = %v, want ErrConfigRead", err)
	}
}

func TestLoadRejectsInvalidTrustedProxyCIDR(t *testing.T) {
	dir := writeValidRuntimeFiles(t)
	writeRestricted(t, filepath.Join(dir, "config.yaml"), replace(t, validConfig, "  public_url: https://config.example.com\n", "  public_url: https://config.example.com\n  trusted_proxy_cidrs:\n    - not-a-cidr\n"))

	_, err := Load(filepath.Join(dir, "config.yaml"))
	if !errors.Is(err, ErrConfigValidation) {
		t.Fatalf("error = %v, want ErrConfigValidation", err)
	}
}

func TestLoadRejectsPermissionsBroaderThan0600(t *testing.T) {
	dir := writeValidRuntimeFiles(t)
	writeRestricted(t, filepath.Join(dir, "config.yaml"), validConfig)
	usersPath := filepath.Join(dir, "users.yaml")
	if err := os.Chmod(usersPath, 0o640); err != nil {
		t.Fatal(err)
	}

	_, err := Load(filepath.Join(dir, "config.yaml"))
	if !errors.Is(err, ErrFilePermissions) {
		t.Fatalf("error = %v, want ErrFilePermissions", err)
	}
}

func writeValidRuntimeFiles(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeRestricted(t, filepath.Join(dir, "users.yaml"), "users: []\n")
	writeRestricted(t, filepath.Join(dir, "session.key"), "01234567890123456789012345678901\n")
	return dir
}

func writeRestricted(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
}

func replace(t *testing.T, value, old, new string) string {
	t.Helper()
	return strings.ReplaceAll(value, old, new)
}
