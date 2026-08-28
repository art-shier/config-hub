# ConfigHub Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a native Go + React configuration center with SQLite version snapshots, project permissions, scoped machine tokens, a Web administration interface, and a read-only Go CLI.

**Architecture:** `confighub-server` is a single Go process that embeds the Vite build, exposes `/api/v1`, and exclusively owns a local SQLite file. `confighub` is a separate Go client binary that only uses the HTTPS API. Runtime accounts are reconciled from `users.yaml`; project data, grants, immutable revisions, sessions, and token hashes live in SQLite.

**Tech Stack:** Go 1.25, standard `net/http`, `modernc.org/sqlite`, React 19, TypeScript 5.9, Vite 8, Vitest, Testing Library, Playwright, SQLite, Bash.

---

## Implementation rules

- Work in an isolated feature worktree created from `main`.
- Follow red-green-refactor for every behavior-bearing task.
- Keep `cmd/server/main.go` and `cmd/cli/main.go` as composition roots only.
- Never log configuration values, passwords, Cookies, Authorization headers, or full Token values.
- The CLI must never open the server SQLite file.
- Keep the server as a single-instance design; do not add distributed abstractions.
- Do not add configuration encryption, secret classification, independent audit events, approvals, releases, historical API reads, Docker, or bundled reverse proxies.
- Run the focused test after each change, the package suite before each task commit, and `./scripts/check.sh` before final completion.

## Locked file map

### Go entry points and shared infrastructure

- `cmd/server/main.go`: parse `serve`/`backup`, compose dependencies, and map fatal errors to exit codes.
- `cmd/cli/main.go`: parse client commands and delegate to `internal/cli`.
- `internal/buildinfo/buildinfo.go`: linker-injected version.
- `internal/config/config.go`: YAML loading, defaults, path resolution, and permission checks.
- `internal/database/{database,migrations,backup}.go`: SQLite connection, schema, transactions, readiness, and online backup.
- `internal/webui/embed.go`: embedded Vite assets and SPA fallback.

### Domain packages

- `internal/auth/{password,token,users,sessions}.go`: Argon2id, opaque credentials, account reconciliation, and browser sessions.
- `internal/permissions/service.go`: global and project access decisions.
- `internal/projects/{repository,service}.go`: projects, environments, and member grants.
- `internal/revisions/{repository,service}.go`: immutable snapshots, conflict detection, diff, and rollback.
- `internal/machineaccess/{repository,service}.go`: identities, environment grants, and token lifecycle.
- `internal/httpapi/*.go`: safe middleware, stable errors, handlers, and route registration.
- `internal/server/server.go`: startup, readiness, graceful stop, and SIGHUP reload.

### CLI packages

- `internal/cli/root.go`: flags, URL/Token resolution, and exit codes.
- `internal/cli/client.go`: authenticated HTTP and error-envelope decoding.
- `internal/cli/{export,dotenv}.go`: JSON/dotenv rendering.
- `internal/cli/run.go`: environment merge, child process, signals, and exit propagation.

### React application

- `web/src/api/{types,client}.ts`: stable API contract and typed fetch.
- `web/src/auth/AuthProvider.tsx`: Session bootstrap/login/logout.
- `web/src/app/{App,AppShell}.tsx`: guarded routes and navigation.
- `web/src/pages/*.tsx`: Login, Projects, Project, Machine Access, Members, and System.
- `web/src/features/config/*.tsx`: configuration table and batch editor.
- `web/src/features/versions/*.tsx`: timeline, diff, and rollback.
- `web/src/features/members/*.tsx`: project member grants.
- `web/src/styles.css`: deliberate desktop-first UI and responsive read-only behavior.

### Operations and acceptance

- `migrations/001_initial.sql`: complete schema.
- `config/*.example.yaml`: safe examples without real credentials.
- `scripts/{build,start,backup,check}.sh`: native lifecycle.
- `internal/acceptance/runtime_test.go`: real server/API/CLI/backup workflow.
- `web/e2e/core-flow.spec.ts`: browser critical path.
- `README.md`: native deployment and accepted security limitations.

## Task 1: Bootstrap the two-binary repository and embedded React shell

**Files:**
- Create: `.gitignore`
- Create: `go.mod`
- Create: `go.sum`
- Create: `cmd/server/main.go`
- Create: `cmd/cli/main.go`
- Create: `internal/buildinfo/buildinfo.go`
- Create: `internal/webui/embed.go`
- Create: `internal/webui/embed_test.go`
- Create: `internal/webui/dist/bootstrap.txt`
- Create: `web/public/bootstrap.txt`
- Create: `web/package.json`
- Create: `web/package-lock.json`
- Create: `web/tsconfig.json`
- Create: `web/vite.config.ts`
- Create: `web/index.html`
- Create: `web/src/main.tsx`
- Create: `web/src/app/App.tsx`
- Create: `web/src/styles.css`

- [ ] **Step 1: Initialize locked Go and npm dependencies**

```bash
go mod init confighub.local
go get github.com/google/uuid@v1.6.0
go get github.com/spf13/cobra@v1.9.1
go get golang.org/x/crypto@v0.54.0
go get gopkg.in/yaml.v3@v3.0.1
go get modernc.org/sqlite@v1.39.1
npm install --prefix web react@19.2.8 react-dom@19.2.8 react-router-dom@7
npm install --prefix web --save-dev @types/react@19 @types/react-dom@19 @vitejs/plugin-react@6 typescript@5.9.2 vite@8.2.2 vitest@4.1.11 jsdom@30.0.1 @testing-library/react@16.3.2 @testing-library/jest-dom@7.0.1 @testing-library/user-event@14.6.1
```

Expected: both lockfiles are created without install errors.

- [ ] **Step 2: Write the failing embedded-assets test**

```go
package webui

import (
	"io/fs"
	"testing"
)

func TestAssetsContainBootstrapFile(t *testing.T) {
	assets, err := Assets()
	if err != nil {
		t.Fatal(err)
	}
	data, err := fs.ReadFile(assets, "bootstrap.txt")
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "ConfigHub web assets are replaced by the Vite build.\n" {
		t.Fatalf("unexpected bootstrap asset: %q", data)
	}
}
```

- [ ] **Step 3: Run it and verify the missing API**

Run: `go test ./internal/webui -run TestAssetsContainBootstrapFile -v`

Expected: FAIL because `Assets` is undefined.

- [ ] **Step 4: Implement the embed boundary and compiling entry points**

```go
package webui

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var embedded embed.FS

func Assets() (fs.FS, error) {
	return fs.Sub(embedded, "dist")
}
```

Create both `bootstrap.txt` files with the exact expected line. Create minimal `main` packages that print a not-configured message to stderr and exit 2. Create `internal/buildinfo/buildinfo.go` with `var Version = "dev"`. Configure Vite to output into `internal/webui/dist`, copy `web/public/bootstrap.txt`, and render a minimal `ConfigHub` React heading. Define exact npm scripts: `build` = `tsc -b && vite build`, `typecheck` = `tsc -b --pretty false`, and `test` = `vitest run`.

- [ ] **Step 5: Verify both toolchains**

```bash
npm run build --prefix web
go test ./...
go build ./cmd/server ./cmd/cli
```

Expected: all commands exit 0 and `internal/webui/dist/index.html` exists.

