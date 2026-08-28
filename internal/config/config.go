// Package config loads and validates ConfigHub's runtime configuration.
package config

import (
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

var (
	// ErrConfigRead identifies failures reading a required configuration file.
	ErrConfigRead = errors.New("configuration read error")
	// ErrConfigValidation identifies invalid configuration syntax or values.
	ErrConfigValidation = errors.New("configuration validation error")
	// ErrFilePermissions identifies a configuration file with unsafe permissions.
	ErrFilePermissions = errors.New("unsafe configuration file permissions")
)

const (
	defaultListen     = "127.0.0.1:8080"
	defaultSessionTTL = 24 * time.Hour
)

type Config struct {
	Server   ServerConfig   `yaml:"server"`
	Database DatabaseConfig `yaml:"database"`
	Auth     AuthConfig     `yaml:"auth"`
	Backup   BackupConfig   `yaml:"backup"`
}

type ServerConfig struct {
	Listen            string   `yaml:"listen"`
	PublicURL         string   `yaml:"public_url"`
	TrustedProxyCIDRs []string `yaml:"trusted_proxy_cidrs"`
}

type DatabaseConfig struct {
	Path string `yaml:"path"`
}

type AuthConfig struct {
	UsersFile      string        `yaml:"users_file"`
	SessionKeyFile string        `yaml:"session_key_file"`
	SessionTTL     time.Duration `yaml:"-"`
	SessionTTLText string        `yaml:"session_ttl"`
}

type BackupConfig struct {
	Directory string `yaml:"directory"`
}

// Load reads configuration from path. Relative runtime paths are resolved
// relative to the configuration file's directory.
func Load(path string) (Config, error) {
	configPath, err := filepath.Abs(path)
	if err != nil {
		return Config{}, fmt.Errorf("%w: resolve config path: %v", ErrConfigRead, err)
	}
	if err := checkRestrictedFile(configPath); err != nil {
		return Config{}, err
	}

	file, err := os.Open(configPath)
	if err != nil {
		return Config{}, fmt.Errorf("%w: open config file: %v", ErrConfigRead, err)
	}
	defer file.Close()

	var cfg Config
	decoder := yaml.NewDecoder(file)
	decoder.KnownFields(true)
	if err := decoder.Decode(&cfg); err != nil {
		return Config{}, fmt.Errorf("%w: decode config file: %v", ErrConfigValidation, err)
	}
	var extra yaml.Node
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return Config{}, fmt.Errorf("%w: config file must contain one document", ErrConfigValidation)
		}
		return Config{}, fmt.Errorf("%w: decode config file: %v", ErrConfigValidation, err)
	}

	if err := applyDefaultsAndValidate(&cfg, filepath.Dir(configPath)); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func applyDefaultsAndValidate(cfg *Config, configDir string) error {
	if cfg.Server.Listen == "" {
		cfg.Server.Listen = defaultListen
	}
	if err := validateListen(cfg.Server.Listen); err != nil {
		return err
	}
	if err := validatePublicURL(cfg.Server.PublicURL); err != nil {
		return err
	}
	for _, cidr := range cfg.Server.TrustedProxyCIDRs {
		if _, _, err := net.ParseCIDR(cidr); err != nil {
			return fmt.Errorf("%w: invalid server.trusted_proxy_cidrs entry %q", ErrConfigValidation, cidr)
		}
	}

	var err error
	if cfg.Database.Path, err = resolveRequiredPath(configDir, "database.path", cfg.Database.Path); err != nil {
		return err
	}
	if cfg.Auth.UsersFile, err = resolveRequiredPath(configDir, "auth.users_file", cfg.Auth.UsersFile); err != nil {
		return err
	}
	if cfg.Auth.SessionKeyFile, err = resolveRequiredPath(configDir, "auth.session_key_file", cfg.Auth.SessionKeyFile); err != nil {
		return err
	}
	if cfg.Backup.Directory, err = resolveRequiredPath(configDir, "backup.directory", cfg.Backup.Directory); err != nil {
		return err
	}

	if cfg.Auth.SessionTTLText == "" {
		cfg.Auth.SessionTTLText = defaultSessionTTL.String()
	}
	cfg.Auth.SessionTTL, err = time.ParseDuration(cfg.Auth.SessionTTLText)
	if err != nil || cfg.Auth.SessionTTL <= 0 {
		return fmt.Errorf("%w: auth.session_ttl must be a positive duration", ErrConfigValidation)
	}

	if err := checkRestrictedFile(cfg.Auth.UsersFile); err != nil {
		return err
	}
	if err := checkRestrictedFile(cfg.Auth.SessionKeyFile); err != nil {
		return err
	}
	return nil
}

