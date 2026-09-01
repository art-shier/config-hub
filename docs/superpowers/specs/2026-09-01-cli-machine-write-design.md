# ConfigHub CLI Machine-Token Write Design

**Date:** 2026-09-01

**Status:** Approved in conversation; awaiting written-spec review

## Goal

Allow the ConfigHub CLI to add, update, and remove individual configuration entries by using the existing machine-token authentication mechanism. Machine writes must preserve revision history, service metadata, concurrent-edit protection, least-privilege authorization, and the existing rule that diagnostics never disclose tokens or configuration values.

## Scope

This change adds:

- environment-level `read` and `write` permissions for machine identity grants;
- machine-authenticated, single-key `set` and `unset` mutations on the current configuration;
- `confighub set KEY=VALUE` and `confighub unset KEY` commands;
- machine attribution in revision history;
- management UI support for selecting and displaying machine grant permission;
- database migration, API, CLI, UI, documentation, and end-to-end verification.

This change does not add bulk imports, machine rollback, revision-history access for machine tokens, multi-key mutation commands, secret redaction, or a new credential transport. The selected `KEY=VALUE` syntax can expose the value in shell history and process listings; the CLI and README will warn about that trade-off without introducing a second value-input mechanism in this scope.

## Authorization Model

Each `machine_grants` row gains a `permission` value:

- `read` permits the existing configuration reads;
- `write` permits reads plus the new single-key mutations.

All grants that exist when the migration runs become `read`. Old machine tokens therefore retain their existing behavior and never acquire write access merely because the server was upgraded.

The machine grant management API includes `permission` in each grant. For compatibility, a grant request that omits `permission` is interpreted as `read`; unknown permission values are rejected with field-level validation. The management UI makes permission explicit when adding a grant and shows it on every saved grant.

Token validity, identity enabled state, environment grant, and required permission are rechecked inside the same database transaction that creates a revision. Revoking a token, disabling an identity, or changing its grant before transactional authorization completes prevents the write.

## Revision Attribution and Migration

The current `revisions.created_by` column can reference only a user. Revision attribution changes to two nullable foreign-key columns, one referencing `users` and one referencing `machine_identities`, with a database check that exactly one is populated. The public revision representation continues to expose `created_by` and additionally exposes `created_by_type` with the value `user` or `machine`.

The migration atomically rebuilds the affected revision schema while preserving revision IDs, environment current-revision pointers, entries, version numbers, timestamps, messages, and sealed state. Every historical revision is copied with its existing creator in the user attribution column. New machine mutations store the authenticated machine identity in the machine attribution column. Revision history must never attribute a machine mutation to a synthetic or unrelated administrator.

The migration also adds the checked machine-grant permission column with a default of `read`. Migration tests cover both a fresh database and an existing version-1 database populated with users, revisions, entries, machine identities, grants, and tokens.

## HTTP API

The existing configuration resource gains `PATCH` support:

```text
PATCH /api/v1/projects/{project}/environments/{environment}/config
Authorization: Bearer <machine-token>
Content-Type: application/json
```

`PATCH` is machine-token-only. The existing session-authenticated `PUT` behavior used by the web editor remains unchanged. The route accepts no query parameters.

A set request is:

```json
{
  "base_revision": 12,
  "message": "Set DATABASE_URL via CLI",
  "operation": {
    "type": "set",
    "key": "DATABASE_URL",
    "value": "postgres://example",
    "service": "api"
  }
}
```

An unset request is:

```json
{
  "base_revision": 12,
  "message": "Unset DATABASE_URL via CLI",
  "operation": {
    "type": "unset",
    "key": "DATABASE_URL"
  }
}
```

The body uses the existing strict JSON rules: malformed JSON, duplicate fields, unknown fields, trailing data, and the wrong field set for an operation are rejected. Existing revision limits apply to keys, values, services, messages, entry count, and snapshot size.

For `set`, omission of `service` preserves the service on an existing key and selects the global service for a new key. A present empty `service` explicitly moves an entry to the global service. A present non-empty value assigns that service. Because keys are unique within a revision, `unset` removes the key regardless of its current service.

The response contains only project, environment, resulting revision number, and whether a new revision was created. It never returns the submitted value or the full snapshot. A content-identical set or an unset of a missing key succeeds without creating a revision and reports the current revision. A successful mutation that changes content creates exactly one immutable revision and returns HTTP `201`; an idempotent no-op returns HTTP `200`.

## Transaction and Component Boundaries

The HTTP handler parses authentication and strict input, delegates the mutation, and maps domain errors. It does not merge snapshots itself.

