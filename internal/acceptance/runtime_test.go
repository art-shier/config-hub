package acceptance

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
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
	runtimeSessionKey       = "runtime-session-key-012345678901234567890123456789"
)

func TestRuntimeWorkflow(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	var cliDiagnostics diagnosticLog
	fixture := startRuntimeServer(t, ctx)
	leakGuard := newRuntimeLeakGuard(func() { fixture.stop(t) }, func() string {
		return fixture.logs.String() + cliDiagnostics.AllString()
	})
	for label, secret := range map[string]string{
		"configured value":           databaseValue,
		"revision two value":         revisionTwoValue,
		"feature value":              featureValue,
		"revision two feature value": revisionTwoFeatureValue,
		"admin password":             adminPassword,
		"developer password":         developerPassword,
		"session signing key":        runtimeSessionKey,
	} {
		leakGuard.add(label, secret)
	}
	t.Cleanup(func() {
		if label, found := leakGuard.stopAndScan(); found {
			t.Errorf("captured logs contain %s", label)
		}
	})

	session := fixture.login(t, "admin", adminPassword, leakGuard.add)
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
	issuedToken, err := decodeIssuedMachineToken(issuedBody, leakGuard.add)
	if err != nil {
		t.Fatal(err)
	}

	getenv := runtimeEnvironment(fixture.url, issuedToken.Plaintext)
	var jsonOutput, dotenvOutput, childOutput bytes.Buffer
	if code := executeCLI(cli.Execute, ctx, []string{"export", "--project", "shop", "--env", "production", "--format", "json"}, getenv, &jsonOutput, &cliDiagnostics); code != 0 {
		t.Fatalf("JSON export exit=%d", code)
	}
	var exported cli.ConfigResponse
	decodeJSON(t, jsonOutput.Bytes(), &exported)
	if exported.Revision != 1 || exported.Values["DATABASE_URL"] != databaseValue || exported.Values["FEATURE_FLAG"] != featureValue {
		t.Fatal("JSON export did not match revision 1")
	}
	if code := executeCLI(cli.Execute, ctx, []string{"export", "--project", "shop", "--env", "production", "--format", "dotenv"}, getenv, &dotenvOutput, &cliDiagnostics); code != 0 {
		t.Fatalf("dotenv export exit=%d", code)
	}
	if !strings.Contains(dotenvOutput.String(), "DATABASE_URL=") || !strings.Contains(dotenvOutput.String(), "FEATURE_FLAG=") {
		t.Fatal("dotenv export omitted expected keys")
	}

	if code := executeCLI(cli.Execute, ctx, []string{"run", "--project", "shop", "--env", "production", "--", "sh", "-c", `printf '%s|%s' "$DATABASE_URL" "$FEATURE_FLAG"`}, getenv, &childOutput, &cliDiagnostics); code != 0 {
		t.Fatalf("run exit=%d", code)
	}
	if childOutput.String() != databaseValue+"|"+featureValue {
		t.Fatalf("child environment output length=%d, want=%d", childOutput.Len(), len(databaseValue)+1+len(featureValue))
	}

	var deniedOutput bytes.Buffer
	if code := executeCLI(cli.Execute, ctx, []string{"export", "--project", "shop", "--env", "development", "--format", "json"}, getenv, &deniedOutput, &cliDiagnostics); code != 1 {
		t.Fatalf("development export exit=%d output_length=%d", code, deniedOutput.Len())
	}
	if !strings.Contains(cliDiagnostics.CurrentString(), "status 403") || !strings.Contains(cliDiagnostics.CurrentString(), "scope_denied") {
		t.Fatal("development denial diagnostics omitted expected status or code")
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
	if code := executeCLI(cli.Execute, ctx, []string{"export", "--project", "shop", "--env", "production", "--format", "json"}, getenv, &jsonOutput, &cliDiagnostics); code != 0 {
		t.Fatalf("post-rollback export exit=%d", code)
	}
	decodeJSON(t, jsonOutput.Bytes(), &exported)
	if exported.Revision != 3 || exported.Values["DATABASE_URL"] != databaseValue || exported.Values["FEATURE_FLAG"] != featureValue {
		t.Fatal("post-rollback export did not match revision 3")
	}

	backupPath := filepath.Join(fixture.runtimeDir, "backups", "runtime.db")
	backupCommand := exec.CommandContext(ctx, fixture.serverBinary, "backup", "--config", fixture.configPath, "--output", backupPath)
	backupCommand.Dir = acceptanceRepositoryRoot(t)
	backupCommand.Stdout = &fixture.logs
	backupCommand.Stderr = &fixture.logs
	if err := backupCommand.Run(); err != nil {
		t.Fatalf("online backup command: %v", err)
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
	processDone    chan struct{}
	processMu      sync.Mutex
	processErr     error
	stopOnce       sync.Once
}

type browserSession struct {
	cookie *http.Cookie
	csrf   string
}

type cliExecutor func(context.Context, []string, func(string) string, io.Writer, io.Writer) int

type dynamicSecretRegistrar func(label, secret string)

type runtimeLeakGuard struct {
	stop         func()
	capturedLogs func() string
	secrets      map[string]string
}

func newRuntimeLeakGuard(stop func(), capturedLogs func() string) *runtimeLeakGuard {
	return &runtimeLeakGuard{stop: stop, capturedLogs: capturedLogs, secrets: make(map[string]string)}
}

func (g *runtimeLeakGuard) add(label, secret string) {
	if secret != "" {
		g.secrets[label] = secret
	}
}

func (g *runtimeLeakGuard) stopAndScan() (string, bool) {
	g.stop()
	return findSensitiveLogLeak(g.capturedLogs(), g.secrets)
}

func validateIssuedMachineToken(token string, register dynamicSecretRegistrar) error {
	if register != nil {
		register("machine token", token)
	}
	if !strings.HasPrefix(token, "ch_") {
		return errors.New("machine token plaintext was not returned at issuance")
	}
	return nil
}

func decodeIssuedMachineToken(contents []byte, register dynamicSecretRegistrar) (machineaccess.IssuedToken, error) {
	var response struct {
		Token machineaccess.IssuedToken `json:"token"`
	}
	decodeErr := json.Unmarshal(contents, &response)
	validationErr := validateIssuedMachineToken(response.Token.Plaintext, register)
	if decodeErr != nil {
		return machineaccess.IssuedToken{}, fmt.Errorf("decode issued token: %w", decodeErr)
	}
	if validationErr != nil {
		return machineaccess.IssuedToken{}, validationErr
	}
	return response.Token, nil
}

func TestDiagnosticLogRetainsEarlierCallsForLeakScan(t *testing.T) {
	const sentinel = "early-cli-diagnostic-secret"
	calls := 0
	executor := func(_ context.Context, _ []string, _ func(string) string, _ io.Writer, diagnostics io.Writer) int {
		calls++
		message := "later safe diagnostics"
		if calls == 1 {
			message = sentinel
		}
		if _, err := io.WriteString(diagnostics, message); err != nil {
			t.Fatal(err)
		}
		return 0
	}

	var diagnostics diagnosticLog
	for range 2 {
		if code := executeCLI(executor, context.Background(), nil, nil, io.Discard, &diagnostics); code != 0 {
			t.Fatalf("execute CLI exit=%d", code)
		}
	}
	if got := diagnostics.CurrentString(); got != "later safe diagnostics" {
		t.Fatalf("current diagnostics=%q", got)
	}
	label, found := findSensitiveLogLeak(diagnostics.AllString(), map[string]string{"early CLI diagnostics": sentinel})
	if !found || label != "early CLI diagnostics" {
		t.Fatalf("leak scan result label=%q found=%t", label, found)
	}
}

func TestRuntimeLeakGuardStopsBeforeScanningAndReportsOnlyLabel(t *testing.T) {
	const secret = "early-runtime-secret"
	stopped := false
	guard := newRuntimeLeakGuard(func() {
		stopped = true
	}, func() string {
		if !stopped {
			t.Fatal("scanned runtime output before resources stopped")
		}
		return "late output contains " + secret
	})
	guard.add("machine token", secret)

	label, found := guard.stopAndScan()
	if !found || label != "machine token" {
		t.Fatalf("leak result label=%q found=%t", label, found)
	}
	if strings.Contains(label, secret) {
		t.Fatal("leak result disclosed the secret")
	}
}

func TestDynamicCredentialsAreRegisteredBeforeValidationFailure(t *testing.T) {
	for _, test := range []struct {
		name      string
		secret    string
		wantLabel string
		validate  func(dynamicSecretRegistrar) error
	}{
		{
			name:      "session cookie",
			secret:    "invalid-session-cookie-secret",
			wantLabel: "session cookie",
			validate: func(register dynamicSecretRegistrar) error {
				cookie := captureSessionCookie([]*http.Cookie{{Name: "confighub_session", Value: "invalid-session-cookie-secret"}}, register)
				_, err := validateBrowserSession([]byte(`{"csrf_token":""}`), cookie, register)
				return err
			},
		},
		{
			name:      "CSRF token",
			secret:    "invalid-csrf-token-secret",
			wantLabel: "CSRF token",
			validate: func(register dynamicSecretRegistrar) error {
				_, err := validateBrowserSession([]byte(`{"csrf_token":"invalid-csrf-token-secret"}`), nil, register)
				return err
			},
		},
		{
			name:      "machine token",
			secret:    "invalid-machine-token-secret",
			wantLabel: "machine token",
			validate: func(register dynamicSecretRegistrar) error {
				return validateIssuedMachineToken("invalid-machine-token-secret", register)
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			guard := newRuntimeLeakGuard(func() {}, func() string { return "captured " + test.secret })
			err := test.validate(guard.add)
			if err == nil {
				t.Fatal("invalid dynamic credential passed validation")
			}
			if strings.Contains(err.Error(), test.secret) {
				t.Fatal("validation error disclosed the dynamic credential")
			}
			label, found := guard.stopAndScan()
			if !found || label != test.wantLabel {
				t.Fatalf("leak result label=%q found=%t", label, found)
			}
		})
	}
}

func TestRuntimeServerRetriesAnOccupiedCandidatePort(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer occupied.Close()

	attempts := 0
	startedAt := time.Now()
	fixture := startRuntimeServerWithAddressProvider(t, ctx, func() (string, error) {
		attempts++
		if attempts == 1 {
			return occupied.Addr().String(), nil
		}
		return unusedLoopbackAddress()
	})
	t.Cleanup(func() { fixture.stop(t) })
	if attempts < 2 {
		t.Fatalf("address attempts=%d, want at least 2", attempts)
	}
	if elapsed := time.Since(startedAt); elapsed >= 1500*time.Millisecond {
		t.Fatalf("occupied-port retry elapsed=%s, want less than 1.5s", elapsed)
	}
}

func TestRuntimeReadinessCancelsAndJoinsProbeWhenProcessExits(t *testing.T) {
	requestStarted := make(chan struct{})
	requestCanceled := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.WriteHeader(http.StatusServiceUnavailable)
		response.(http.Flusher).Flush()
		close(requestStarted)
		<-request.Context().Done()
		close(requestCanceled)
	}))
	defer server.Close()

	fixture := &runtimeFixture{
		url:         server.URL,
		client:      server.Client(),
		processDone: make(chan struct{}),
	}
	result := make(chan error, 1)
	go func() { result <- fixture.waitReady(context.Background()) }()
	<-requestStarted
	close(fixture.processDone)

	if err := <-result; !errors.Is(err, errRuntimeServerExited) {
		t.Fatalf("readiness error=%v", err)
	}
	select {
	case <-requestCanceled:
	default:
		t.Fatal("waitReady returned before the canceled probe and response completed")
	}
}

