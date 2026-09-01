# ConfigHub CLI Machine-Token Write Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add least-privilege machine-token `set KEY=VALUE` and `unset KEY` CLI writes that preserve immutable revisions, service metadata, concurrent-edit safety, attribution, and secret-safe diagnostics.

**Architecture:** Add read/write permission to environment grants and migrate revision attribution so either a user or a machine identity owns each revision. The revision service owns an immediate SQLite transaction and calls a narrow machine authorizer within it before applying one key mutation to the full snapshot. HTTP exposes that path as a bearer-only `PATCH`; the CLI reads the base revision, sends one mutation without retrying conflicts, and prints only the resulting revision.

**Tech Stack:** Go 1.25, Cobra 1.9, `net/http`, modernc SQLite, React 19, TypeScript 5.9, Vite 8, Vitest 4, Testing Library, Playwright, Bash.

**Spec:** `docs/superpowers/specs/2026-09-01-cli-machine-write-design.md`

## Global Constraints

- Preserve the user's unrelated untracked `.coder-studio/` directory.
- Use red-green-refactor for every production behavior: add one focused failing test, run it and confirm the expected failure, implement the minimum, rerun green, then refactor.
- Existing machine grants migrate to `read`; no old token gains write access during upgrade.
- A `write` grant includes read access. A `read` grant never authorizes a mutation.
- Token, identity, grant, base-revision validation, snapshot mutation, attribution, sealing, and current-pointer advancement occur in one immediate SQLite transaction.
- Never retry `409 revision_conflict` automatically.
- Never put a machine token or configuration value in an error, log, test failure message, or successful mutation response.
- CLI local/usage failures return `2`, runtime/API failures return `1`, success returns `0`.
- CLI success output is exactly `revision <number>\n`.
- The only supported UI locales are `en-US` and `zh-CN`; new copy must exist in both.
- Before Task 7 changes UI code, read and follow both `frontend-design-premium:frontend-design` and `frontend-design-premium:frontend-design-premium`, preserving established `DESIGN.md` and `UX-CONTRACT.md` behavior.
- Keep Go `1.25.x` and Node.js `^22.22.2`, `^24.15.0`, or `>=26.0.0`; do not add dependencies.
- Commit only files named by the current task and leave unrelated worktree state untouched.

## File and Responsibility Map

- `migrations/002_machine_writes.sql`: grant permission and user-or-machine revision attribution migration, including rebuilt immutability triggers.
- `internal/database/database_test.go`: fresh-schema and populated-v1 migration proofs.
- `internal/revisions/service.go`, `internal/revisions/repository.go`: actor model, single-key mutation, transaction coordinator, and attribution reads.
- `internal/machineaccess/service.go`, `internal/machineaccess/repository.go`: grant permission and transactional token authorization.
- `internal/httpapi/revision_handlers.go`, `internal/httpapi/router.go`, `internal/httpapi/middleware.go`: bearer-only PATCH and routing surface.
- `internal/cli/mutation_client.go`: strict PATCH transport and minimized-response decoder.
- `internal/cli/mutation_command.go`: Cobra set/unset behavior and base-revision read.
- `web/src/pages/MachineAccessPage.tsx`: permission selection and display.
- `web/src/features/versions/VersionList.tsx`: user-versus-machine attribution display.
- `internal/acceptance/runtime_test.go`: real server plus real CLI write/read/unset/history proof.
- `README.md`: writable CLI contract and command-line disclosure warning.

---

### Task 1: Migrate Grant Permission and Revision Attribution

**Files:**
- Create: `migrations/002_machine_writes.sql`
- Modify: `internal/database/database_test.go`
- Modify: `internal/revisions/service.go`
- Modify: `internal/revisions/repository.go`
- Modify: `internal/revisions/service_test.go`
- Modify: `internal/httpapi/revision_handlers_test.go`

**Interfaces:**
- Produces: `Revision.CreatedByType string` and `RevisionSummary.CreatedByType string`, serialized as `created_by_type`.
- Produces: nullable `revisions.created_by` for users and nullable `created_by_machine_identity_id`, constrained so exactly one is non-null.
- Preserves: `created_by` remains both the public actor ID and SQL user-column name, keeping existing user callers source-compatible.

- [ ] **Step 1: Add failing migration and attribution tests**

Change the embedded migration assertion to require versions 1 and 2. Add a populated-v1 upgrade test that executes embedded `001_initial.sql`, records version 1, seeds one user revision/entry and one machine grant/token, closes the raw database, and reopens it through `database.Open`:

```go
func TestOpenMigratesVersionOneGrantAndRevisionAttribution(t *testing.T) {
	path := filepath.Join(t.TempDir(), "database", "version-one.db")
	seedVersionOneDatabase(t, path)
	store, err := Open(path)
	if err != nil { t.Fatal(err) }
	defer store.Close()

	var permission, createdBy string
	var machineCreator sql.NullString
	if err := store.DB().QueryRow(`SELECT permission FROM machine_grants
		WHERE identity_id = 'm1' AND environment_id = 'e1'`).Scan(&permission); err != nil { t.Fatal(err) }
	if err := store.DB().QueryRow(`SELECT created_by, created_by_machine_identity_id
		FROM revisions WHERE id = 'r1'`).Scan(&createdBy, &machineCreator); err != nil { t.Fatal(err) }
	if permission != "read" || createdBy != "u1" || machineCreator.Valid {
		t.Fatalf("permission=%q created_by=%q machine_creator_valid=%t", permission, createdBy, machineCreator.Valid)
	}
	assertRowCount(t, store, `SELECT count(*) FROM revision_entries
		WHERE revision_id = 'r1' AND key = 'VALUE' AND value = 'preserved'`, 1)
	assertRowCount(t, store, `SELECT count(*) FROM schema_migrations WHERE version = 2`, 1)
}
```

Add schema guard assertions that reject grant permissions outside `read`/`write`, reject revisions with neither or both creator columns, and accept one machine-owned revision. In revision service and HTTP tests, require existing user revisions to expose `created_by_type: "user"`.

- [ ] **Step 2: Run focused tests and verify RED**

```powershell
go test ./internal/database ./internal/revisions ./internal/httpapi -run 'Test(OpenMigratesVersionOneGrantAndRevisionAttribution|EmbeddedMigrations|Schema.*Revision|RevisionHTTP.*Lifecycle)' -count=1
```

Expected: FAIL because migration 2, polymorphic creator columns, and `created_by_type` do not exist.

- [ ] **Step 3: Add the migration without weakening immutability**

Create `002_machine_writes.sql` with this table transition:

```sql
ALTER TABLE machine_grants
ADD COLUMN permission TEXT NOT NULL DEFAULT 'read'
CHECK (permission IN ('read', 'write'));

DROP TRIGGER environments_current_revision_insert;
DROP TRIGGER environments_current_revision_update;
DROP TRIGGER environments_seal_current_revision_insert;
DROP TRIGGER environments_seal_current_revision_update;
DROP TRIGGER revisions_prevent_direct_delete;
DROP TRIGGER revisions_prevent_update;
DROP TRIGGER revisions_prevent_replace;
DROP TRIGGER revision_entries_prevent_sealed_insert;
DROP TRIGGER revision_entries_prevent_sealed_update;
DROP TRIGGER revision_entries_prevent_sealed_delete;
DROP INDEX revisions_environment_id_idx;
DROP INDEX revisions_created_by_idx;

ALTER TABLE revision_entries RENAME TO revision_entries_v1;
ALTER TABLE revisions RENAME TO revisions_v1;

CREATE TABLE revisions (
    id TEXT PRIMARY KEY NOT NULL,
    environment_id TEXT NOT NULL REFERENCES environments(id) ON DELETE CASCADE,
    version INTEGER NOT NULL,
    message TEXT NOT NULL DEFAULT '',
    created_by TEXT REFERENCES users(id),
    created_by_machine_identity_id TEXT REFERENCES machine_identities(id),
    created_at INTEGER NOT NULL,
    sealed INTEGER NOT NULL DEFAULT 0 CHECK (sealed IN (0, 1)),
    CHECK ((created_by IS NOT NULL) <> (created_by_machine_identity_id IS NOT NULL)),
    UNIQUE (environment_id, version)
);

CREATE TABLE revision_entries (
    revision_id TEXT NOT NULL REFERENCES revisions(id) ON DELETE CASCADE,
    key TEXT NOT NULL,
    value TEXT NOT NULL,
    service TEXT,
    PRIMARY KEY (revision_id, key)
);

INSERT INTO revisions
    (id, environment_id, version, message, created_by, created_by_machine_identity_id, created_at, sealed)
SELECT id, environment_id, version, message, created_by, NULL, created_at, sealed
FROM revisions_v1;
INSERT INTO revision_entries (revision_id, key, value, service)
SELECT revision_id, key, value, service FROM revision_entries_v1;
DROP TABLE revision_entries_v1;
DROP TABLE revisions_v1;

CREATE INDEX revisions_environment_id_idx ON revisions(environment_id);
CREATE INDEX revisions_created_by_idx ON revisions(created_by);
CREATE INDEX revisions_created_by_machine_idx ON revisions(created_by_machine_identity_id);
```

Recreate the four `environments_*current_revision*`/seal triggers, three `revisions_prevent_*` triggers, and three `revision_entries_prevent_sealed_*` triggers from migration 1 against the new tables. The update immutability predicate must include:

```sql
AND NEW.created_by IS OLD.created_by
AND NEW.created_by_machine_identity_id IS OLD.created_by_machine_identity_id
```

Do not omit or relax any existing ID, environment ownership, sealing, replace, update, or delete invariant.

- [ ] **Step 4: Read and write the actor type**

Add `CreatedByType string `json:"created_by_type"`` to both revision response types. In `revisionByID` and `list`, select and scan:

```sql
COALESCE(created_by, created_by_machine_identity_id),
CASE WHEN created_by IS NOT NULL THEN 'user' ELSE 'machine' END
```

Keep user snapshot inserts on `created_by` and set `revision.CreatedByType = "user"` in memory.

- [ ] **Step 5: Run migration and revision tests green**

```powershell
gofmt -w internal/database/database_test.go internal/revisions/service.go internal/revisions/repository.go internal/revisions/service_test.go internal/httpapi/revision_handlers_test.go
go test ./internal/database ./internal/revisions ./internal/httpapi -count=1
```

Expected: PASS, including every existing immutability and reverse-mutation test.

- [ ] **Step 6: Commit Task 1**

```powershell
git add -- migrations/002_machine_writes.sql internal/database/database_test.go internal/revisions/service.go internal/revisions/repository.go internal/revisions/service_test.go internal/httpapi/revision_handlers_test.go
git commit -m "feat: migrate machine write attribution"
```

---

### Task 2: Add Read and Write Machine Grant Permissions

**Files:**
- Modify: `internal/machineaccess/service.go`
- Modify: `internal/machineaccess/repository.go`
- Modify: `internal/machineaccess/service_test.go`
- Modify: `internal/httpapi/machine_handlers_test.go`
- Modify: `internal/acceptance/runtime_test.go` only where current read fixtures assert grant responses.

**Interfaces:**
- Produces: `GrantRead = "read"`, `GrantWrite = "write"`, and `EnvironmentGrant.Permission string` serialized as `permission`.
- Compatibility: omitted/empty permission normalizes to `read`; responses always include an explicit permission.

- [ ] **Step 1: Add failing service and HTTP permission tests**

Add a service test proving omitted permission becomes read, explicit write round-trips, and an unknown value identifies the indexed field:

```go
func TestMachineGrantPermissionsDefaultToReadAndValidateWrite(t *testing.T) {
	fixture := newMachineServiceFixture(t)
	identity := createMachineIdentity(t, fixture, "permission-ci")
	grants := []EnvironmentGrant{
		{ProjectID: fixture.allowedEnv.ProjectID, EnvironmentID: fixture.allowedEnv.ID},
		{ProjectID: fixture.deniedEnv.ProjectID, EnvironmentID: fixture.deniedEnv.ID, Permission: GrantWrite},
	}
	if err := fixture.service.ReplaceGrants(context.Background(), fixture.admin, identity.ID, grants); err != nil { t.Fatal(err) }
	detail, err := fixture.service.GetIdentity(context.Background(), fixture.admin, identity.ID)
	if err != nil { t.Fatal(err) }
	if detail.Grants[0].Permission != GrantRead || detail.Grants[1].Permission != GrantWrite {
		t.Fatalf("grants=%+v", detail.Grants)
	}
	err = fixture.service.ReplaceGrants(context.Background(), fixture.admin, identity.ID, []EnvironmentGrant{
		{ProjectID: fixture.allowedEnv.ProjectID, EnvironmentID: fixture.allowedEnv.ID, Permission: "admin"},
	})
	var validation *ValidationError
	if !errors.As(err, &validation) || validation.Fields["grants[0].permission"] == "" { t.Fatalf("error=%v", err) }
}
```

In `machine_handlers_test.go`, assert omitted permission is returned as read, explicit write survives PUT then GET, and `permission: "admin"` returns `422 validation_failed` without changing saved grants.

- [ ] **Step 2: Run focused tests and verify RED**

```powershell
go test ./internal/machineaccess ./internal/httpapi -run 'TestMachineGrantPermission|TestMachineAdminLifecycle' -count=1
```

Expected: FAIL because permission fields, constants, validation, and persistence do not exist.

- [ ] **Step 3: Implement normalization and persistence**

```go
const (
	GrantRead  = "read"
	GrantWrite = "write"
)

type EnvironmentGrant struct {
	ProjectID     string `json:"project_id"`
	EnvironmentID string `json:"environment_id"`
	Permission    string `json:"permission"`
}
```

In `validateGrants`:

```go
if grant.Permission == "" { grant.Permission = GrantRead }
if grant.Permission != GrantRead && grant.Permission != GrantWrite {
	fields[fmt.Sprintf("grants[%d].permission", index)] = "must be read or write"
}
```

List `project_id, environment_id, permission`, scan all three, and insert:

```go
_, err := tx.ExecContext(ctx, `INSERT INTO machine_grants
	(identity_id, project_id, environment_id, permission) VALUES (?, ?, ?, ?)`,
	identityID, grant.ProjectID, grant.EnvironmentID, grant.Permission)
```

Keep read authorization unchanged in this task: both read and write grants satisfy the existing read scope.

- [ ] **Step 4: Update existing explicit grant assertions**

Keep old fixture literals without a permission where they model legacy/read access. Update equality and HTTP JSON assertions so normalized output includes `permission: "read"`. Ensure normalization does not mutate the caller-owned slice.

- [ ] **Step 5: Run packages green and commit**

