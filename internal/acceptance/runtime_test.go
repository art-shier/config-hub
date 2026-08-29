package acceptance

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"confighub.local/internal/cli"
	"confighub.local/internal/database"
	"confighub.local/internal/machineaccess"
	"confighub.local/internal/projects"
	"confighub.local/internal/revisions"
)

const (
	adminPassword           = "runtime-admin-password"
	developerPassword       = "runtime-developer-password"
	databaseValue           = "postgres://runtime-config-value"
	featureValue            = "runtime-feature-revision-one"
	revisionTwoValue        = "postgres://runtime-revision-two"
	revisionTwoFeatureValue = "runtime-feature-revision-two"
)

func TestRuntimeWorkflow(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	fixture := startRuntimeServer(t, ctx)
	defer fixture.stop(t)

	session := fixture.login(t, "admin", adminPassword)
	project := fixture.writeJSON(t, session, http.MethodPost, "/api/v1/projects", map[string]any{
		"slug": "shop", "name": "Shop",
	}, http.StatusCreated)
	var createdProject struct {
		Project projects.Project `json:"project"`
	}
	decodeJSON(t, project, &createdProject)

	development := fixture.createEnvironment(t, session, "development", "Development")
	production := fixture.createEnvironment(t, session, "production", "Production")
	fixture.writeJSON(t, session, http.MethodPut, "/api/v1/projects/shop/members/developer-a", map[string]any{
		"permission": "editor",
	}, http.StatusNoContent)

	revisionOne := fixture.replaceConfig(t, session, 0, "runtime revision one", []revisions.Entry{
		{Key: "DATABASE_URL", Value: databaseValue, Service: "api"},
		{Key: "FEATURE_FLAG", Value: featureValue},
	})
	if revisionOne.Version != 1 {
		t.Fatalf("revision one version=%d", revisionOne.Version)
	}

	identityBody := fixture.writeJSON(t, session, http.MethodPost, "/api/v1/machine-identities", map[string]any{
		"name": "shop-ci", "description": "runtime acceptance", "enabled": true,
	}, http.StatusCreated)
	var identity struct {
		Identity machineaccess.Identity `json:"identity"`
	}
	decodeJSON(t, identityBody, &identity)
	fixture.writeJSON(t, session, http.MethodPut, "/api/v1/machine-identities/"+identity.Identity.ID+"/grants", map[string]any{
		"grants": []machineaccess.EnvironmentGrant{{ProjectID: createdProject.Project.ID, EnvironmentID: production.ID}},
	}, http.StatusNoContent)
	issuedBody := fixture.writeJSON(t, session, http.MethodPost, "/api/v1/machine-identities/"+identity.Identity.ID+"/tokens", map[string]any{
		"name": "runtime", "expires_at": time.Now().UTC().Add(time.Hour).Truncate(time.Second),
	}, http.StatusCreated)
	var issued struct {
		Token machineaccess.IssuedToken `json:"token"`
	}
	decodeJSON(t, issuedBody, &issued)
	if !strings.HasPrefix(issued.Token.Plaintext, "ch_") {
		t.Fatal("machine token plaintext was not returned at issuance")
	}

	getenv := runtimeEnvironment(fixture.url, issued.Token.Plaintext)
	var jsonOutput, dotenvOutput, childOutput, cliDiagnostics bytes.Buffer
	if code := cli.Execute(ctx, []string{"export", "--project", "shop", "--env", "production", "--format", "json"}, getenv, &jsonOutput, &cliDiagnostics); code != 0 {
		t.Fatalf("JSON export exit=%d diagnostics=%q", code, cliDiagnostics.String())
	}
	var exported cli.ConfigResponse
	decodeJSON(t, jsonOutput.Bytes(), &exported)
	if exported.Revision != 1 || exported.Values["DATABASE_URL"] != databaseValue || exported.Values["FEATURE_FLAG"] != featureValue {
		t.Fatalf("JSON export=%+v", exported)
	}
	if code := cli.Execute(ctx, []string{"export", "--project", "shop", "--env", "production", "--format", "dotenv"}, getenv, &dotenvOutput, &cliDiagnostics); code != 0 {
		t.Fatalf("dotenv export exit=%d diagnostics=%q", code, cliDiagnostics.String())
	}
	if !strings.Contains(dotenvOutput.String(), "DATABASE_URL=") || !strings.Contains(dotenvOutput.String(), "FEATURE_FLAG=") {
		t.Fatalf("dotenv export omitted values: %q", dotenvOutput.String())
	}

	if code := cli.Execute(ctx, []string{"run", "--project", "shop", "--env", "production", "--", "sh", "-c", `printf '%s|%s' "$DATABASE_URL" "$FEATURE_FLAG"`}, getenv, &childOutput, &cliDiagnostics); code != 0 {
		t.Fatalf("run exit=%d diagnostics=%q", code, cliDiagnostics.String())
	}
	if childOutput.String() != databaseValue+"|"+featureValue {
		t.Fatalf("child environment output=%q", childOutput.String())
	}

	var deniedOutput, deniedDiagnostics bytes.Buffer
	if code := cli.Execute(ctx, []string{"export", "--project", "shop", "--env", "development", "--format", "json"}, getenv, &deniedOutput, &deniedDiagnostics); code != 1 {
		t.Fatalf("development export exit=%d output=%q diagnostics=%q", code, deniedOutput.String(), deniedDiagnostics.String())
	}
	if !strings.Contains(deniedDiagnostics.String(), "status 403") || !strings.Contains(deniedDiagnostics.String(), "scope_denied") {
		t.Fatalf("development denial diagnostics=%q", deniedDiagnostics.String())
	}

	revisionTwo := fixture.replaceConfig(t, session, 1, "runtime revision two", []revisions.Entry{
		{Key: "DATABASE_URL", Value: revisionTwoValue, Service: "api"},
		{Key: "FEATURE_FLAG", Value: revisionTwoFeatureValue},
	})
	if revisionTwo.Version != 2 {
		t.Fatalf("revision two version=%d", revisionTwo.Version)
	}
	rollbackBody := fixture.writeJSON(t, session, http.MethodPost, "/api/v1/projects/shop/environments/production/revisions/1/rollback", map[string]any{
		"message": "restore runtime revision one",
	}, http.StatusCreated)
	var rolledBack struct {
		Revision revisions.Revision `json:"revision"`
	}
	decodeJSON(t, rollbackBody, &rolledBack)
	if rolledBack.Revision.Version != 3 {
		t.Fatalf("rollback version=%d", rolledBack.Revision.Version)
	}

	jsonOutput.Reset()
	cliDiagnostics.Reset()
	if code := cli.Execute(ctx, []string{"export", "--project", "shop", "--env", "production", "--format", "json"}, getenv, &jsonOutput, &cliDiagnostics); code != 0 {
		t.Fatalf("post-rollback export exit=%d diagnostics=%q", code, cliDiagnostics.String())
	}
	decodeJSON(t, jsonOutput.Bytes(), &exported)
	if exported.Revision != 3 || exported.Values["DATABASE_URL"] != databaseValue || exported.Values["FEATURE_FLAG"] != featureValue {
		t.Fatalf("post-rollback export=%+v", exported)
	}

	backupPath := filepath.Join(fixture.runtimeDir, "backups", "runtime.db")
	backupCommand := exec.CommandContext(ctx, fixture.serverBinary, "backup", "--config", fixture.configPath, "--output", backupPath)
	backupCommand.Dir = acceptanceRepositoryRoot(t)
	backupCommand.Stdout = &fixture.logs
	backupCommand.Stderr = &fixture.logs
	if err := backupCommand.Run(); err != nil {
		t.Fatalf("online backup command: %v logs=%s", err, fixture.logs.String())
	}
	backup, err := database.OpenReadOnly(backupPath)
	if err != nil {
		t.Fatal(err)
	}
	defer backup.Close()
	var integrity string
	if err := backup.QueryRowContext(ctx, `PRAGMA integrity_check`).Scan(&integrity); err != nil || integrity != "ok" {
		t.Fatalf("backup integrity=%q err=%v", integrity, err)
	}
	var version int64
	if err := backup.QueryRowContext(ctx, `SELECT MAX(version) FROM revisions WHERE environment_id = ?`, production.ID).Scan(&version); err != nil || version != 3 {
		t.Fatalf("backup revision=%d err=%v", version, err)
	}
	for label, path := range map[string]string{
		"runtime config": fixture.configPath,
		"users file":     fixture.usersPath,
		"session key":    fixture.sessionKeyPath,
		"database":       fixture.databasePath,
		"backup":         backupPath,
	} {
		info, err := os.Stat(path)
		if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
			t.Fatalf("%s mode=%v err=%v", label, infoMode(info), err)
		}
	}

	fixture.stop(t)
	capturedLogs := fixture.logs.String() + cliDiagnostics.String() + deniedDiagnostics.String()
	for label, secret := range map[string]string{
		"configured value":           databaseValue,
		"revision two value":         revisionTwoValue,
		"feature value":              featureValue,
		"revision two feature value": revisionTwoFeatureValue,
		"admin password":             adminPassword,
		"developer password":         developerPassword,
		"session cookie":             session.cookie.Value,
		"machine token":              issued.Token.Plaintext,
	} {
		if strings.Contains(capturedLogs, secret) {
			t.Fatalf("captured logs contain %s", label)
		}
	}
	if development.ID == "" {
		t.Fatal("development environment was not created")
	}
}

