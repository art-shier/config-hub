CREATE TABLE IF NOT EXISTS schema_migrations (
    version INTEGER PRIMARY KEY,
    applied_at INTEGER NOT NULL
);

CREATE TABLE users (
    id TEXT PRIMARY KEY,
    username TEXT NOT NULL UNIQUE,
    display_name TEXT NOT NULL,
    password_hash TEXT NOT NULL,
    role TEXT NOT NULL CHECK (role IN ('admin', 'member')),
    enabled INTEGER NOT NULL CHECK (enabled IN (0, 1)),
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);

CREATE TABLE sessions (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash BLOB NOT NULL UNIQUE,
    csrf_hash BLOB NOT NULL,
    expires_at INTEGER NOT NULL,
    created_at INTEGER NOT NULL
);

CREATE TABLE projects (
    id TEXT PRIMARY KEY,
    slug TEXT NOT NULL UNIQUE,
    name TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    created_by TEXT NOT NULL REFERENCES users(id),
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);

CREATE TABLE project_members (
    project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    permission TEXT NOT NULL CHECK (permission IN ('viewer', 'editor')),
    PRIMARY KEY (project_id, user_id)
);

CREATE TABLE environments (
    id TEXT PRIMARY KEY,
    project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    slug TEXT NOT NULL,
    name TEXT NOT NULL,
    current_revision_id TEXT,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    UNIQUE (project_id, slug)
);

CREATE TABLE revisions (
    id TEXT PRIMARY KEY,
    environment_id TEXT NOT NULL REFERENCES environments(id) ON DELETE CASCADE,
    version INTEGER NOT NULL,
    message TEXT NOT NULL DEFAULT '',
    created_by TEXT NOT NULL REFERENCES users(id),
    created_at INTEGER NOT NULL,
    UNIQUE (environment_id, version)
);

CREATE TABLE revision_entries (
    revision_id TEXT NOT NULL REFERENCES revisions(id) ON DELETE CASCADE,
    key TEXT NOT NULL,
    value TEXT NOT NULL,
    service TEXT,
    PRIMARY KEY (revision_id, key)
);

CREATE TABLE machine_identities (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL UNIQUE,
    description TEXT NOT NULL DEFAULT '',
    enabled INTEGER NOT NULL CHECK (enabled IN (0, 1)),
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);

CREATE TABLE machine_grants (
    identity_id TEXT NOT NULL REFERENCES machine_identities(id) ON DELETE CASCADE,
    project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    environment_id TEXT NOT NULL REFERENCES environments(id) ON DELETE CASCADE,
    PRIMARY KEY (identity_id, project_id, environment_id)
);

CREATE TABLE access_tokens (
    id TEXT PRIMARY KEY,
    identity_id TEXT NOT NULL REFERENCES machine_identities(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    prefix TEXT NOT NULL,
    token_hash BLOB NOT NULL UNIQUE,
    expires_at INTEGER NOT NULL,
    revoked_at INTEGER,
    created_at INTEGER NOT NULL,
    UNIQUE (identity_id, name)
);

CREATE INDEX sessions_user_id_idx ON sessions(user_id);
CREATE INDEX projects_created_by_idx ON projects(created_by);
CREATE INDEX project_members_user_id_idx ON project_members(user_id);
CREATE INDEX environments_project_id_idx ON environments(project_id);
CREATE INDEX revisions_environment_id_idx ON revisions(environment_id);
CREATE INDEX revisions_created_by_idx ON revisions(created_by);
CREATE INDEX machine_grants_project_id_idx ON machine_grants(project_id);
CREATE INDEX machine_grants_environment_id_idx ON machine_grants(environment_id);
CREATE INDEX access_tokens_identity_id_idx ON access_tokens(identity_id);

CREATE TRIGGER environments_prevent_replace
BEFORE INSERT ON environments
WHEN EXISTS (
    SELECT 1 FROM environments
    WHERE id = NEW.id
       OR (project_id = NEW.project_id AND slug = NEW.slug)
)
BEGIN
    SELECT RAISE(ABORT, 'environments cannot be replaced');
END;

CREATE TRIGGER environments_prevent_update_replace
BEFORE UPDATE OF id, project_id, slug ON environments
WHEN EXISTS (
    SELECT 1 FROM environments
    WHERE id <> OLD.id
      AND (id = NEW.id OR (project_id = NEW.project_id AND slug = NEW.slug))
)
BEGIN
    SELECT RAISE(ABORT, 'environment update conflicts with existing environment');
END;

CREATE TRIGGER environments_current_revision_insert
BEFORE INSERT ON environments
WHEN NEW.current_revision_id IS NOT NULL
 AND NOT EXISTS (
    SELECT 1 FROM revisions
    WHERE id = NEW.current_revision_id AND environment_id = NEW.id
 )
BEGIN
    SELECT RAISE(ABORT, 'current revision must belong to environment');
END;

CREATE TRIGGER environments_current_revision_update
BEFORE UPDATE OF current_revision_id, id ON environments
WHEN NEW.current_revision_id IS NOT NULL
 AND NOT EXISTS (
    SELECT 1 FROM revisions
    WHERE id = NEW.current_revision_id AND environment_id = NEW.id
 )
BEGIN
    SELECT RAISE(ABORT, 'current revision must belong to environment');
END;

CREATE TRIGGER revisions_prevent_current_delete
BEFORE DELETE ON revisions
WHEN EXISTS (
    SELECT 1 FROM environments WHERE current_revision_id = OLD.id
)
BEGIN
    SELECT RAISE(ABORT, 'cannot delete current revision');
END;

CREATE TRIGGER revisions_prevent_update
BEFORE UPDATE ON revisions
BEGIN
    SELECT RAISE(ABORT, 'revisions are immutable');
END;

CREATE TRIGGER revisions_prevent_replace
BEFORE INSERT ON revisions
WHEN EXISTS (
    SELECT 1 FROM revisions
    WHERE id = NEW.id
       OR (environment_id = NEW.environment_id AND version = NEW.version)
)
BEGIN
    SELECT RAISE(ABORT, 'revisions are immutable');
END;

CREATE TRIGGER machine_grants_project_environment_insert
BEFORE INSERT ON machine_grants
WHEN NOT EXISTS (
    SELECT 1 FROM environments
    WHERE id = NEW.environment_id AND project_id = NEW.project_id
)
BEGIN
    SELECT RAISE(ABORT, 'machine grant environment must belong to project');
END;

CREATE TRIGGER machine_grants_project_environment_update
BEFORE UPDATE OF project_id, environment_id ON machine_grants
WHEN NOT EXISTS (
    SELECT 1 FROM environments
    WHERE id = NEW.environment_id AND project_id = NEW.project_id
)
BEGIN
    SELECT RAISE(ABORT, 'machine grant environment must belong to project');
END;

CREATE TRIGGER environments_prevent_grant_project_mismatch
BEFORE UPDATE OF project_id ON environments
WHEN EXISTS (
    SELECT 1 FROM machine_grants
    WHERE environment_id = OLD.id AND project_id <> NEW.project_id
)
BEGIN
    SELECT RAISE(ABORT, 'environment project must match machine grants');
END;