- [ ] **Step 6: Commit**

```bash
git add .gitignore go.mod go.sum cmd internal/buildinfo internal/webui web
git commit -m "chore: bootstrap ConfigHub Go and React applications"
```

## Task 2: Load and validate native runtime configuration

**Files:**
- Create: `internal/config/config.go`
- Create: `internal/config/config_test.go`
- Create: `config/config.example.yaml`
- Create: `config/users.example.yaml`

- [ ] **Step 1: Write failing defaults and validation tests**

```go
func TestLoadResolvesPathsAndDefaults(t *testing.T) {
	dir := t.TempDir()
	writeRestricted(t, filepath.Join(dir, "users.yaml"), "users: []\n")
	writeRestricted(t, filepath.Join(dir, "session.key"), "01234567890123456789012345678901\n")
	writeRestricted(t, filepath.Join(dir, "config.yaml"), `server:
  public_url: https://config.example.com
database:
  path: ./data/confighub.db
auth:
  users_file: ./users.yaml
  session_key_file: ./session.key
backup:
  directory: ./backups
`)
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
}
```

Add cases for unknown YAML keys, non-HTTPS public URL, invalid Session TTL, missing files, malformed trusted CIDRs, and permissions wider than `0600`.

- [ ] **Step 2: Run and observe failure**

Run: `go test ./internal/config -v`

Expected: FAIL because `Load` and configuration types are undefined.

- [ ] **Step 3: Implement exact types and validation**

```go
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

type DatabaseConfig struct { Path string `yaml:"path"` }

type AuthConfig struct {
	UsersFile      string        `yaml:"users_file"`
	SessionKeyFile string        `yaml:"session_key_file"`
	SessionTTL     time.Duration `yaml:"-"`
	SessionTTLText string        `yaml:"session_ttl"`
}

type BackupConfig struct { Directory string `yaml:"directory"` }
```

Use `yaml.Decoder.KnownFields(true)`, default listen and TTL, resolve paths against the config directory, parse trusted CIDRs, require HTTPS, and reject group/other file permission bits. Return wrapped sentinel errors for caller exit-code mapping.

- [ ] **Step 4: Add safe examples and verify**

Use the approved design schema in `config/config.example.yaml`. Put fake passwords only in `config/users.example.yaml`, with comments explaining `0600` and Git exclusion.

```bash
go test ./internal/config -v
git diff --check
```

Expected: PASS and no whitespace errors.

- [ ] **Step 5: Commit**

```bash
git add internal/config config
git commit -m "feat: validate native runtime configuration"
```

## Task 3: Create the SQLite schema, migration runner, and transaction boundary

**Files:**
- Create: `migrations/001_initial.sql`
- Create: `internal/database/database.go`
- Create: `internal/database/migrations.go`
- Create: `internal/database/database_test.go`

- [ ] **Step 1: Write the failing schema test**

```go
func TestOpenMigratesAndEnablesSQLiteSafety(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "confighub.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	var foreignKeys int
	if err := store.DB().QueryRow("PRAGMA foreign_keys").Scan(&foreignKeys); err != nil {
		t.Fatal(err)
	}
	if foreignKeys != 1 {
		t.Fatalf("foreign_keys = %d", foreignKeys)
	}
	for _, table := range []string{"users", "sessions", "projects", "project_members", "environments", "revisions", "revision_entries", "machine_identities", "machine_grants", "access_tokens"} {
		var count int
		err := store.DB().QueryRow("SELECT count(*) FROM sqlite_master WHERE type='table' AND name=?", table).Scan(&count)
		if err != nil || count != 1 {
			t.Fatalf("table %s count=%d err=%v", table, count, err)
		}
	}
}
```

- [ ] **Step 2: Run and observe failure**

Run: `go test ./internal/database -run TestOpenMigratesAndEnablesSQLiteSafety -v`

Expected: FAIL because `Open` is undefined.

- [ ] **Step 3: Write the complete initial schema**

Create the design tables with TEXT UUID primary keys, INTEGER Unix timestamps, foreign keys, and these invariants:

```sql
CREATE TABLE users (id TEXT PRIMARY KEY, username TEXT NOT NULL UNIQUE, display_name TEXT NOT NULL, password_hash TEXT NOT NULL, role TEXT NOT NULL CHECK (role IN ('admin','member')), enabled INTEGER NOT NULL CHECK (enabled IN (0,1)), created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL);
CREATE TABLE sessions (id TEXT PRIMARY KEY, user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE, token_hash BLOB NOT NULL UNIQUE, csrf_hash BLOB NOT NULL, expires_at INTEGER NOT NULL, created_at INTEGER NOT NULL);
CREATE TABLE projects (id TEXT PRIMARY KEY, slug TEXT NOT NULL UNIQUE, name TEXT NOT NULL, description TEXT NOT NULL DEFAULT '', created_by TEXT NOT NULL REFERENCES users(id), created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL);
CREATE TABLE project_members (project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE, user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE, permission TEXT NOT NULL CHECK (permission IN ('viewer','editor')), PRIMARY KEY (project_id,user_id));
CREATE TABLE environments (id TEXT PRIMARY KEY, project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE, slug TEXT NOT NULL, name TEXT NOT NULL, current_revision_id TEXT, created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL, UNIQUE (project_id,slug));
CREATE TABLE revisions (id TEXT PRIMARY KEY, environment_id TEXT NOT NULL REFERENCES environments(id) ON DELETE CASCADE, version INTEGER NOT NULL, message TEXT NOT NULL DEFAULT '', created_by TEXT NOT NULL REFERENCES users(id), created_at INTEGER NOT NULL, UNIQUE (environment_id,version));
CREATE TABLE revision_entries (revision_id TEXT NOT NULL REFERENCES revisions(id) ON DELETE CASCADE, key TEXT NOT NULL, value TEXT NOT NULL, service TEXT, PRIMARY KEY (revision_id,key));
CREATE TABLE machine_identities (id TEXT PRIMARY KEY, name TEXT NOT NULL UNIQUE, description TEXT NOT NULL DEFAULT '', enabled INTEGER NOT NULL CHECK (enabled IN (0,1)), created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL);
CREATE TABLE machine_grants (identity_id TEXT NOT NULL REFERENCES machine_identities(id) ON DELETE CASCADE, project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE, environment_id TEXT NOT NULL REFERENCES environments(id) ON DELETE CASCADE, PRIMARY KEY (identity_id,project_id,environment_id));
CREATE TABLE access_tokens (id TEXT PRIMARY KEY, identity_id TEXT NOT NULL REFERENCES machine_identities(id) ON DELETE CASCADE, name TEXT NOT NULL, prefix TEXT NOT NULL, token_hash BLOB NOT NULL UNIQUE, expires_at INTEGER NOT NULL, revoked_at INTEGER, created_at INTEGER NOT NULL, UNIQUE (identity_id,name));
```

Add `schema_migrations`, indexes for common foreign-key lookups, and triggers enforcing that a non-null `environments.current_revision_id` exists in `revisions` for the same environment, that a current Revision cannot be deleted, and that every machine grant's environment belongs to its project. These triggers intentionally resolve the circular current-Revision relationship while preserving the normal `revisions.environment_id` foreign key.

- [ ] **Step 4: Implement database APIs**