type runtimeFixture struct {
	url            string
	publicOrigin   string
	runtimeDir     string
	databasePath   string
	serverBinary   string
	configPath     string
	usersPath      string
	sessionKeyPath string
	client         *http.Client
	command        *exec.Cmd
	logs           lockedBuffer
	stopOnce       sync.Once
}

type browserSession struct {
	cookie *http.Cookie
	csrf   string
}

type lockedBuffer struct {
	mu     sync.Mutex
	buffer bytes.Buffer
}

func (b *lockedBuffer) Write(contents []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.Write(contents)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.String()
}

func startRuntimeServer(t *testing.T, ctx context.Context) *runtimeFixture {
	t.Helper()
	repositoryRoot := acceptanceRepositoryRoot(t)
	runtimeDir := t.TempDir()
	serverBinary := filepath.Join(runtimeDir, "confighub-server")
	build := exec.CommandContext(ctx, "go", "build", "-o", serverBinary, "./cmd/server")
	build.Dir = repositoryRoot
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build runtime server: %v\n%s", err, output)
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	publicOrigin := "https://" + address
	databasePath := filepath.Join(runtimeDir, "data", "confighub.db")
	usersPath := filepath.Join(runtimeDir, "users.yaml")
	sessionKeyPath := filepath.Join(runtimeDir, "session.key")
	configPath := filepath.Join(runtimeDir, "config.yaml")
	writeRestrictedFile(t, usersPath, []byte(fmt.Sprintf(`users:
  - username: admin
    display_name: Runtime Administrator
    password: %s
    role: admin
    enabled: true
  - username: developer-a
    display_name: Runtime Developer
    password: %s
    role: member
    enabled: true
`, adminPassword, developerPassword)))
	writeRestrictedFile(t, sessionKeyPath, []byte("runtime-session-key-012345678901234567890123456789\n"))
	writeRestrictedFile(t, configPath, []byte(fmt.Sprintf(`server:
  listen: %s
  public_url: %s
  trusted_proxy_cidrs:
    - 127.0.0.1/32
database:
  path: %s
auth:
  users_file: %s
  session_key_file: %s
  session_ttl: 1h
backup:
  directory: %s
`, address, publicOrigin, databasePath, usersPath, sessionKeyPath, filepath.Join(runtimeDir, "backups"))))

	fixture := &runtimeFixture{
		url:            "http://" + address,
		publicOrigin:   publicOrigin,
		runtimeDir:     runtimeDir,
		databasePath:   databasePath,
		serverBinary:   serverBinary,
		configPath:     configPath,
		usersPath:      usersPath,
		sessionKeyPath: sessionKeyPath,
		client:         &http.Client{Timeout: 5 * time.Second},
	}
	fixture.command = exec.CommandContext(ctx, serverBinary, "serve", "--config", configPath)
	fixture.command.Dir = repositoryRoot
	fixture.command.Stdout = &fixture.logs
	fixture.command.Stderr = &fixture.logs
	if err := fixture.command.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { fixture.stop(t) })
	fixture.waitReady(t, ctx)
	return fixture
}