type diagnosticLog struct {
	mu      sync.Mutex
	current bytes.Buffer
	all     bytes.Buffer
}

func (d *diagnosticLog) Write(contents []byte) (int, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if _, err := d.all.Write(contents); err != nil {
		return 0, err
	}
	return d.current.Write(contents)
}

func (d *diagnosticLog) ResetCurrent() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.current.Reset()
}

func (d *diagnosticLog) CurrentString() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.current.String()
}

func (d *diagnosticLog) AllString() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.all.String()
}

func findSensitiveLogLeak(capturedLogs string, secrets map[string]string) (string, bool) {
	for label, secret := range secrets {
		if secret != "" && strings.Contains(capturedLogs, secret) {
			return label, true
		}
	}
	return "", false
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

var errRuntimeServerExited = errors.New("runtime server exited during startup")

type runtimeAddressProvider func() (string, error)

func startRuntimeServer(t *testing.T, ctx context.Context) *runtimeFixture {
	t.Helper()
	return startRuntimeServerWithAddressProvider(t, ctx, unusedLoopbackAddress)
}

func startRuntimeServerWithAddressProvider(t *testing.T, ctx context.Context, addressProvider runtimeAddressProvider) *runtimeFixture {
	t.Helper()
	const maxStartAttempts = 5

	repositoryRoot := acceptanceRepositoryRoot(t)
	runtimeDir := t.TempDir()
	serverBinary := filepath.Join(runtimeDir, "confighub-server")
	build := exec.CommandContext(ctx, "go", "build", "-o", serverBinary, "./cmd/server")
	build.Dir = repositoryRoot
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build runtime server: %v output_length=%d", err, len(output))
	}

	usersPath := filepath.Join(runtimeDir, "users.yaml")
	sessionKeyPath := filepath.Join(runtimeDir, "session.key")
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
	writeRestrictedFile(t, sessionKeyPath, []byte(runtimeSessionKey+"\n"))

	for attempt := 1; attempt <= maxStartAttempts; attempt++ {
		address, err := addressProvider()
		if err != nil {
			t.Fatalf("select runtime address attempt=%d: %v", attempt, err)
		}
		publicOrigin := "https://" + address
		databasePath := filepath.Join(runtimeDir, fmt.Sprintf("data-%d", attempt), "confighub.db")
		configPath := filepath.Join(runtimeDir, fmt.Sprintf("config-%d.yaml", attempt))
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
			processDone:    make(chan struct{}),
		}
		fixture.command = exec.Command(serverBinary, "serve", "--config", configPath)
		fixture.command.Dir = repositoryRoot
		fixture.command.Stdout = &fixture.logs
		fixture.command.Stderr = &fixture.logs
		if err := fixture.command.Start(); err != nil {
			t.Fatalf("start runtime server attempt=%d: %v", attempt, err)
		}
		go fixture.waitForProcess()

		if err := fixture.waitReady(ctx); err == nil {
			t.Cleanup(func() { fixture.stop(t) })
			return fixture
		} else if errors.Is(err, errRuntimeServerExited) && attempt < maxStartAttempts {
			if label, found := findSensitiveLogLeak(fixture.logs.String(), staticRuntimeSecrets()); found {
				t.Fatalf("runtime startup logs contain %s", label)
			}
			continue
		} else {
			if !errors.Is(err, errRuntimeServerExited) {
				fixture.stop(t)
			}
			if label, found := findSensitiveLogLeak(fixture.logs.String(), staticRuntimeSecrets()); found {
				t.Fatalf("runtime startup logs contain %s", label)
			}
			t.Fatalf("runtime server readiness attempt=%d: %v", attempt, err)
		}
	}

	t.Fatalf("runtime server did not start after %d attempts", maxStartAttempts)
	return nil
}