```go
type Store struct { db *sql.DB }

func Open(path string) (*Store, error)
func (s *Store) DB() *sql.DB
func (s *Store) Close() error
func (s *Store) Ready(ctx context.Context) error
func (s *Store) InTx(ctx context.Context, fn func(*sql.Tx) error) error
```

Create the parent directory as `0700`, configure the modernc DSN with `_txlock=immediate`, apply foreign keys, WAL, and a 5-second busy timeout, then run embedded migrations once. `InTx` must always roll back callback errors and only commit nil results; the DSN makes write transactions acquire the SQLite write lock at begin time.

- [ ] **Step 5: Verify idempotence, rollback, and commit**

Add tests that open one file twice and prove a callback error persists no rows.

```bash
go test ./internal/database -v
git add migrations internal/database
git commit -m "feat: add SQLite schema and migration runner"
```

Expected: PASS and a new commit.

## Task 4: Reconcile account configuration and hash passwords

**Files:**
- Create: `internal/auth/password.go`
- Create: `internal/auth/password_test.go`
- Create: `internal/auth/users.go`
- Create: `internal/auth/users_test.go`

- [ ] **Step 1: Write the failing password test**

```go
func TestPasswordRoundTripAndMismatch(t *testing.T) {
	hash, err := HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	if !VerifyPassword(hash, "correct horse battery staple") {
		t.Fatal("expected password to verify")
	}
	if VerifyPassword(hash, "wrong") {
		t.Fatal("wrong password verified")
	}
}
```

- [ ] **Step 2: Run and observe failure**

Run: `go test ./internal/auth -run TestPasswordRoundTripAndMismatch -v`

Expected: FAIL because password helpers are undefined.

- [ ] **Step 3: Implement versioned Argon2id hashing**

Use a 16-byte random salt, memory 64 MiB, time 3, threads 2, and 32-byte output. Encode the final two fields as unpadded base64 salt and derived hash in `$argon2id$v=19$m=65536,t=3,p=2$BASE64_SALT$BASE64_HASH`. Parse defensively and compare with `subtle.ConstantTimeCompare`.

- [ ] **Step 4: Write failing reconciliation tests**

```go
func TestSyncUsersChangesPasswordAndRevokesSessions(t *testing.T) {
	ctx := context.Background()
	store := testStore(t)
	syncer := NewUserSyncer(store)
	first := UserFile{Users: []UserSpec{{Username: "admin", DisplayName: "Admin", Password: "first", Role: "admin", Enabled: true}}}
	if _, err := syncer.Sync(ctx, first); err != nil {
		t.Fatal(err)
	}
	user := loadUserByUsername(t, store, "admin")
	insertSessionFixture(t, store, user.ID)
	second := UserFile{Users: []UserSpec{{Username: "admin", DisplayName: "Admin", Password: "second", Role: "admin", Enabled: true}}}
	result, err := syncer.Sync(ctx, second)
	if err != nil {
		t.Fatal(err)
	}
	if result.PasswordsChanged != 1 || countSessions(t, store, user.ID) != 0 {
		t.Fatalf("result=%+v sessions=%d", result, countSessions(t, store, user.ID))
	}
}
```

Also cover creation, unchanged idempotence, removal/disable, duplicate usernames, unknown fields, invalid roles, and no enabled admin.

- [ ] **Step 5: Implement strict account sync**

```go
type UserSpec struct {
	Username    string `yaml:"username"`
	DisplayName string `yaml:"display_name"`
	Password    string `yaml:"password"`
	Role        string `yaml:"role"`
	Enabled     bool   `yaml:"enabled"`
}

type UserFile struct { Users []UserSpec `yaml:"users"` }

type User struct {
	ID, Username, DisplayName, Role string
	Enabled                        bool
}

type SyncResult struct {
	Created, Updated, Disabled, PasswordsChanged int
	SyncedAt                                      time.Time
}

type UserSyncer struct { store *database.Store }

func NewUserSyncer(store *database.Store) *UserSyncer
func (s *UserSyncer) LoadAndSync(ctx context.Context, path string) (SyncResult, error)
func (s *UserSyncer) Sync(ctx context.Context, file UserFile) (SyncResult, error)
```

Require usernames matching `[A-Za-z0-9][A-Za-z0-9._-]{0,63}`, unique users, non-empty passwords, valid roles, and one enabled admin. Reconcile in one transaction. Verify an unchanged plaintext password against the stored hash; only rehash and delete Sessions when it differs. Disable missing users rather than deleting them.

- [ ] **Step 6: Verify and commit**

```bash
go test ./internal/auth -v
git add internal/auth
git commit -m "feat: reconcile configured users securely"
```

## Task 5: Create browser sessions and the safe HTTP foundation

**Files:**
- Create: `internal/auth/token.go`
- Create: `internal/auth/token_test.go`
- Create: `internal/auth/sessions.go`
- Create: `internal/auth/sessions_test.go`
- Create: `internal/httpapi/errors.go`
- Create: `internal/httpapi/middleware.go`
- Create: `internal/httpapi/router.go`
- Create: `internal/httpapi/auth_handlers.go`
- Create: `internal/httpapi/auth_handlers_test.go`

- [ ] **Step 1: Write the failing Session lifecycle test**

```go
func TestSessionCreateAuthenticateRevoke(t *testing.T) {
	ctx := context.Background()
	store := testStoreWithUser(t, "admin", "admin")
	manager := NewSessionManager(store, []byte("01234567890123456789012345678901"), time.Hour)
	issued, err := manager.Create(ctx, loadUserByUsername(t, store, "admin"))
	if err != nil {
		t.Fatal(err)
	}
	user, err := manager.Authenticate(ctx, issued.CookieValue)
	if err != nil || user.Username != "admin" {
		t.Fatalf("user=%+v err=%v", user, err)
	}
	if err := manager.Revoke(ctx, issued.CookieValue); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Authenticate(ctx, issued.CookieValue); !errors.Is(err, ErrInvalidSession) {
		t.Fatalf("error=%v", err)
	}
}
```

- [ ] **Step 2: Run and verify failure**

Run: `go test ./internal/auth -run TestSessionCreateAuthenticateRevoke -v`

Expected: FAIL because `NewSessionManager` is undefined.

- [ ] **Step 3: Implement opaque signed Sessions and CSRF**

```go
type IssuedSession struct {
	CookieValue string
	CSRFToken   string
	ExpiresAt   time.Time
}

type SessionManager struct {
	store *database.Store
	key   []byte
	ttl   time.Duration
}

func RandomOpaque(prefix string, bytes int) (string, error)
func SHA256(value string) []byte
func NewSessionManager(store *database.Store, key []byte, ttl time.Duration) *SessionManager
func (m *SessionManager) Create(ctx context.Context, user User) (IssuedSession, error)
func (m *SessionManager) Authenticate(ctx context.Context, cookie string) (User, error)
func (m *SessionManager) ValidateCSRF(cookie, token string) bool
func (m *SessionManager) Revoke(ctx context.Context, cookie string) error
```

Cookie format is `BASE64URL_RANDOM.BASE64URL_HMAC`. Store only SHA-256 of the random component and CSRF Token. Verify HMAC before lookup, then reject disabled users and expired Sessions. Use constant-time comparisons.