func infoMode(info os.FileInfo) os.FileMode {
	if info == nil {
		return 0
	}
	return info.Mode()
}

func (f *runtimeFixture) waitReady(t *testing.T, ctx context.Context) {
	t.Helper()
	deadline := time.NewTimer(30 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, f.url+"/api/v1/health/ready", nil)
		if err != nil {
			t.Fatal(err)
		}
		response, err := f.client.Do(request)
		if err == nil {
			_, _ = io.Copy(io.Discard, response.Body)
			_ = response.Body.Close()
			if response.StatusCode == http.StatusOK {
				return
			}
		}
		select {
		case <-ctx.Done():
			t.Fatalf("server readiness: %v logs=%s", ctx.Err(), f.logs.String())
		case <-deadline.C:
			t.Fatalf("server readiness timed out logs=%s", f.logs.String())
		case <-ticker.C:
		}
	}
}

func (f *runtimeFixture) stop(t *testing.T) {
	t.Helper()
	f.stopOnce.Do(func() {
		if f.command == nil || f.command.Process == nil {
			return
		}
		if err := f.command.Process.Signal(syscall.SIGTERM); err != nil && !strings.Contains(err.Error(), "process already finished") {
			t.Errorf("stop runtime server: %v", err)
		}
		wait := make(chan error, 1)
		go func() { wait <- f.command.Wait() }()
		select {
		case err := <-wait:
			if err != nil {
				t.Errorf("runtime server exit: %v logs=%s", err, f.logs.String())
			}
		case <-time.After(15 * time.Second):
			_ = f.command.Process.Kill()
			<-wait
			t.Errorf("runtime server did not stop gracefully")
		}
	})
}