func validateListen(listen string) error {
	host, port, err := net.SplitHostPort(listen)
	if err != nil || !isValidListenHost(host) {
		return fmt.Errorf("%w: server.listen must be a host:port", ErrConfigValidation)
	}
	if _, ok := parsePort(port); !ok {
		return fmt.Errorf("%w: server.listen must contain a port from 1 through 65535", ErrConfigValidation)
	}
	return nil
}

func validatePublicURL(value string) error {
	parsed, err := url.Parse(value)
	if err != nil || !parsed.IsAbs() || parsed.Scheme != "https" || parsed.Hostname() == "" {
		return fmt.Errorf("%w: server.public_url must be an absolute HTTPS URL", ErrConfigValidation)
	}
	if port, explicit := urlPort(parsed.Host); explicit {
		if _, ok := parsePort(port); !ok {
			return fmt.Errorf("%w: server.public_url must contain a port from 1 through 65535", ErrConfigValidation)
		}
	}
	return nil
}

func isValidListenHost(host string) bool {
	if host == "" || strings.TrimSpace(host) != host {
		return false
	}
	if net.ParseIP(host) != nil {
		return true
	}
	if strings.HasSuffix(host, ".") {
		host = strings.TrimSuffix(host, ".")
	}
	if host == "" || len(host) > 253 {
		return false
	}
	for _, label := range strings.Split(host, ".") {
		if len(label) == 0 || len(label) > 63 || !isASCIIAlphaNumeric(label[0]) || !isASCIIAlphaNumeric(label[len(label)-1]) {
			return false
		}
		for i := 1; i < len(label)-1; i++ {
			if !isASCIIAlphaNumeric(label[i]) && label[i] != '-' {
				return false
			}
		}
	}
	return true
}

func isASCIIAlphaNumeric(value byte) bool {
	return value >= 'a' && value <= 'z' || value >= 'A' && value <= 'Z' || value >= '0' && value <= '9'
}

func parsePort(port string) (int, bool) {
	if port == "" {
		return 0, false
	}
	for i := 0; i < len(port); i++ {
		if port[i] < '0' || port[i] > '9' {
			return 0, false
		}
	}
	portNumber, err := strconv.Atoi(port)
	if err != nil || portNumber < 1 || portNumber > 65535 {
		return 0, false
	}
	return portNumber, true
}

func urlPort(host string) (string, bool) {
	if strings.HasPrefix(host, "[") {
		if closeBracket := strings.LastIndex(host, "]"); closeBracket >= 0 {
			rest := host[closeBracket+1:]
			if strings.HasPrefix(rest, ":") {
				return rest[1:], true
			}
		}
		return "", false
	}
	if colon := strings.LastIndexByte(host, ':'); colon >= 0 {
		return host[colon+1:], true
	}
	return "", false
}

func resolveRequiredPath(base, field, value string) (string, error) {
	if strings.TrimSpace(value) == "" {
		return "", fmt.Errorf("%w: %s is required", ErrConfigValidation, field)
	}
	if filepath.IsAbs(value) {
		return filepath.Clean(value), nil
	}
	return filepath.Clean(filepath.Join(base, value)), nil
}

func checkRestrictedFile(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("%w: stat %s: %v", ErrConfigRead, path, err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%w: %s is not a regular file", ErrConfigValidation, path)
	}
	if info.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("%w: %s must not grant group or other access", ErrFilePermissions, path)
	}
	return nil
}