- [ ] **Step 4: Write failing HTTP authentication and safety tests**

Cover successful login, wrong password, disabled account, Session bootstrap, logout, missing Origin, CSRF mismatch, requests over 1 MiB, panic recovery, and redacted logs. Assert:

```go
if !cookie.HttpOnly || !cookie.Secure || cookie.SameSite != http.SameSiteLaxMode {
	t.Fatalf("unsafe cookie: %+v", cookie)
}
if got := response.Header().Get("Cache-Control"); got != "no-store" {
	t.Fatalf("Cache-Control = %q", got)
}
```

- [ ] **Step 5: Implement the HTTP shell**

Use standard `http.ServeMux`. Apply request ID, panic recovery, safe access log, security headers, body limit, then route. Register:

```text
POST /api/v1/auth/login
POST /api/v1/auth/logout
GET  /api/v1/auth/session
GET  /api/v1/health/live
```

Return the exact design error envelope. Log request ID, method, route pattern, status, byte count, duration, and trusted source IP only. Implement an in-memory login token bucket keyed by trusted source IP plus lowercase username.

- [ ] **Step 6: Verify and commit**

```bash
go test ./internal/auth ./internal/httpapi -v
git add internal/auth internal/httpapi
git commit -m "feat: add browser sessions and HTTP security"
```

## Task 6: Implement projects, environments, and human permissions

**Files:**
- Create: `internal/permissions/service.go`
- Create: `internal/permissions/service_test.go`
- Create: `internal/projects/repository.go`
- Create: `internal/projects/service.go`
- Create: `internal/projects/service_test.go`
- Create: `internal/httpapi/project_handlers.go`
- Create: `internal/httpapi/project_handlers_test.go`
- Modify: `internal/httpapi/router.go`

- [ ] **Step 1: Write the failing permission matrix**

```go
func TestProjectPermissionMatrix(t *testing.T) {
	cases := []struct {
		role, grant, action string
		want                bool
	}{
		{"admin", "", "manage_project", true},
		{"member", "viewer", "read_config", true},
		{"member", "viewer", "write_config", false},
		{"member", "editor", "write_config", true},
		{"member", "", "read_config", false},
	}
	for _, tc := range cases {
		if got := Allowed(tc.role, tc.grant, tc.action); got != tc.want {
			t.Fatalf("%+v got=%v", tc, got)
		}
	}
}
```

Add service tests proving members cannot list ungranted projects, editors cannot create environments, admins can create them, and disabled users have no access.

- [ ] **Step 2: Run and verify failure**

Run: `go test ./internal/permissions ./internal/projects -v`

Expected: FAIL because permission and project services are undefined.

- [ ] **Step 3: Implement focused repository and service contracts**

```go
type Project struct {
	ID, Slug, Name, Description string
	CreatedAt, UpdatedAt        time.Time
}

type Environment struct {
	ID, ProjectID, Slug, Name string
	CurrentRevisionID         *string
	CreatedAt, UpdatedAt      time.Time
}

type MemberGrant struct { UserID, Username, DisplayName, Permission string }

type CreateProject struct { Slug, Name, Description string }
type CreateEnvironment struct { Slug, Name string }

func (s *Service) ListVisible(ctx context.Context, actor auth.User) ([]Project, error)
func (s *Service) CreateProject(ctx context.Context, actor auth.User, input CreateProject) (Project, error)
func (s *Service) CreateEnvironment(ctx context.Context, actor auth.User, projectSlug string, input CreateEnvironment) (Environment, error)
func (s *Service) SetMember(ctx context.Context, actor auth.User, projectSlug, username, permission string) error
func (s *Service) RemoveMember(ctx context.Context, actor auth.User, projectSlug, username string) error
```

Validate slugs with `[a-z0-9][a-z0-9-]{0,62}`, trim names, reject disabled targets, and translate SQLite unique errors to typed conflicts.

- [ ] **Step 4: Add HTTP route tests and handlers**

```text
GET    /api/v1/projects
POST   /api/v1/projects
GET    /api/v1/projects/{project}
POST   /api/v1/projects/{project}/environments
GET    /api/v1/projects/{project}/members
PUT    /api/v1/projects/{project}/members/{username}
DELETE /api/v1/projects/{project}/members/{username}
```

Use Cookie Session auth and assert 401, 403, 404, 409, and 422 mappings.

- [ ] **Step 5: Verify and commit**

```bash
go test ./internal/permissions ./internal/projects ./internal/httpapi -v
git add internal/permissions internal/projects internal/httpapi
git commit -m "feat: add projects environments and member grants"
```

## Task 7: Implement immutable configuration revisions, diff, and rollback

**Files:**
- Create: `internal/revisions/repository.go`
- Create: `internal/revisions/service.go`
- Create: `internal/revisions/service_test.go`
- Create: `internal/httpapi/revision_handlers.go`
- Create: `internal/httpapi/revision_handlers_test.go`
- Modify: `internal/httpapi/router.go`

- [ ] **Step 1: Write failing atomic-snapshot and conflict tests**

```go
func TestReplaceCreatesAtomicSnapshotAndRejectsStaleBase(t *testing.T) {
	ctx := context.Background()
	service, editor, env := revisionFixture(t)
	first, err := service.Replace(ctx, editor, env.ID, ReplaceInput{
		BaseRevision: 0,
		Message: "initial",
		Entries: []Entry{{Key: "PORT", Value: "8080"}, {Key: "DATABASE_URL", Value: "postgres://db", Service: "api"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if first.Version != 1 || len(first.Entries) != 2 {
		t.Fatalf("revision=%+v", first)
	}
	_, err = service.Replace(ctx, editor, env.ID, ReplaceInput{BaseRevision: 0, Entries: []Entry{{Key: "PORT", Value: "9090"}}})
	if !errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("error=%v", err)
	}
	current, err := service.Current(ctx, editor, env.ID, "")
	if err != nil || current.Version != 1 || len(current.Entries) != 2 {
		t.Fatalf("current=%+v err=%v", current, err)
	}
}
```

Add cases for duplicate/invalid keys, exact newline values, service filtering, viewer writes, full-value diff, and rollback creating N+1.

- [ ] **Step 2: Run and verify failure**

Run: `go test ./internal/revisions -v`

Expected: FAIL because revision types are undefined.

- [ ] **Step 3: Implement snapshot types and algorithms**

```go
type Entry struct { Key, Value, Service string }

type Revision struct {
	ID, EnvironmentID, Message, CreatedBy string
	Version                               int64
	CreatedAt                             time.Time
	Entries                               []Entry
}

type ReplaceInput struct {
	BaseRevision int64
	Message      string
	Entries      []Entry
}

type Change struct { Key, Kind, Before, After, BeforeService, AfterService string }

func (s *Service) Current(ctx context.Context, actor auth.User, environmentID, service string) (Revision, error)
func (s *Service) Replace(ctx context.Context, actor auth.User, environmentID string, input ReplaceInput) (Revision, error)
func (s *Service) Diff(ctx context.Context, actor auth.User, environmentID string, version int64) ([]Change, error)
func (s *Service) Rollback(ctx context.Context, actor auth.User, environmentID string, version int64, message string) (Revision, error)
```

