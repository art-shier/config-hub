package revisions

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"confighub.local/internal/auth"
	"confighub.local/internal/database"
)

type repository struct {
	store *database.Store
}

type queryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

type environmentRecord struct {
	ID, ProjectID, ProjectSlug, Slug, Grant string
}

func newRepository(store *database.Store) *repository {
	return &repository{store: store}
}

func (r *repository) currentActor(ctx context.Context, q queryer, actorID string) (auth.User, error) {
	var actor auth.User
	var enabled int
	err := q.QueryRowContext(ctx, `SELECT id, username, display_name, role, enabled FROM users WHERE id = ?`, actorID).
		Scan(&actor.ID, &actor.Username, &actor.DisplayName, &actor.Role, &enabled)
	if errors.Is(err, sql.ErrNoRows) {
		return auth.User{}, ErrForbidden
	}
	if err != nil {
		return auth.User{}, database.ClassifyError(fmt.Errorf("load revision actor: %w", err))
	}
	actor.Enabled = enabled == 1
	if !actor.Enabled {
		return auth.User{}, ErrForbidden
	}
	return actor, nil
}

func (r *repository) environmentByID(ctx context.Context, q queryer, environmentID, actorID string) (environmentRecord, error) {
	return scanEnvironmentRecord(q.QueryRowContext(ctx, `SELECT e.id, e.project_id, p.slug, e.slug, COALESCE(pm.permission, '')
		FROM environments e
		JOIN projects p ON p.id = e.project_id
		LEFT JOIN project_members pm ON pm.project_id = p.id AND pm.user_id = ?
		WHERE e.id = ?`, actorID, environmentID))
}

func (r *repository) environmentBySlugs(ctx context.Context, q queryer, projectSlug, environmentSlug, actorID string) (environmentRecord, error) {
	return scanEnvironmentRecord(q.QueryRowContext(ctx, `SELECT e.id, e.project_id, p.slug, e.slug, COALESCE(pm.permission, '')
		FROM projects p
		JOIN environments e ON e.project_id = p.id
		LEFT JOIN project_members pm ON pm.project_id = p.id AND pm.user_id = ?
		WHERE p.slug = ? AND e.slug = ?`, actorID, projectSlug, environmentSlug))
}

func scanEnvironmentRecord(row *sql.Row) (environmentRecord, error) {
	var environment environmentRecord
	err := row.Scan(&environment.ID, &environment.ProjectID, &environment.ProjectSlug, &environment.Slug, &environment.Grant)
	if errors.Is(err, sql.ErrNoRows) {
		return environmentRecord{}, ErrNotFound
	}
	if err != nil {
		return environmentRecord{}, database.ClassifyError(fmt.Errorf("load revision environment: %w", err))
	}
	return environment, nil
}

func (r *repository) currentRevision(ctx context.Context, q queryer, environmentID, service string) (Revision, error) {
	var revision Revision
	var currentID sql.NullString
	err := q.QueryRowContext(ctx, `SELECT current_revision_id FROM environments WHERE id = ?`, environmentID).Scan(&currentID)
	if errors.Is(err, sql.ErrNoRows) {
		return Revision{}, ErrNotFound
	}
	if err != nil {
		return Revision{}, database.ClassifyError(fmt.Errorf("load current revision pointer: %w", err))
	}
	if !currentID.Valid {
		return Revision{EnvironmentID: environmentID, Entries: []Entry{}}, nil
	}
	revision, err = r.revisionByID(ctx, q, currentID.String, environmentID, service)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return Revision{}, ErrDataIntegrity
		}
		return Revision{}, err
	}
	if revision.Version <= 0 {
		return Revision{}, ErrDataIntegrity
	}
	return revision, nil
}