```powershell
gofmt -w internal/machineaccess/service.go internal/machineaccess/repository.go internal/machineaccess/service_test.go internal/httpapi/machine_handlers_test.go internal/acceptance/runtime_test.go
go test ./internal/machineaccess ./internal/httpapi ./internal/acceptance -count=1
git add -- internal/machineaccess/service.go internal/machineaccess/repository.go internal/machineaccess/service_test.go internal/httpapi/machine_handlers_test.go internal/acceptance/runtime_test.go
git commit -m "feat: add machine grant permissions"
```

---

### Task 3: Implement Transactional Single-Key Revision Mutations

**Files:**
- Modify: `internal/revisions/service.go`
- Modify: `internal/revisions/service_test.go`
- Modify: `internal/machineaccess/service.go`
- Modify: `internal/machineaccess/repository.go`
- Modify: `internal/machineaccess/service_test.go`
- Modify: `cmd/server/main.go`
- Modify: `cmd/server/main_test.go` if its fixture constructs services directly.

**Interfaces:**
- Produces: `MachineWriteActor`, `MachineWriteAuthorizer`, `ServiceOption`, `WithMachineWriteAuthorizer`.
- Produces: `MutationOperation`, `MachineMutationInput`, `MutationResult`, `(*revisions.Service).MutateForMachine`.
- Consumes: `machineaccess.Service.AuthorizeMachineWrite` implements `revisions.MachineWriteAuthorizer`.

- [ ] **Step 1: Add failing mutation and authorizer tests**

In revision service tests, cover new global set, existing set preserving service, explicit global service, unset, identical-set no-op, missing-unset no-op, stale base, invalid field combinations, snapshot limits, and machine attribution. Use these representative cases:

```go
tests := []struct {
	name      string
	operation MutationOperation
	created   bool
}{
	{name: "new global", operation: MutationOperation{Type: "set", Key: "NEW", Value: stringPointer("value")}, created: true},
	{name: "preserve service", operation: MutationOperation{Type: "set", Key: "EXISTING", Value: stringPointer("new")}, created: true},
	{name: "explicit global", operation: MutationOperation{Type: "set", Key: "EXISTING", Value: stringPointer("new"), Service: stringPointer("")}, created: true},
	{name: "unset", operation: MutationOperation{Type: "unset", Key: "EXISTING"}, created: true},
	{name: "identical set", operation: MutationOperation{Type: "set", Key: "EXISTING", Value: stringPointer("old")}, created: false},
	{name: "missing unset", operation: MutationOperation{Type: "unset", Key: "MISSING"}, created: false},
}
```

Use a fake authorizer that records the exact `*sql.Tx` passed by the revision service. In machineaccess tests, prove read grants fail writes, write grants succeed and still read, while invalid/expired/revoked/disabled tokens preserve current error classes.

- [ ] **Step 2: Run tests and verify RED**

```powershell
go test ./internal/revisions ./internal/machineaccess -run 'Test(MachineMutation|AuthorizeMachineWrite)' -count=1
```

Expected: FAIL because mutation types, service option, authorizer, and permission-aware query do not exist.

- [ ] **Step 3: Define the narrow interface and request/result types**

```go
type MachineWriteActor struct {
	IdentityID    string
	EnvironmentID string
}

type MachineWriteAuthorizer interface {
	AuthorizeMachineWrite(context.Context, *sql.Tx, string, string, string) (MachineWriteActor, error)
}

type ServiceOption func(*Service)

func WithMachineWriteAuthorizer(authorizer MachineWriteAuthorizer) ServiceOption {
	return func(service *Service) { service.machineWriteAuthorizer = authorizer }
}

type MutationOperation struct {
	Type    string  `json:"type"`
	Key     string  `json:"key"`
	Value   *string `json:"value,omitempty"`
	Service *string `json:"service,omitempty"`
}

type MachineMutationInput struct {
	BaseRevision int64             `json:"base_revision"`
	Message      string            `json:"message"`
	Operation    MutationOperation `json:"operation"`
}

type MutationResult struct {
	Project string `json:"project"`; Environment string `json:"environment"`
	Revision int64 `json:"revision"`; Created bool `json:"created"`
}
```

Make `NewService(store, options ...ServiceOption)` apply options while retaining all one-argument call sites.

- [ ] **Step 4: Implement read/write-aware transactional authorization**

Change `authenticateForProjectEnvironment` to accept `requiredPermission` and include:

```sql
AND (mg.permission = ? OR (? = 'read' AND mg.permission = 'write'))
```

Existing reads pass `GrantRead`. Implement:

```go
func (s *Service) AuthorizeMachineWrite(ctx context.Context, tx *sql.Tx,
	plaintext, projectSlug, environmentSlug string) (revisions.MachineWriteActor, error) {
	if err := s.ready(); err != nil || tx == nil { return revisions.MachineWriteActor{}, errors.New("machine write authorizer is unavailable") }
	hash, err := parseToken(plaintext)
	if err != nil { return revisions.MachineWriteActor{}, ErrInvalidToken }
	identity, environmentID, err := s.repository.authenticateForProjectEnvironment(
		ctx, tx, hash[:], projectSlug, environmentSlug, s.now().UTC().Unix(), GrantWrite)
	if err != nil { return revisions.MachineWriteActor{}, err }
	return revisions.MachineWriteActor{IdentityID: identity.ID, EnvironmentID: environmentID}, nil
}
```

- [ ] **Step 5: Validate and merge one operation**

Require set to have `Value != nil`; require unset to have neither value nor service; reject other types. Reuse existing key, UTF-8, byte, entry-count, and aggregate validators. Merge into a copy of the complete current entries, preserving an old service when `Service == nil`, using global service for a new key, sorting by key, and comparing key/value/service triples after normalization. Equal output returns the current version without creating a row.

- [ ] **Step 6: Coordinate everything in one immediate transaction**

Implement:

```go
func (s *Service) MutateForMachine(ctx context.Context, plaintext, projectSlug, environmentSlug string,
	input MachineMutationInput) (MutationResult, error)
```

Inside `store.InTx`, call the authorizer with that transaction, load the complete current revision by returned environment ID, compare `current.Version` with `BaseRevision`, validate/merge, and either return `Created:false` or create one machine-owned snapshot. Refactor creator input to:

```go
type revisionCreator struct { userID, machineIdentityID string }
```

User paths pass `revisionCreator{userID: currentActor.ID}`. Machine paths pass `revisionCreator{machineIdentityID: authorized.IdentityID}`. Insert both nullable columns and set public `CreatedBy`/`CreatedByType` from the non-empty creator.

- [ ] **Step 7: Wire production, run all Go tests, and commit**

Construct services in this order:

```go
machineService := machineaccess.NewService(store)
revisionService := revisions.NewService(store, revisions.WithMachineWriteAuthorizer(machineService))
```

Then run and commit:

```powershell
gofmt -w internal/revisions/service.go internal/revisions/service_test.go internal/machineaccess/service.go internal/machineaccess/repository.go internal/machineaccess/service_test.go cmd/server/main.go cmd/server/main_test.go
go test ./... -count=1
git add -- internal/revisions/service.go internal/revisions/service_test.go internal/machineaccess/service.go internal/machineaccess/repository.go internal/machineaccess/service_test.go cmd/server/main.go cmd/server/main_test.go
git commit -m "feat: apply transactional machine mutations"
```

---

### Task 4: Expose the Bearer-Only PATCH API

**Files:**
- Modify: `internal/httpapi/revision_handlers.go`
- Modify: `internal/httpapi/revision_handlers_test.go`
- Modify: `internal/httpapi/router.go`
- Modify: `internal/httpapi/middleware.go`
- Modify: `internal/httpapi/machine_handlers_test.go`

**Interfaces:**
- Consumes: `RevisionService.MutateForMachine(context.Context, string, string, string, revisions.MachineMutationInput) (revisions.MutationResult, error)`.
- Produces: bearer-only `PATCH /api/v1/projects/{project}/environments/{environment}/config`.
- Preserves: session PUT, bearer GET, and rejection of bearer credentials everywhere else.

- [ ] **Step 1: Add failing HTTP lifecycle, strictness, and routing tests**

Use a write grant to set, preserve/change service, unset, and no-op. Assert changed requests return `201` and exactly:

```json
{"project":"shop","environment":"production","revision":2,"created":true}
```

Assert no-op returns `200`, `created:false`, and no new history row. Add cases for read grant `403 scope_denied`, invalid token `401 invalid_token`, stale base `409 revision_conflict`, unknown JSON fields/duplicates `400 malformed_request`, and invalid operation combinations `422 validation_failed`. Extend fallback tests to require `Allow: GET, PATCH, PUT`; bearer PUT must remain rejected.

- [ ] **Step 2: Run focused tests and verify RED**

```powershell
go test ./internal/httpapi -run 'Test(MachineMutation|AuthorizationGuard|MachineRoutesOptionalFallback|OptionalRevisionRoutes)' -count=1
```

Expected: FAIL because PATCH is unregistered and Authorization permits only GET.

- [ ] **Step 3: Register PATCH and restrict the bearer surface by ServeMux pattern**

Extend `RevisionService`, register `PATCH .../config`, and change the config Allow string. Define exact GET and PATCH route-pattern constants and accept Authorization only when `mux.Handler(r)` selects one of them; do not authorize by raw path alone.

- [ ] **Step 4: Decode, delegate, minimize, and map errors**

`revisionHandlers.mutate` must parse one strict bearer token, reject query parameters, decode `MachineMutationInput` through existing strict JSON logic, and call `MutateForMachine`. Use status 201 when `Created` and 200 otherwise, writing `MutationResult` directly. Map machine invalid token first, scope denied second, revision conflict third, then normal revision validation/operational errors. Do not accept cookies, CSRF, redirects, or session fallback on PATCH.