Trim key/service, preserve value bytes, sort keys, and reject duplicates. Within `Store.InTx` (configured as an immediate transaction), compare current version, insert Revision and every entry, then update `current_revision_id`. Diff sorted maps into added/changed/deleted. Rollback loads the target and uses the same replacement path with the current base.

- [ ] **Step 4: Add HTTP behavior**

```text
GET  /api/v1/projects/{project}/environments/{environment}/config
PUT  /api/v1/projects/{project}/environments/{environment}/config
GET  /api/v1/projects/{project}/environments/{environment}/revisions
GET  /api/v1/projects/{project}/environments/{environment}/revisions/{version}
GET  /api/v1/projects/{project}/environments/{environment}/revisions/{version}/diff
POST /api/v1/projects/{project}/environments/{environment}/revisions/{version}/rollback
```

Reads require viewer; writes require editor. Set `Cache-Control: no-store` on configuration responses and return 409 for stale bases.

- [ ] **Step 5: Verify and commit**

```bash
go test ./internal/revisions ./internal/httpapi -v
git add internal/revisions internal/httpapi
git commit -m "feat: add immutable configuration revisions"
```

## Task 8: Add machine identities, scoped Tokens, and machine reads

**Files:**
- Create: `internal/machineaccess/repository.go`
- Create: `internal/machineaccess/service.go`
- Create: `internal/machineaccess/service_test.go`
- Create: `internal/httpapi/machine_handlers.go`
- Create: `internal/httpapi/machine_handlers_test.go`
- Modify: `internal/httpapi/middleware.go`
- Modify: `internal/httpapi/router.go`
- Modify: `internal/httpapi/revision_handlers.go`

- [ ] **Step 1: Write failing Token and scope tests**

```go
func TestIssuedTokenIsShownOnceAndScopeIsEnforced(t *testing.T) {
	ctx := context.Background()
	service, admin, allowedEnv, deniedEnv := machineFixture(t)
	identity, err := service.CreateIdentity(ctx, admin, CreateIdentity{Name: "shop-ci", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.ReplaceGrants(ctx, admin, identity.ID, []EnvironmentGrant{{ProjectID: allowedEnv.ProjectID, EnvironmentID: allowedEnv.ID}}); err != nil {
		t.Fatal(err)
	}
	issued, err := service.IssueToken(ctx, admin, identity.ID, IssueToken{Name: "primary", ExpiresAt: time.Now().Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(issued.Plaintext, "ch_") {
		t.Fatalf("token=%q", issued.Plaintext)
	}
	if _, err := service.AuthenticateForEnvironment(ctx, issued.Plaintext, allowedEnv.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := service.AuthenticateForEnvironment(ctx, issued.Plaintext, deniedEnv.ID); !errors.Is(err, ErrScopeDenied) {
		t.Fatalf("error=%v", err)
	}
}
```

Add expiry, revoke, disabled identity, overlapping rotation, immediate grant change, plaintext-persistence, and write rejection tests.

- [ ] **Step 2: Run and verify failure**

Run: `go test ./internal/machineaccess -v`

Expected: FAIL because the service is undefined.

- [ ] **Step 3: Implement identity and Token lifecycle**

Use 32 random bytes after `ch_`, store SHA-256 and a ten-character display prefix, and return plaintext only from `IssueToken`:

```go
type IssuedToken struct {
	ID, Name, Prefix, Plaintext string
	ExpiresAt                  time.Time
}

type Identity struct { ID, Name, Description string; Enabled bool }
type CreateIdentity struct { Name, Description string; Enabled bool }
type EnvironmentGrant struct { ProjectID, EnvironmentID string }
type IssueToken struct { Name string; ExpiresAt time.Time }

func (s *Service) CreateIdentity(context.Context, auth.User, CreateIdentity) (Identity, error)
func (s *Service) ReplaceGrants(context.Context, auth.User, string, []EnvironmentGrant) error
func (s *Service) IssueToken(context.Context, auth.User, string, IssueToken) (IssuedToken, error)
func (s *Service) RevokeToken(context.Context, auth.User, string, string) error
func (s *Service) AuthenticateForEnvironment(context.Context, string, string) (Identity, error)
```

- [ ] **Step 4: Add admin routes and dual-auth read**

Register identity, grant, issue, and revoke routes under `/api/v1/machine-identities`. Only the GET current-config route accepts either Cookie viewer or authorized Bearer Token. Reject Bearer auth on every write. Do not store last-used data.

- [ ] **Step 5: Verify redaction and commit**

```bash
go test ./internal/machineaccess ./internal/httpapi -v
git add internal/machineaccess internal/httpapi
git commit -m "feat: add scoped machine access Tokens"
```

## Task 9: Compose the native server, SPA fallback, health, and SIGHUP reload

**Files:**
- Create: `internal/server/server.go`
- Create: `internal/server/server_test.go`
- Create: `internal/httpapi/system_handlers.go`
- Modify: `internal/httpapi/router.go`
- Modify: `internal/webui/embed.go`
- Modify: `cmd/server/main.go`

- [ ] **Step 1: Write failing lifecycle tests**

Test live/ready transitions, `/projects/shop` SPA fallback, 404 for missing file extensions, ten-second graceful drain, initial account-sync failure, and runtime reload retaining the last valid state:

```go
func TestReloadKeepsLastValidUsers(t *testing.T) {
	reloader := &fakeReloader{results: []error{nil, errors.New("invalid users file")}}
	server := newTestServer(t, WithUserReloader(reloader))
	server.Reload(context.Background())
	server.Reload(context.Background())
	if reloader.calls != 2 || !server.Ready() {
		t.Fatalf("calls=%d ready=%v", reloader.calls, server.Ready())
	}
}
```

- [ ] **Step 2: Run and verify failure**

Run: `go test ./internal/server ./internal/httpapi ./internal/webui -v`

Expected: FAIL because composition and SPA behavior are incomplete.

- [ ] **Step 3: Implement lifecycle and static rules**

Start listening only after migrations and initial account sync. On cancellation call `Shutdown` with ten seconds. Record the last successful reload; a failed runtime reload logs a redacted error and retains state. Never route `/api/` to SPA. Serve embedded files, immutable-cache hashed assets, no-cache `index.html` fallback for extensionless paths, and 404 missing extensions.

- [ ] **Step 4: Wire the server entry point**

Support:

```text
confighub-server serve --config ./config/config.yaml
confighub-server backup --config ./config/config.yaml --output ./backups/name.db
```

For `serve`, load config/key/Store/users/services/router/server in order. Use exit code 2 for usage/config errors and 1 for runtime errors. Install SIGINT/SIGTERM cancellation and separate SIGHUP reload.

- [ ] **Step 5: Verify and commit**

```bash
go test ./internal/server ./internal/httpapi ./internal/webui ./cmd/server -v
git add internal/server internal/httpapi internal/webui cmd/server
git commit -m "feat: compose the native ConfigHub server"
```

## Task 10: Build the CLI API client and deterministic export

**Files:**
- Create: `internal/cli/client.go`
- Create: `internal/cli/client_test.go`
- Create: `internal/cli/dotenv.go`
- Create: `internal/cli/dotenv_test.go`
- Create: `internal/cli/export.go`
- Create: `internal/cli/export_test.go`
- Create: `internal/cli/root.go`
- Modify: `cmd/cli/main.go`

