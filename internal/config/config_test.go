package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
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
	if cfg.Auth.SessionTTL != 24*time.Hour {
		t.Fatalf("session TTL = %s", cfg.Auth.SessionTTL)
	}
	if cfg.Backup.Directory != filepath.Join(dir, "backups") {
		t.Fatalf("backup directory = %q", cfg.Backup.Directory)
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
	for _, ttl := range []string{"not-a-duration", "0s", "-1s"} {
		t.Run(ttl, func(t *testing.T) {
			dir := writeValidRuntimeFiles(t)
			writeRestricted(t, filepath.Join(dir, "config.yaml"), replace(t, validConfig, "  session_key_file: ./session.key\n", "  session_key_file: ./session.key\n  session_ttl: "+ttl+"\n"))

			_, err := Load(filepath.Join(dir, "config.yaml"))
			if !errors.Is(err, ErrConfigValidation) {
				t.Fatalf("error = %v, want ErrConfigValidation", err)
			}
		})
	}
}

func TestLoadRejectsMissingRequiredFiles(t *testing.T) {
	for _, file := range []string{"config.yaml", "users.yaml", "session.key"} {
		t.Run(file, func(t *testing.T) {
			dir := writeValidRuntimeFiles(t)
			if file != "config.yaml" {
				writeRestricted(t, filepath.Join(dir, "config.yaml"), validConfig)
			}
			if file == "users.yaml" {
				if err := os.Remove(filepath.Join(dir, file)); err != nil {
					t.Fatal(err)
				}
			}
			if file == "session.key" {
				if err := os.Remove(filepath.Join(dir, file)); err != nil {
					t.Fatal(err)
				}
			}

			_, err := Load(filepath.Join(dir, "config.yaml"))
			if !errors.Is(err, ErrConfigRead) {
				t.Fatalf("error = %v, want ErrConfigRead", err)
			}
		})
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
	for _, testCase := range []struct {
		name string
		mode os.FileMode
	}{
		{name: "config.yaml", mode: 0o640},
		{name: "users.yaml", mode: 0o640},
		{name: "session.key", mode: 0o604},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			dir := writeValidRuntimeFiles(t)
			writeRestricted(t, filepath.Join(dir, "config.yaml"), validConfig)
			if err := os.Chmod(filepath.Join(dir, testCase.name), testCase.mode); err != nil {
				t.Fatal(err)
			}

			_, err := Load(filepath.Join(dir, "config.yaml"))
			if !errors.Is(err, ErrFilePermissions) {
				t.Fatalf("error = %v, want ErrFilePermissions", err)
			}
		})
	}
}

func TestLoadRejectsMalformedEmptyAndMultiDocumentYAML(t *testing.T) {
	for _, testCase := range []struct {
		name    string
		content string
	}{
		{name: "malformed", content: "server: [\n"},
		{name: "empty", content: ""},
		{name: "multiple documents", content: validConfig + "---\n" + validConfig},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			dir := writeValidRuntimeFiles(t)
			writeRestricted(t, filepath.Join(dir, "config.yaml"), testCase.content)

			assertLoadError(t, filepath.Join(dir, "config.yaml"), ErrConfigValidation)
		})
	}
}

func TestLoadRejectsInvalidListen(t *testing.T) {
	for _, listen := range []string{":8080", "bad host:80", "localhost:+80", "localhost:0", "localhost:65536"} {
		t.Run(listen, func(t *testing.T) {
			dir := writeValidRuntimeFiles(t)
			writeRestricted(t, filepath.Join(dir, "config.yaml"), configWithListen(listen))

			assertLoadError(t, filepath.Join(dir, "config.yaml"), ErrConfigValidation)
		})
	}
}