- [ ] **Step 5: Run HTTP/full Go tests and commit**

```powershell
gofmt -w internal/httpapi/revision_handlers.go internal/httpapi/revision_handlers_test.go internal/httpapi/router.go internal/httpapi/middleware.go internal/httpapi/machine_handlers_test.go
go test ./internal/httpapi -count=1
go test ./... -count=1
git add -- internal/httpapi/revision_handlers.go internal/httpapi/revision_handlers_test.go internal/httpapi/router.go internal/httpapi/middleware.go internal/httpapi/machine_handlers_test.go
git commit -m "feat: expose machine config patch API"
```

---

### Task 5: Add the Strict CLI Mutation Client

**Files:**
- Create: `internal/cli/mutation_client.go`
- Create: `internal/cli/mutation_client_test.go`
- Modify: `internal/cli/client.go` only if a common bounded-response helper is extracted after both paths are green.

**Interfaces:**
- Produces: `MutationOperation`, `MutationRequest`, `MutationResponse`, `(*Client).MutateConfig` in package `cli`.
- Consumes: Task 4 PATCH and minimized response.

- [ ] **Step 1: Add failing transport and strict-response tests**

With `httptest.Server`, verify PATCH method, safe URL-prefix join, bearer/accept/content-type headers, and exact JSON. Decode the captured body and assert service is absent for nil but present as `""` for explicit global. Check values only by boolean equality so failure text cannot disclose them.

Accept exact 200/no-op and 201/created responses. Reject extra/duplicate fields, wrong project/environment, status/created mismatch, revision jumps, malformed Unicode, oversized bodies, redirects, canceled contexts, response-read failure, and typed API errors. A sentinel token/value must be absent from every returned error string.

- [ ] **Step 2: Run tests and verify RED**

```powershell
go test ./internal/cli -run 'TestClientMutateConfig' -count=1
```

Expected: FAIL because mutation client types/method do not exist.

- [ ] **Step 3: Implement request types and safe PATCH creation**

```go
type MutationOperation struct {
	Type string `json:"type"`; Key string `json:"key"`
	Value *string `json:"value,omitempty"`; Service *string `json:"service,omitempty"`
}
type MutationRequest struct {
	BaseRevision int64 `json:"base_revision"`; Message string `json:"message"`
	Operation MutationOperation `json:"operation"`
}
type MutationResponse struct {
	Project string `json:"project"`; Environment string `json:"environment"`
	Revision int64 `json:"revision"`; Created bool `json:"created"`
}
```

`MutateConfig` validates client state, slugs, base revision, key, UTF-8, and byte limits before using the same safe config URL construction as `FetchConfig`. Set `Accept`, `Content-Type`, and bearer Authorization; never include marshaled request data in errors.

- [ ] **Step 4: Strictly validate minimized responses**

Use the existing 8 MiB limit and context-error precedence. Non-200/201 statuses go through `decodeAPIError`. Require exactly four fields and validate:

```go
if payload.Project != project || payload.Environment != environment { return MutationResponse{}, errInvalidResponse }
if status == http.StatusCreated && (!payload.Created || payload.Revision != request.BaseRevision+1) { return MutationResponse{}, errInvalidResponse }
if status == http.StatusOK && (payload.Created || payload.Revision != request.BaseRevision) { return MutationResponse{}, errInvalidResponse }
```

Reject `BaseRevision == math.MaxInt64` before the addition. If common response code is extracted, rerun all existing client tests after the refactor.

- [ ] **Step 5: Run CLI tests and commit**

```powershell
gofmt -w internal/cli/mutation_client.go internal/cli/mutation_client_test.go internal/cli/client.go
go test ./internal/cli -count=1
git add -- internal/cli/mutation_client.go internal/cli/mutation_client_test.go internal/cli/client.go
git commit -m "feat: add CLI mutation client"
```

---

### Task 6: Add `confighub set` and `confighub unset`

**Files:**
- Create: `internal/cli/mutation_command.go`
- Create: `internal/cli/mutation_command_test.go`
- Modify: `internal/cli/root.go`
- Modify: `internal/cli/root_test.go`

**Interfaces:**
- Produces: `confighub set --project P --env E [--service S] [--message M] KEY=VALUE`.
- Produces: `confighub unset --project P --env E [--message M] KEY`.

- [ ] **Step 1: Add failing real-command HTTP fixture tests**

Test exact GET then PATCH ordering for set with a value containing `=`, set with empty value, omitted service, `--service=`, custom/default message, and unset. GET returns revision 7; PATCH returns revision 8/created; require `revision 8\n` and empty stderr.

Add no-request cases for missing/extra args, missing `=`, invalid key, invalid service/message/value UTF-8 or size, and missing credentials. A 409 test must count exactly one GET plus one PATCH, return exit 1, write no stdout, and show only safe status/code/request ID. Add stdout short-write, network, and API response leak tests.

- [ ] **Step 2: Run tests and verify RED**