- [ ] **Step 1: Write failing client and dotenv tests**

```go
func TestEncodeDotenvSortsAndQuotesWithoutInterpolation(t *testing.T) {
	values := map[string]string{
		"Z_LAST": "plain",
		"A_FIRST": "line one\nline two\n$(touch /tmp/never)",
	}
	got, err := EncodeDotenv(values)
	if err != nil {
		t.Fatal(err)
	}
	want := "A_FIRST='line one\nline two\n$(touch /tmp/never)'\nZ_LAST='plain'\n"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}
```

Client tests assert Bearer auth, URL joining, service query encoding, HTTPS enforcement with a loopback-HTTP development exception, no retry on 401/403, typed error-envelope decoding, a ten-second timeout, and no response-body logging.

- [ ] **Step 2: Run and verify failure**

Run: `go test ./internal/cli -run 'TestEncodeDotenv|TestClient' -v`

Expected: FAIL because client and encoder are undefined.

- [ ] **Step 3: Implement client and output contracts**

```go
type ConfigResponse struct {
	Project, Environment string
	Revision             int64
	Values               map[string]string
}

type Client struct {
	baseURL string
	token   string
	http    *http.Client
}

type APIError struct {
	Status                     int
	Code, Message, RequestID   string
	Fields                     map[string]string
}

func (e *APIError) Error() string { return e.Message }

func (c *Client) FetchConfig(ctx context.Context, project, environment, service string) (ConfigResponse, error)
func EncodeDotenv(values map[string]string) (string, error)
func WriteExport(w io.Writer, format string, response ConfigResponse) error
```

JSON is stable indented output. Dotenv sorts keys, single-quotes literal values, preserves actual newline characters inside the quoted value, escapes a literal single quote as the shell-safe sequence `'\''`, and never evaluates shell syntax.

- [ ] **Step 4: Implement root flags and credential resolution**

Support:

```text
confighub export --project P --env E [--service S] --format json|dotenv
```

Resolve URL from `--server` then `CONFIGHUB_URL`. Resolve Token from `--token-file` then `CONFIGHUB_TOKEN`; do not define `--token`. Reject Token files with group/other permissions. Write data only to stdout and errors only to stderr.

- [ ] **Step 5: Verify and commit**

```bash
go test ./internal/cli ./cmd/cli -v
go run ./cmd/cli export --help
git add internal/cli cmd/cli
git commit -m "feat: add ConfigHub CLI export"
```

Expected: tests PASS and help contains no plaintext Token flag.

## Task 11: Add CLI child-process injection and signal propagation

**Files:**
- Create: `internal/cli/run.go`
- Create: `internal/cli/run_test.go`
- Create: `internal/cli/testdata/printenv.go`
- Modify: `internal/cli/root.go`

- [ ] **Step 1: Write failing real-child tests**

Cover remote-over-local precedence, untouched unrelated variables, no child on fetch failure, exact exit code, and SIGTERM forwarding:

```go
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
```

- [ ] **Step 2: Run and verify failure**

Run: `go test ./internal/cli -run TestRun -v`

Expected: FAIL because `Runner` is undefined.

- [ ] **Step 3: Implement `run`**

Support:

```text
confighub run --project P --env E [--service S] -- command arg
```

Use this concrete boundary:

```go
type Runner struct {
	Fetch func(context.Context) (map[string]string, error)
}

func (r Runner) Run(ctx context.Context, argv, parentEnv []string, stdout, stderr io.Writer) (int, error)
```

Fetch before creating `exec.Cmd`. Merge current environment into a key map, overwrite with remote values, sort the final `KEY=value` slice, attach stdio, start the child in its own process group, forward SIGINT/SIGTERM, and propagate `exec.ExitError.ExitCode()`.

- [ ] **Step 4: Verify and commit**

```bash
go test ./internal/cli -run TestRun -v
go test ./internal/cli ./cmd/cli -v
git add internal/cli
git commit -m "feat: inject configuration into child processes"
```

## Task 12: Build the React authentication shell and typed API client

**Files:**
- Create: `web/src/test/setup.ts`
- Create: `web/src/api/types.ts`
- Create: `web/src/api/client.ts`
- Create: `web/src/api/client.test.ts`
- Create: `web/src/auth/AuthProvider.tsx`
- Create: `web/src/auth/AuthProvider.test.tsx`
- Create: `web/src/app/AppShell.tsx`
- Create: `web/src/pages/LoginPage.tsx`
- Create: `web/src/pages/LoginPage.test.tsx`
- Modify: `web/src/app/App.tsx`
- Modify: `web/src/styles.css`
- Modify: `web/vite.config.ts`
- Modify: `web/package.json`

- [ ] **Step 1: Write failing API and login tests**

```tsx
it("sends CSRF and exposes a typed conflict", async () => {
  server.use(http.put("/api/v1/projects/shop/environments/prod/config", () =>
    HttpResponse.json({ error: { code: "revision_conflict", message: "changed", request_id: "req_1", fields: {} } }, { status: 409 })
  ));
  const client = new APIClient(() => "csrf-token");
  await expect(client.put("/projects/shop/environments/prod/config", {})).rejects.toMatchObject({ status: 409, code: "revision_conflict" });
});

it("logs in and redirects to projects", async () => {
  renderAppAt("/login");
  await userEvent.type(screen.getByLabelText("Username"), "admin");
  await userEvent.type(screen.getByLabelText("Password"), "password");
  await userEvent.click(screen.getByRole("button", { name: "Sign in" }));
  expect(await screen.findByRole("heading", { name: "Projects" })).toBeInTheDocument();
});
```

Install `msw` as a locked dev dependency, then use MSW HTTP fixtures and jsdom setup.

- [ ] **Step 2: Run and verify failure**

```bash
npm run test --prefix web -- src/api/client.test.ts src/pages/LoginPage.test.tsx
```

Expected: FAIL because the client, provider, and page are missing.

- [ ] **Step 3: Implement typed fetch and Session state**

```ts
export class APIError extends Error {
  constructor(
    public status: number,
    public code: string,
    message: string,
    public requestId: string,
    public fields: Record<string, string>,
  ) {
    super(message);
  }
}

export interface APIClientContract {
  get<T>(path: string): Promise<T>;
  post<T>(path: string, body: unknown): Promise<T>;
  put<T>(path: string, body: unknown): Promise<T>;
  delete(path: string): Promise<void>;
}
```

`APIClient` must implement this contract and accept `() => string | null` in its constructor.

`APIClient` prefixes `/api/v1`, includes credentials, adds JSON and CSRF headers correctly, and converts non-2xx envelopes. `AuthProvider` bootstraps `/auth/session`, retains CSRF only in memory, exposes `user/loading/login/logout`, and clears state on 401.

- [ ] **Step 4: Implement guarded routes and semantic shell**

Require Session except on `/login`. AppShell shows Projects to all authenticated users and Machine Access, Members, System only to admins. Use labels, visible focus, keyboard menus, and an `aria-live` error region.

- [ ] **Step 5: Verify and commit**

```bash
npm run typecheck --prefix web
npm run test --prefix web -- src/api/client.test.ts src/auth/AuthProvider.test.tsx src/pages/LoginPage.test.tsx
git add web
git commit -m "feat: add authenticated React application shell"
```

## Task 13: Add project, environment, and member administration UI