func (r *repository) revisionByVersion(ctx context.Context, q queryer, environmentID string, version int64) (Revision, error) {
	var revisionID string
	err := q.QueryRowContext(ctx, `SELECT id FROM revisions WHERE environment_id = ? AND version = ?`, environmentID, version).Scan(&revisionID)
	if errors.Is(err, sql.ErrNoRows) {
		return Revision{}, ErrNotFound
	}
	if err != nil {
		return Revision{}, database.ClassifyError(fmt.Errorf("load revision version: %w", err))
	}
	revision, err := r.revisionByID(ctx, q, revisionID, environmentID, "")
	if errors.Is(err, ErrNotFound) {
		return Revision{}, ErrDataIntegrity
	}
	return revision, err
}

func (r *repository) revisionByID(ctx context.Context, q queryer, revisionID, environmentID, service string) (Revision, error) {
	var revision Revision
	var createdAt int64
	err := q.QueryRowContext(ctx, `SELECT id, environment_id, version, message,
		COALESCE(created_by, created_by_machine_identity_id),
		CASE WHEN created_by IS NOT NULL THEN 'user' ELSE 'machine' END, created_at
		FROM revisions WHERE id = ? AND environment_id = ?`, revisionID, environmentID).
		Scan(&revision.ID, &revision.EnvironmentID, &revision.Version, &revision.Message, &revision.CreatedBy, &revision.CreatedByType, &createdAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Revision{}, ErrNotFound
	}
	if err != nil {
		return Revision{}, database.ClassifyError(fmt.Errorf("load revision metadata: %w", err))
	}
	revision.CreatedAt = time.Unix(createdAt, 0).UTC()
	entries, err := r.entries(ctx, q, revision.ID, environmentID, service)
	if err != nil {
		return Revision{}, err
	}
	revision.Entries = entries
	return revision, nil
}

func (r *repository) entries(ctx context.Context, q queryer, revisionID, environmentID, service string) ([]Entry, error) {
	query := `SELECT re.key, re.value, COALESCE(re.service, '') FROM revision_entries re
		JOIN revisions r ON r.id = re.revision_id
		WHERE re.revision_id = ? AND r.environment_id = ?`
	args := []any{revisionID, environmentID}
	if service != "" {
		query += ` AND re.service = ?`
		args = append(args, service)
	}
	query += ` ORDER BY re.key`
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, database.ClassifyError(fmt.Errorf("load revision entries: %w", err))
	}
	defer rows.Close()
	entries := make([]Entry, 0)
	for rows.Next() {
		var entry Entry
		if err := rows.Scan(&entry.Key, &entry.Value, &entry.Service); err != nil {
			return nil, database.ClassifyError(fmt.Errorf("scan revision entry: %w", err))
		}
		entries = append(entries, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, database.ClassifyError(fmt.Errorf("iterate revision entries: %w", err))
	}
	return entries, nil
}

func (r *repository) list(ctx context.Context, q queryer, environmentID string) ([]RevisionSummary, error) {
	rows, err := q.QueryContext(ctx, `SELECT id, environment_id, version, message,
		COALESCE(created_by, created_by_machine_identity_id),
		CASE WHEN created_by IS NOT NULL THEN 'user' ELSE 'machine' END, created_at
		FROM revisions WHERE environment_id = ? ORDER BY version DESC`, environmentID)
	if err != nil {
		return nil, database.ClassifyError(fmt.Errorf("list revisions: %w", err))
	}
	defer rows.Close()
	revisions := make([]RevisionSummary, 0)
	for rows.Next() {
		var revision RevisionSummary
		var createdAt int64
		if err := rows.Scan(&revision.ID, &revision.EnvironmentID, &revision.Version, &revision.Message, &revision.CreatedBy, &revision.CreatedByType, &createdAt); err != nil {
			return nil, database.ClassifyError(fmt.Errorf("scan revision summary: %w", err))
		}
		revision.CreatedAt = time.Unix(createdAt, 0).UTC()
		revisions = append(revisions, revision)
	}
	if err := rows.Err(); err != nil {
		return nil, database.ClassifyError(fmt.Errorf("iterate revision summaries: %w", err))
	}
	return revisions, nil
}