func TestLoadAcceptsValidListen(t *testing.T) {
	for _, listen := range []string{"127.0.0.1:8080", "[::1]:8080", "localhost:443"} {
		t.Run(listen, func(t *testing.T) {
			dir := writeValidRuntimeFiles(t)
			writeRestricted(t, filepath.Join(dir, "config.yaml"), configWithListen(listen))

			if _, err := Load(filepath.Join(dir, "config.yaml")); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestLoadRejectsInvalidPublicURL(t *testing.T) {
	for _, publicURL := range []string{"relative/path", "https://:443", "https://example.com:", "https://example.com:+443", "https://example.com:0", "https://example.com:99999"} {
		t.Run(publicURL, func(t *testing.T) {
			dir := writeValidRuntimeFiles(t)
			writeRestricted(t, filepath.Join(dir, "config.yaml"), replace(t, validConfig, "https://config.example.com", publicURL))

			assertLoadError(t, filepath.Join(dir, "config.yaml"), ErrConfigValidation)
		})
	}
}

func TestLoadRejectsMissingRequiredPaths(t *testing.T) {
	for _, testCase := range []struct {
		name string
		line string
	}{
		{name: "database path", line: "  path: ./data/confighub.db\n"},
		{name: "users file", line: "  users_file: ./users.yaml\n"},
		{name: "session key file", line: "  session_key_file: ./session.key\n"},
		{name: "backup directory", line: "  directory: ./backups\n"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			dir := writeValidRuntimeFiles(t)
			writeRestricted(t, filepath.Join(dir, "config.yaml"), replace(t, validConfig, testCase.line, ""))

			assertLoadError(t, filepath.Join(dir, "config.yaml"), ErrConfigValidation)
		})
	}
}

func TestLoadRejectsNonRegularRestrictedFiles(t *testing.T) {
	for _, file := range []string{"config.yaml", "users.yaml", "session.key"} {
		t.Run(file, func(t *testing.T) {
			dir := writeValidRuntimeFiles(t)
			if file != "config.yaml" {
				writeRestricted(t, filepath.Join(dir, "config.yaml"), validConfig)
				if err := os.Remove(filepath.Join(dir, file)); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.Mkdir(filepath.Join(dir, file), 0o700); err != nil {
				t.Fatal(err)
			}

			assertLoadError(t, filepath.Join(dir, "config.yaml"), ErrConfigValidation)
		})
	}
}

func TestLoadPreservesAbsolutePaths(t *testing.T) {
	dir := t.TempDir()
	databasePath := filepath.Join(dir, "database", "confighub.db")
	usersPath := filepath.Join(dir, "credentials", "users.yaml")
	sessionKeyPath := filepath.Join(dir, "credentials", "session.key")
	backupPath := filepath.Join(dir, "backups")
	if err := os.Mkdir(filepath.Dir(usersPath), 0o700); err != nil {
		t.Fatal(err)
	}
	writeRestricted(t, usersPath, "users: []\n")
	writeRestricted(t, sessionKeyPath, "01234567890123456789012345678901\n")
	content := fmt.Sprintf(`server:
  public_url: https://config.example.com
database:
  path: %s
auth:
  users_file: %s
  session_key_file: %s
backup:
  directory: %s
`, databasePath, usersPath, sessionKeyPath, backupPath)
	configPath := filepath.Join(dir, "config.yaml")
	writeRestricted(t, configPath, content)

	cfg, err := Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Database.Path != databasePath || cfg.Auth.UsersFile != usersPath || cfg.Auth.SessionKeyFile != sessionKeyPath || cfg.Backup.Directory != backupPath {
		t.Fatalf("absolute paths were changed: %+v", cfg)
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

func configWithListen(listen string) string {
	return replaceString(validConfig, "  public_url: https://config.example.com\n", "  listen: "+strconv.Quote(listen)+"\n  public_url: https://config.example.com\n")
}

func assertLoadError(t *testing.T, path string, want error) {
	t.Helper()
	_, err := Load(path)
	if !errors.Is(err, want) {
		t.Fatalf("error = %v, want %v", err, want)
	}
}

func replaceString(value, old, new string) string {
	return strings.ReplaceAll(value, old, new)
}