**Files:**
- Create: `web/src/pages/ProjectsPage.tsx`
- Create: `web/src/pages/ProjectsPage.test.tsx`
- Create: `web/src/pages/ProjectPage.tsx`
- Create: `web/src/pages/ProjectPage.test.tsx`
- Create: `web/src/features/members/ProjectMembers.tsx`
- Create: `web/src/features/members/ProjectMembers.test.tsx`
- Modify: `web/src/api/types.ts`
- Modify: `web/src/app/App.tsx`
- Modify: `web/src/styles.css`

- [ ] **Step 1: Write failing role and project tests**

```tsx
it("does not render project creation for a member", async () => {
  mockSession({ role: "member" });
  mockProjects([{ slug: "shop", name: "Shop" }]);
  renderAppAt("/projects");
  expect(await screen.findByText("Shop")).toBeInTheDocument();
  expect(screen.queryByRole("button", { name: "New project" })).not.toBeInTheDocument();
});
```

Also test admin project/environment creation, visible-project filtering, viewer actions, grant changes, confirmations, and failed-save retention.

- [ ] **Step 2: Run and verify failure**

```bash
npm run test --prefix web -- src/pages/ProjectsPage.test.tsx src/pages/ProjectPage.test.tsx src/features/members/ProjectMembers.test.tsx
```

Expected: FAIL because pages do not exist.

- [ ] **Step 3: Implement project list and detail**

ProjectsPage lists name, slug, description, and updated time; admin creation uses a dialog with inline 422 fields. ProjectPage loads environments, preserves the selected environment slug in the URL query, and exposes Configuration, Versions, and Members tabs.

- [ ] **Step 4: Implement project grants**

ProjectMembers lists synchronized enabled users and current permissions. Admin can set viewer/editor or remove a grant; other roles get read-only output. Confirm removal and preserve state on request failure.

- [ ] **Step 5: Verify and commit**

```bash
npm run typecheck --prefix web
npm run test --prefix web -- src/pages/ProjectsPage.test.tsx src/pages/ProjectPage.test.tsx src/features/members/ProjectMembers.test.tsx
git add web
git commit -m "feat: add project and member administration UI"
```

## Task 14: Add configuration editing, versions, conflict handling, and rollback UI

**Files:**
- Create: `web/src/features/config/ConfigTable.tsx`
- Create: `web/src/features/config/ConfigTable.test.tsx`
- Create: `web/src/features/config/ConfigEditor.tsx`
- Create: `web/src/features/config/ConfigEditor.test.tsx`
- Create: `web/src/features/versions/VersionList.tsx`
- Create: `web/src/features/versions/VersionList.test.tsx`
- Modify: `web/src/pages/ProjectPage.tsx`
- Modify: `web/src/api/types.ts`
- Modify: `web/src/styles.css`

- [ ] **Step 1: Write failing editor and version tests**

Cover exact plain-value rendering, key/service search, add/edit/delete, duplicate/invalid keys, dirty navigation, one batch save, viewer read-only behavior, 409 refresh-and-compare, full-value diff, rollback confirmation, and local-draft retention:

```tsx
it("keeps the draft and offers refresh on a revision conflict", async () => {
  mockCurrentRevision(12, { PORT: "8080" });
  mockReplaceConflict();
  renderProjectConfiguration({ permission: "editor" });
  await userEvent.click(await screen.findByRole("button", { name: "Edit configuration" }));
  await userEvent.clear(screen.getByDisplayValue("8080"));
  await userEvent.type(screen.getByLabelText("Value for PORT"), "9090");
  await userEvent.click(screen.getByRole("button", { name: "Save revision" }));
  expect(await screen.findByText("Configuration changed since you loaded it")).toBeInTheDocument();
  expect(screen.getByDisplayValue("9090")).toBeInTheDocument();
  expect(screen.getByRole("button", { name: "Refresh and compare" })).toBeInTheDocument();
});
```

- [ ] **Step 2: Run and verify failure**

Run: `npm run test --prefix web -- src/features/config src/features/versions`

Expected: FAIL because components are missing.

- [ ] **Step 3: Implement read and batch edit**

Use a draft array with local stable IDs. Validate keys with the server regex, prevent duplicates, preserve exact value strings, and submit `{base_revision, message, entries}` once. Install `beforeunload` and router blocking only while dirty. Display values plainly without masking.

- [ ] **Step 4: Implement history, diff, and rollback**

Render added/changed/deleted rows with full before/after values. State in the dialog that rollback creates a new current version. On success refresh current and history; on failure keep the dialog and typed error.

- [ ] **Step 5: Verify and commit**

```bash
npm run typecheck --prefix web
npm run test --prefix web -- src/features/config src/features/versions
git add web
git commit -m "feat: add configuration and revision management UI"
```

## Task 15: Add machine access, member status, and system pages

**Files:**
- Create: `web/src/pages/MachineAccessPage.tsx`
- Create: `web/src/pages/MachineAccessPage.test.tsx`
- Create: `web/src/pages/MembersPage.tsx`
- Create: `web/src/pages/MembersPage.test.tsx`
- Create: `web/src/pages/SystemPage.tsx`
- Create: `web/src/pages/SystemPage.test.tsx`
- Modify: `web/src/api/types.ts`
- Modify: `web/src/app/App.tsx`
- Modify: `web/src/styles.css`

- [ ] **Step 1: Write failing admin-page tests**

Test non-admin redirect, explicit project/environment grants, one-time plaintext, dialog destruction, revoke confirmation, absence of password editing, and safe system state:

```tsx
it("never reveals an issued Token after the one-time dialog closes", async () => {
  mockIssuedToken("ch_once_only");
  renderAdminAt("/machine-access");
  await userEvent.click(await screen.findByRole("button", { name: "Issue Token" }));
  expect(await screen.findByText("ch_once_only")).toBeInTheDocument();
  await userEvent.click(screen.getByRole("button", { name: "I have copied it" }));
  expect(screen.queryByText("ch_once_only")).not.toBeInTheDocument();
  await userEvent.click(screen.getByRole("button", { name: "View primary Token" }));
  expect(screen.queryByText("ch_once_only")).not.toBeInTheDocument();
});
```

- [ ] **Step 2: Run and verify failure**

```bash
npm run test --prefix web -- src/pages/MachineAccessPage.test.tsx src/pages/MembersPage.test.tsx src/pages/SystemPage.test.tsx
```

Expected: FAIL because pages are missing.

- [ ] **Step 3: Implement the three admin pages**

Machine Access separates identity, grants, and Tokens; plaintext stays only in dialog state. Members shows username, display name, role, enabled state, and last successful sync status, with no credential editor. System shows build version, live/ready, SQLite readiness, and last successful sync time, without file contents or sensitive paths.

- [ ] **Step 4: Add focused responsive and accessible behavior**

Below 760px, navigation becomes a menu and tables scroll horizontally. Configuration editing shows a desktop-required notice while read-only pages remain usable. Preserve keyboard order, focus visibility, dialog focus trapping, and reduced-motion behavior.

- [ ] **Step 5: Verify and commit**

```bash
npm run typecheck --prefix web
npm run test --prefix web -- src/pages/MachineAccessPage.test.tsx src/pages/MembersPage.test.tsx src/pages/SystemPage.test.tsx
git add web
git commit -m "feat: add machine access and system administration UI"
```