func staticRuntimeSecrets() map[string]string {
	return map[string]string{
		"admin password":      adminPassword,
		"developer password":  developerPassword,
		"session signing key": runtimeSessionKey,
	}
}

func unusedLoopbackAddress() (string, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", err
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		return "", err
	}
	return address, nil
}

func infoMode(info os.FileInfo) os.FileMode {
	if info == nil {
		return 0
	}
	return info.Mode()
}

func (f *runtimeFixture) waitForProcess() {
	err := f.command.Wait()
	f.processMu.Lock()
	f.processErr = err
	f.processMu.Unlock()
	close(f.processDone)
}

func (f *runtimeFixture) processResult() error {
	f.processMu.Lock()
	defer f.processMu.Unlock()
	return f.processErr
}

type readinessProbeResult struct {
	status int
	err    error
}

func (f *runtimeFixture) waitReady(ctx context.Context) error {
	overallCtx, cancelOverall := context.WithTimeout(ctx, 30*time.Second)
	defer cancelOverall()
	for {
		if err := f.readinessStopped(overallCtx, ctx); err != nil {
			return err
		}

		probeCtx, cancelProbe := context.WithTimeout(overallCtx, 250*time.Millisecond)
		probeDone := make(chan readinessProbeResult, 1)
		go func() { probeDone <- f.runReadinessProbe(probeCtx) }()

		var probe readinessProbeResult
		select {
		case <-f.processDone:
			cancelProbe()
			<-probeDone
			return fmt.Errorf("%w: %v", errRuntimeServerExited, f.processResult())
		case <-overallCtx.Done():
			cancelProbe()
			<-probeDone
			return readinessContextError(overallCtx, ctx)
		case probe = <-probeDone:
			cancelProbe()
		}

		// The probe and process can complete together. Never accept readiness or
		// launch another probe after the child has already exited.
		select {
		case <-f.processDone:
			return fmt.Errorf("%w: %v", errRuntimeServerExited, f.processResult())
		default:
		}
		if probe.err == nil && probe.status == http.StatusOK {
			return nil
		}

		retry := time.NewTimer(25 * time.Millisecond)
		select {
		case <-f.processDone:
			stopTimer(retry)
			return fmt.Errorf("%w: %v", errRuntimeServerExited, f.processResult())
		case <-overallCtx.Done():
			stopTimer(retry)
			return readinessContextError(overallCtx, ctx)
		case <-retry.C:
		}
	}
}

