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

CREATE TRIGGER environments_seal_current_revision_insert
AFTER INSERT ON environments
WHEN NEW.current_revision_id IS NOT NULL
BEGIN
    UPDATE revisions
    SET sealed = 1
    WHERE id = NEW.current_revision_id AND environment_id = NEW.id AND sealed = 0;
END;

CREATE TRIGGER environments_seal_current_revision_update
AFTER UPDATE OF current_revision_id ON environments
WHEN NEW.current_revision_id IS NOT NULL
BEGIN
    UPDATE revisions
    SET sealed = 1
    WHERE id = NEW.current_revision_id AND environment_id = NEW.id AND sealed = 0;
END;

CREATE TRIGGER revisions_prevent_direct_delete
BEFORE DELETE ON revisions
WHEN EXISTS (
    SELECT 1 FROM environments WHERE id = OLD.environment_id
)
BEGIN
    SELECT RAISE(ABORT, 'cannot delete revision while environment exists');
END;

CREATE TRIGGER revisions_prevent_update
BEFORE UPDATE ON revisions
WHEN NOT (
    OLD.sealed = 0
    AND NEW.sealed = 1
    AND NEW.id IS OLD.id
    AND NEW.environment_id IS OLD.environment_id
    AND NEW.version IS OLD.version
    AND NEW.message IS OLD.message
    AND NEW.created_by IS OLD.created_by
    AND NEW.created_by_machine_identity_id IS OLD.created_by_machine_identity_id
    AND NEW.created_at IS OLD.created_at
)
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

CREATE TRIGGER revision_entries_prevent_sealed_insert
BEFORE INSERT ON revision_entries
WHEN EXISTS (
    SELECT 1 FROM revisions WHERE id = NEW.revision_id AND sealed = 1
)
BEGIN
    SELECT RAISE(ABORT, 'sealed revision entries are immutable');
END;

CREATE TRIGGER revision_entries_prevent_sealed_update
BEFORE UPDATE ON revision_entries
WHEN EXISTS (
    SELECT 1 FROM revisions
    WHERE id IN (OLD.revision_id, NEW.revision_id) AND sealed = 1
)
BEGIN
    SELECT RAISE(ABORT, 'sealed revision entries are immutable');
END;

CREATE TRIGGER revision_entries_prevent_sealed_delete
BEFORE DELETE ON revision_entries
WHEN EXISTS (
    SELECT 1 FROM revisions WHERE id = OLD.revision_id AND sealed = 1
)
BEGIN
    SELECT RAISE(ABORT, 'sealed revision entries are immutable');
END;