func (f *runtimeFixture) login(t *testing.T, username, password string) browserSession {
	t.Helper()
	body := f.requestJSON(t, nil, http.MethodPost, "/api/v1/auth/login", map[string]any{"username": username, "password": password}, http.StatusOK)
	var response struct {
		CSRF string `json:"csrf_token"`
	}
	decodeJSON(t, body.contents, &response)
	if response.CSRF == "" || body.cookie == nil || body.cookie.Value == "" {
		t.Fatalf("login response omitted browser credentials")
	}
	return browserSession{cookie: body.cookie, csrf: response.CSRF}
}

func (f *runtimeFixture) createEnvironment(t *testing.T, session browserSession, slug, name string) projects.Environment {
	t.Helper()
	body := f.writeJSON(t, session, http.MethodPost, "/api/v1/projects/shop/environments", map[string]any{"slug": slug, "name": name}, http.StatusCreated)
	var response struct {
		Environment projects.Environment `json:"environment"`
	}
	decodeJSON(t, body, &response)
	return response.Environment
}

func (f *runtimeFixture) replaceConfig(t *testing.T, session browserSession, base int64, message string, entries []revisions.Entry) revisions.Revision {
	t.Helper()
	body := f.writeJSON(t, session, http.MethodPut, "/api/v1/projects/shop/environments/production/config", map[string]any{
		"base_revision": base, "message": message, "entries": entries,
	}, http.StatusCreated)
	var response struct {
		Revision revisions.Revision `json:"revision"`
	}
	decodeJSON(t, body, &response)
	return response.Revision
}

func (f *runtimeFixture) writeJSON(t *testing.T, session browserSession, method, path string, payload any, status int) []byte {
	t.Helper()
	return f.requestJSON(t, &session, method, path, payload, status).contents
}

type runtimeResponse struct {
	contents []byte
	cookie   *http.Cookie
}

func (f *runtimeFixture) requestJSON(t *testing.T, session *browserSession, method, path string, payload any, status int) runtimeResponse {
	t.Helper()
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequest(method, f.url+path, bytes.NewReader(encoded))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", f.publicOrigin)
	if session != nil {
		request.AddCookie(session.cookie)
		request.Header.Set("X-CSRF-Token", session.csrf)
	}
	response, err := f.client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	contents, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != status {
		t.Fatalf("%s %s status=%d want=%d body=%s", method, path, response.StatusCode, status, contents)
	}
	result := runtimeResponse{contents: contents}
	for _, cookie := range response.Cookies() {
		if cookie.Name == "confighub_session" {
			result.cookie = cookie
		}
	}
	return result
}

func runtimeEnvironment(serverURL, token string) func(string) string {
	return func(key string) string {
		switch key {
		case "CONFIGHUB_URL":
			return serverURL
		case "CONFIGHUB_TOKEN":
			return token
		default:
			return os.Getenv(key)
		}
	}
}

func writeRestrictedFile(t *testing.T, path string, contents []byte) {
	t.Helper()
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write(contents); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

func acceptanceRepositoryRoot(t *testing.T) string {
	t.Helper()
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate runtime acceptance test")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(currentFile), "..", ".."))
}

func decodeJSON(t *testing.T, contents []byte, destination any) {
	t.Helper()
	if err := json.Unmarshal(contents, destination); err != nil {
		t.Fatalf("decode JSON: %v body=%s", err, contents)
	}
}