Machine access remains responsible for parsing and hashing tokens and for checking token, identity, grant, and permission state. Revision logic remains responsible for validating mutations, loading the complete current snapshot, preserving unaffected entries, applying one operation, detecting a no-op, checking the base revision, and creating the next immutable snapshot.

The two domains meet through a narrow internal machine-write authorization interface used by revision mutation logic. The revision service owns the database transaction and invokes machine authorization with that transaction before reading or writing revision state. This avoids duplicated snapshot-writing logic and prevents a time-of-check/time-of-use gap without introducing a package import cycle.

Within the transaction, processing is:

1. Parse and validate the token without disclosing it in errors.
2. Resolve the project and environment and require a `write` grant.
3. Load the complete current revision and compare it with `base_revision`.
4. Apply the single-key operation while preserving all unaffected entries and service fields.
5. Return the current revision for a no-op, or create and seal one new machine-attributed revision.
6. Atomically advance the environment current-revision pointer and commit.

No automatic retry follows a revision conflict.

## CLI Contract

The new commands are:

```text
confighub set \
  --project shop \
  --env production \
  [--service api] \
  [--message "..."] \
  KEY=VALUE

confighub unset \
  --project shop \
  --env production \
  [--message "..."] \
  KEY
```

Both commands use the existing layered server URL and token-file configuration. They accept exactly one positional mutation. `set` splits its argument at the first `=` so values may be empty or contain additional equals signs. Keys are validated locally before any request. `unset` does not accept a service flag.

For `set`, an omitted `--service` omits the JSON service field, preserving an existing assignment. An explicitly present `--service=` sends an empty service and moves the key to global scope. The CLI uses Cobra's changed-flag state to distinguish those cases.

When no message is supplied, the CLI uses `Set <KEY> via CLI` or `Unset <KEY> via CLI`. It first performs the existing unfiltered current-config read to obtain the base revision, then sends the mutation. It never retries a `409`, because doing so could overwrite a concurrent change to the same key.

Successful output is exactly:

```text
revision <number>
```

The CLI never prints the submitted value. Existing exit conventions remain: command usage and local validation failures return `2`; network, server, permission, and conflict failures return `1`; success returns `0`.

## Errors and Security

The API uses the existing error envelope and these status/code pairs:

- `401 invalid_token` for malformed, expired, revoked, or otherwise invalid tokens;
- `403 scope_denied` when the identity lacks the environment or `write` permission;
- `409 revision_conflict` when `base_revision` is stale;
- `422 validation_failed` for invalid keys, values, services, messages, operations, or snapshot limits;
- `400 malformed_request` for invalid strict JSON or structurally invalid bodies.

CLI diagnostics remain sanitized and may include only safe HTTP status, error code, and request ID fields. Tokens, submitted values, response bodies, and complete configuration snapshots must not appear in diagnostics, logs, or test failure messages. Redirects remain disabled, request and response size limits remain enforced, and context cancellation and timeouts preserve their existing classifications.

Because the requested command syntax places the configuration value on the command line, documentation explicitly warns that shells and operating-system process inspection can expose it. This is an accepted product trade-off for this version, not a claim that command-line values are secret-safe.

## Management UI

The machine access page adds a permission selector to the grant picker with `read` as the safe default. Saved grant rows show a localized read/write label. Editing and saving an existing pre-migration grant preserves `read` unless an administrator explicitly changes it to `write`.

The UI updates its runtime response validation and TypeScript types for the permission field and revision actor type. All new labels, help text, validation feedback, and permission badges are provided for every supported locale. Layout must remain stable for long translated strings and narrow viewports.

## Verification

Implementation follows red-green-refactor. Focused tests cover:

- fresh and populated-database migrations, including preservation of revisions and default-read grants;
- grant validation and read/write permission serialization;
- read grants continuing to read but failing writes;
- write grants supporting both reads and writes;
- invalid, expired, revoked, and disabled machine credentials;
- grant or token changes observed by transactional authorization;
- stale base revisions and the absence of automatic retry;
- set of new and existing keys, empty values, values containing `=`, and service preserve/change/global behavior;
- unset of existing and missing keys;
- no-op mutations not creating revisions;
- exact machine revision attribution;
- strict request decoding, method routing, response minimization, and error mapping;
- CLI validation, layered connection configuration, request construction, sanitized diagnostics, exact output, and exit codes;
- management UI permission selection, display, response validation, localization, and resilient interaction states;
- an acceptance flow that grants write access, runs CLI set and unset against a real server, and verifies results through a machine read and revision history.

The final verification runs Go formatting and vet, focused and full Go tests including race coverage where supported, frontend type checking and unit tests, production builds, and the repository's complete quality gate.