func (f *runtimeFixture) runReadinessProbe(ctx context.Context) readinessProbeResult {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, f.url+"/api/v1/health/ready", nil)
	if err != nil {
		return readinessProbeResult{err: fmt.Errorf("create readiness request: %w", err)}
	}
	response, err := f.client.Do(request)
	if err != nil {
		return readinessProbeResult{err: err}
	}
	defer response.Body.Close()
	_, copyErr := io.Copy(io.Discard, response.Body)
	if copyErr != nil {
		return readinessProbeResult{err: copyErr}
	}
	return readinessProbeResult{status: response.StatusCode}
}

func (f *runtimeFixture) readinessStopped(overallCtx, parentCtx context.Context) error {
	select {
	case <-f.processDone:
		return fmt.Errorf("%w: %v", errRuntimeServerExited, f.processResult())
	case <-overallCtx.Done():
		return readinessContextError(overallCtx, parentCtx)
	default:
		return nil
	}
}

func readinessContextError(overallCtx, parentCtx context.Context) error {
	if err := parentCtx.Err(); err != nil {
		return fmt.Errorf("readiness context: %w", err)
	}
	return fmt.Errorf("readiness timed out: %w", overallCtx.Err())
}

func stopTimer(timer *time.Timer) {
	if !timer.Stop() {
		select {
		case <-timer.C:
		default:
		}
	}
}