## Task 16: Implement online backup and native operations scripts

**Files:**
- Create: `internal/database/backup.go`
- Create: `internal/database/backup_test.go`
- Modify: `cmd/server/main.go`
- Create: `scripts/build.sh`
- Create: `scripts/start.sh`
- Create: `scripts/backup.sh`
- Create: `scripts/check.sh`
- Modify: `.gitignore`

- [ ] **Step 1: Write failing online-backup tests**

```go
func TestBackupCreatesIndependentlyReadableDatabase(t *testing.T) {
	ctx := context.Background()
	store := populatedStore(t)
	destination := filepath.Join(t.TempDir(), "backup.db")
	if err := Backup(ctx, store.DB(), destination); err != nil {
		t.Fatal(err)
	}
	backup, err := OpenReadOnly(destination)
	if err != nil {
		t.Fatal(err)
	}
	defer backup.Close()
	var result string
	if err := backup.QueryRow("PRAGMA integrity_check").Scan(&result); err != nil || result != "ok" {
		t.Fatalf("result=%q err=%v", result, err)
	}
}
```

Also test live WAL writes, existing destination rejection, atomic rename, failed temporary cleanup, and mode `0600`.

- [ ] **Step 2: Run and verify failure**

Run: `go test ./internal/database -run TestBackup -v`

Expected: FAIL because backup is undefined.

- [ ] **Step 3: Implement online backup safely**

Use the driver backup API or `VACUUM INTO` with a new temporary file in the destination directory. Run `PRAGMA integrity_check`, chmod `0600`, fsync file and directory, and rename to a final never-overwritten path. Remove only the temporary file on failure.

Expose:

```go
func Backup(ctx context.Context, source *sql.DB, destination string) error
func OpenReadOnly(path string) (*sql.DB, error)
```

- [ ] **Step 4: Implement the native scripts**

Every script uses `#!/usr/bin/env bash`, `set -euo pipefail`, a script-relative repository root, quoted paths, and foreground processes.

`build.sh` executes frontend install/typecheck/tests/build, Go tests, then builds:

```bash
go build -trimpath -ldflags "-X confighub.local/internal/buildinfo.Version=$build_version" -o "$repo_root/dist/confighub-server" ./cmd/server
go build -trimpath -ldflags "-X confighub.local/internal/buildinfo.Version=$build_version" -o "$repo_root/dist/confighub" ./cmd/cli
```

Compute `build_version` from sanitized `git describe --always --dirty`. `start.sh` execs `confighub-server serve` using `CONFIGHUB_CONFIG` or the default config. `backup.sh` makes an UTC filename and execs the backup subcommand. `check.sh` runs formatting, vet, all Go tests, frontend typecheck/tests/build, both Go builds, Playwright, and runtime acceptance.

- [ ] **Step 5: Verify scripts and commit**

```bash
chmod +x scripts/*.sh
go test ./internal/database -run TestBackup -v
bash -n scripts/*.sh
./scripts/build.sh
git add internal/database cmd/server scripts .gitignore
git commit -m "feat: add native build start and backup operations"
```

Expected: all commands exit 0 and both binaries exist in `dist/`.

## Task 17: Add browser/runtime acceptance, documentation, and final verification

**Files:**
- Create: `web/playwright.config.ts`
- Create: `web/e2e/core-flow.spec.ts`
- Create: `internal/acceptance/runtime_test.go`
- Create: `README.md`
- Modify: `web/package.json`
- Modify: `web/package-lock.json`
- Modify: `scripts/check.sh`
- Modify: `config/config.example.yaml`
- Modify: `config/users.example.yaml`

- [ ] **Step 1: Write the failing real runtime workflow**

The Go acceptance test creates temporary `0600` runtime files and SQLite, starts the composed server on a random loopback port, then uses real HTTP and CLI command code to execute:

```text
admin login
create shop project
create development and production
grant developer-a editor
save production revision 1
issue shop-ci Token scoped only to production
export production JSON and dotenv
run a helper with injected configuration
verify development is 403 for that Token
save revision 2
rollback revision 1 to create revision 3
verify CLI reads revision 3 values
create backup and pass integrity_check
```

Fail if captured logs contain a configured value, plaintext password, Session Cookie, or full machine Token.

- [ ] **Step 2: Run and verify initial failure**

Run: `go test ./internal/acceptance -run TestRuntimeWorkflow -v`

Expected: FAIL at the first incomplete integration boundary.

- [ ] **Step 3: Close only revealed integration gaps**

Fix wiring, serialization, migration, and lifecycle defects without replacing the real server, HTTP client, CLI command layer, SQLite file, or backup with mocks.

Run: `go test ./internal/acceptance -run TestRuntimeWorkflow -v`

Expected: PASS.

- [ ] **Step 4: Add Playwright critical-path coverage**

Install `@playwright/test`, configure Chromium against a temporary real server, and cover login, project/environment creation, batch edit, version diff, a two-context conflict, one-time Token copy, and rollback.

Run: `npm run e2e --prefix web`

Expected: all Chromium tests PASS.

- [ ] **Step 5: Write operational README**

Document prerequisites, build/start commands, safe files, `chmod 600`, external HTTPS proxy expectation, Session key generation, CLI variables/examples, backup/restore, SIGHUP reload, health routes, single-instance SQLite, plaintext database/backup warning, and every deliberate MVP non-goal.

- [ ] **Step 6: Run the complete gate**

```bash
./scripts/check.sh
git diff --check
git status --short
```

Expected: checks exit 0, whitespace check is empty, and status lists only Task 17 files.

- [ ] **Step 7: Commit and rerun fresh evidence**

```bash
git add README.md internal/acceptance web scripts/check.sh config
git commit -m "test: verify complete ConfigHub workflows"
./scripts/check.sh
git status --short --branch
git log --oneline --decorate -5
```

Expected: the suite exits 0, the worktree is clean on the feature branch, and recent history shows task commits.

## Spec coverage matrix

| Design requirement | Implemented by |
| --- | --- |
| Native Go + React, two binaries, no Docker/Caddy | Tasks 1, 9, 16 |
| Strict runtime files and `0600` checks | Tasks 2, 16 |
| Plaintext `users.yaml`, Argon2id database hashes, revoke on change | Tasks 4, 5, 9 |
| Admin/member plus viewer/editor project permissions | Tasks 6, 13 |
| Project → environment → immutable full revision | Tasks 3, 7 |
| Conflict detection and rollback as a new version | Tasks 7, 14 |
| Plain string configuration and optional service filter | Tasks 7, 10, 14 |
| Machine identity, scoped read-only Token, rotation/revoke | Tasks 8, 15 |
| Versioned API and stable safe errors | Tasks 5–9 |
| CLI JSON/dotenv export and `run` | Tasks 10, 11 |
| Role-aware management pages | Tasks 12–15 |
| Loopback server, health, graceful stop, SIGHUP | Tasks 2, 9, 16 |
| SQLite WAL, migrations, backup/restore | Tasks 3, 16, 17 |
| No encryption, classification, audit, approvals, or historical API reads | Guardrails and Task 17 review |
| Unit, integration, browser, runtime, and build verification | Tasks 1–17, especially Task 17 |