```powershell
go test ./internal/cli -run 'Test(Set|Unset|MutationCommand)' -count=1
```

Expected: FAIL with invalid command usage because commands are unregistered.

- [ ] **Step 3: Implement local parsing and validation**

```go
func parseSetArgument(raw string) (string, string, error) {
	key, value, found := strings.Cut(raw, "=")
	if !found || !environmentKeyPattern.MatchString(key) || !utf8.ValidString(value) || len(value) > maxMutationValueBytes {
		return "", "", errLocalInput
	}
	return key, value, nil
}
```

Define the mirrored server limits in this focused command file:

```go
const (
	maxMutationValueBytes   = 1 << 20
	maxMutationMessageBytes = 1024
)
```

Validate unset key locally. Validate service as trimmed UTF-8 at most 128 bytes and message as trimmed UTF-8 at most `maxMutationMessageBytes`. Empty values and explicitly empty service remain valid.

- [ ] **Step 4: Implement commands using layered configuration**

Resolve server/token exactly as export/run do, instantiate `Client`, perform one unfiltered `FetchConfig` to obtain base revision, then one `MutateConfig`. Use `command.Flags().Changed("service")`:

```go
var servicePointer *string
if command.Flags().Changed("service") { servicePointer = &service }
```

Default message is `Set <KEY> via CLI` or `Unset <KEY> via CLI`. Do not retry. Write success through `writeCLICommandOutput(stdout, fmt.Sprintf("revision %d\n", result.Revision))`.

- [ ] **Step 5: Add safe mutation diagnostics and root registration**

Preserve typed API diagnostics. For default runtime failures, retain operation name so set/unset report `confighub: set failed`/`confighub: unset failed`; existing timeout, cancel, network, invalid-response, size, read, and stdout messages remain unchanged. Change root short text to `Read and write configuration with ConfigHub`.

- [ ] **Step 6: Run CLI/build tests and commit**

```powershell
gofmt -w internal/cli/mutation_command.go internal/cli/mutation_command_test.go internal/cli/root.go internal/cli/root_test.go
go test ./internal/cli ./cmd/cli -count=1
go build ./cmd/cli
git add -- internal/cli/mutation_command.go internal/cli/mutation_command_test.go internal/cli/root.go internal/cli/root_test.go
git commit -m "feat: add CLI set and unset commands"
```

---

### Task 7: Add Permission and Machine Attribution UI

**Files:**
- Modify: `web/src/api/types.ts`
- Modify: `web/src/pages/MachineAccessPage.tsx`
- Modify: `web/src/pages/MachineAccessPage.test.tsx`
- Modify: `web/src/features/versions/VersionList.tsx`
- Modify: `web/src/features/versions/VersionList.test.tsx`
- Modify: `web/src/i18n/resources/machineAccess.ts`
- Modify: `web/src/i18n/resources/versions.ts`
- Modify: typed revision fixtures in `web/src/pages/ProjectPage.test.tsx`, `web/src/features/config/ConfigEditor.test.tsx`, `web/src/features/config/ConfigTable.test.tsx`.
- Modify: `web/e2e/core-flow.spec.ts`
- Modify: `DESIGN.md` only if the required frontend skills identify a durable new visual rule.

**Interfaces:**
- Consumes: grant `permission: "read" | "write"`, revision `created_by_type: "user" | "machine"`.
- Produces: safe read default, writable selection, localized saved permission, and machine actor label.

- [ ] **Step 1: Read required frontend skills and add failing tests**

Read both frontend skills named in Global Constraints. Change the grant request test to select Write and expect:

```ts
expect(requestBody).toEqual({ grants: [{
  project_id: "project-shop",
  environment_id: "environment-prod",
  permission: "write",
}] });
```

Add tests for default Read, saved read/write labels, locale switching without draft loss, and a machine revision rendered as `Machine machine-ci`/`机器 machine-ci`.

- [ ] **Step 2: Run focused tests and verify RED**

```powershell
npm test --prefix web -- MachineAccessPage.test.tsx VersionList.test.tsx
```

Expected: FAIL because permission controls/types/copy and machine attribution rendering do not exist.

- [ ] **Step 3: Add strict frontend types and guards**

```ts
export type MachineGrantPermission = "read" | "write";
export interface MachineEnvironmentGrant {
  project_id: string;
  environment_id: string;
  permission: MachineGrantPermission;
}
export type RevisionActorType = "user" | "machine";
```

Add required `created_by_type` to `RevisionSummary`, require read/write in `isGrant`, and update every typed user-revision fixture with `created_by_type: "user"`; do not make the field optional.

- [ ] **Step 4: Add permission selector and saved label**

Initialize permission state to read. Render a labeled read/write select beside project/environment, include permission when adding a grant, and display it in saved rows. Project+environment remains the unique grant key; changing permission replaces that row.

Add bilingual copy:

```ts
// en-US
permission: "Permission",
permissions: { read: "Read", write: "Read and write" },
label: "{{project}} / {{environment}} · {{permission}}",
// zh-CN
permission: "权限",
permissions: { read: "只读", write: "读写" },
label: "{{project}} / {{environment}} · {{permission}}",
```

- [ ] **Step 5: Display machine revision actors distinctly**

```tsx
<dd>{revision.created_by_type === "machine"
  ? t("register.machineCreatedBy", { id: revision.created_by })
  : revision.created_by}</dd>
```

Add `Machine {{id}}` and `机器 {{id}}` to versions resources.

- [ ] **Step 6: Update browser flow and run all frontend checks**

After creating the browser identity, select an existing project/environment plus Write, save it, and assert the saved row contains `Read and write`. Then run:

```powershell
npm run typecheck --prefix web
npm test --prefix web
npm run build --prefix web
npm run e2e --prefix web -- --grep "admin completes configuration"
```

Expected: PASS with translation parity, stable layout, accessible labels, and no secret leakage.

- [ ] **Step 7: Commit Task 7**

Stage only files actually changed; include `DESIGN.md` only if the frontend skills required a durable update:

```powershell
git add -- web/src/api/types.ts web/src/pages/MachineAccessPage.tsx web/src/pages/MachineAccessPage.test.tsx web/src/features/versions/VersionList.tsx web/src/features/versions/VersionList.test.tsx web/src/i18n/resources/machineAccess.ts web/src/i18n/resources/versions.ts web/src/pages/ProjectPage.test.tsx web/src/features/config/ConfigEditor.test.tsx web/src/features/config/ConfigTable.test.tsx web/e2e/core-flow.spec.ts
git commit -m "feat: manage machine write grants"
```

---

### Task 8: Prove Runtime Workflow and Document Writable CLI

**Files:**
- Modify: `internal/acceptance/runtime_test.go`
- Modify: `README.md`

**Interfaces:**
- Consumes: production server wiring, write grants, CLI set/unset, machine reads, revision history.
- Produces: real-process acceptance proof and user-facing security contract.

- [ ] **Step 1: Add failing real runtime set/unset flow**

Change the production grant to explicit write. After existing rollback reaches revision 3, register a CLI-only secret with the leak guard, execute set, require `revision 4\n`, export with its service and verify without printing the value on failure, execute unset, require `revision 5\n`, and export again to prove absence. Fetch history through the admin session and require versions 4/5 to have `created_by_type == "machine"` and `created_by == identity.Identity.ID`.

Representative set call:

```go
setArg := "CLI_ONLY=" + cliOnlyValue
code := executeCLI(cli.Execute, ctx, []string{
	"set", "--project", "shop", "--env", "production", "--service", "worker", setArg,
}, getenv, &mutationOutput, &cliDiagnostics)
if code != 0 { t.Fatalf("set exit=%d", code) }
if mutationOutput.String() != "revision 4\n" { t.Fatalf("set output=%q", mutationOutput.String()) }
```

- [ ] **Step 2: Run runtime test and close integration gaps**

```powershell
go test ./internal/acceptance -run TestRuntimeWorkflow -count=1 -v
```

Expected: PASS once all production boundaries are wired; any failure message and captured log remains free of registered values/tokens.

- [ ] **Step 3: Update README contracts**

Remove read-only product wording. Document write-grant requirement, old-grant read migration, revision creation/no-op, conflict non-retry, exact output, and examples:

```bash
./dist/confighub set --project shop --env production DATABASE_URL=postgres://db.internal/app
./dist/confighub set --project shop --env production --service api PORT=8080
./dist/confighub unset --project shop --env production LEGACY_FLAG
```

Place this warning beside them:

```text
`set KEY=VALUE` 会把配置值放入命令行；shell history 和同机进程检查可能看到该值。不要把这种输入方式视为秘密安全通道。
```

Remove “机器写入配置” from non-goals; leave bulk files, approval workflows, sidecars, and dynamic credentials out of scope.

- [ ] **Step 4: Run focused and full platform-neutral checks**

```powershell
gofmt -w internal/acceptance/runtime_test.go
go test ./internal/acceptance -run TestRuntimeWorkflow -count=1 -v
go test ./... -count=1
npm run typecheck --prefix web
npm test --prefix web
npm run build --prefix web
git diff --check
```

Expected: PASS.

- [ ] **Step 5: Run the supported full quality gate**

From Linux/Bash:

```bash
./scripts/check.sh
```

Expected: PASS for toolchain, gofmt, vet, race, shell tests, frontend checks/build, both Go builds, Chromium E2E, and runtime acceptance. On Windows, record Linux-only Bash/PTY steps as requiring the supported environment instead of claiming success.

- [ ] **Step 6: Commit Task 8 and perform completion review**

```powershell
git add -- internal/acceptance/runtime_test.go README.md
git commit -m "docs: explain writable machine CLI"
```

Confirm `.coder-studio/` remains unstaged. Re-read the spec and map every requirement to green evidence, then invoke `superpowers:verification-before-completion` before reporting success.