func (f *runtimeFixture) stop(t *testing.T) {
	t.Helper()
	f.stopOnce.Do(func() {
		if f.command == nil || f.command.Process == nil {
			return
		}
		select {
		case <-f.processDone:
			if err := f.processResult(); err != nil {
				t.Errorf("runtime server exited before shutdown: %v", err)
			}
			return
		default:
		}
		if err := f.command.Process.Signal(syscall.SIGTERM); err != nil && !strings.Contains(err.Error(), "process already finished") {
			t.Errorf("stop runtime server: %v", err)
		}
		select {
		case <-f.processDone:
			if err := f.processResult(); err != nil {
				t.Errorf("runtime server exit after shutdown: %v", err)
			}
		case <-time.After(15 * time.Second):
			_ = f.command.Process.Kill()
			<-f.processDone
			t.Errorf("runtime server did not stop gracefully")
		}
	})
}

func (f *runtimeFixture) login(t *testing.T, username, password string, register dynamicSecretRegistrar) browserSession {
	t.Helper()
	body := f.requestJSON(t, nil, http.MethodPost, "/api/v1/auth/login", map[string]any{"username": username, "password": password}, http.StatusOK, register)
	session, err := validateBrowserSession(body.contents, body.cookie, register)
	if err != nil {
		t.Fatal(err)
	}
	return session
}

func validateBrowserSession(contents []byte, cookie *http.Cookie, register dynamicSecretRegistrar) (browserSession, error) {
	var response struct {
		CSRF string `json:"csrf_token"`
	}
	decodeErr := json.Unmarshal(contents, &response)
	if register != nil {
		register("CSRF token", response.CSRF)
	}
	if decodeErr != nil {
		return browserSession{}, fmt.Errorf("decode login response: %w", decodeErr)
	}
	if response.CSRF == "" || cookie == nil || cookie.Value == "" {
		return browserSession{}, errors.New("login response omitted browser credentials")
	}
	return browserSession{cookie: cookie, csrf: response.CSRF}, nil
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
	return f.requestJSON(t, &session, method, path, payload, status, nil).contents
}

type runtimeResponse struct {
	contents []byte
	cookie   *http.Cookie
}

func (f *runtimeFixture) requestJSON(t *testing.T, session *browserSession, method, path string, payload any, status int, register dynamicSecretRegistrar) runtimeResponse {
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
	result := runtimeResponse{cookie: captureSessionCookie(response.Cookies(), register)}
	defer response.Body.Close()
	contents, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != status {
		t.Fatalf("%s %s status=%d want=%d response_length=%d", method, path, response.StatusCode, status, len(contents))
	}
	result.contents = contents
	return result
}

func captureSessionCookie(cookies []*http.Cookie, register dynamicSecretRegistrar) *http.Cookie {
	var sessionCookie *http.Cookie
	for _, cookie := range cookies {
		if cookie.Name != "confighub_session" {
			continue
		}
		sessionCookie = cookie
		if register != nil {
			register("session cookie", cookie.Value)
		}
	}
	return sessionCookie
}

func executeCLI(executor cliExecutor, ctx context.Context, args []string, getenv func(string) string, stdout io.Writer, diagnostics *diagnosticLog) int {
	diagnostics.ResetCurrent()
	return executor(ctx, args, getenv, stdout, diagnostics)
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
		t.Fatalf("decode JSON: %v body_length=%d", err, len(contents))
	}
}
