package machineaccess

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

func newRepository(store *database.Store) *repository {
	return &repository{store: store}
}

func (r *repository) currentAdmin(ctx context.Context, q queryer, actorID string) (auth.User, error) {
	var actor auth.User
	var enabled int
	err := q.QueryRowContext(ctx, `SELECT id, username, display_name, role, enabled FROM users WHERE id = ?`, actorID).
		Scan(&actor.ID, &actor.Username, &actor.DisplayName, &actor.Role, &enabled)
	if errors.Is(err, sql.ErrNoRows) {
		return auth.User{}, ErrForbidden
	}
	if err != nil {
		return auth.User{}, database.ClassifyError(fmt.Errorf("load machine access actor: %w", err))
	}
	actor.Enabled = enabled == 1
	if !actor.Enabled || actor.Role != "admin" {
		return auth.User{}, ErrForbidden
	}
	return actor, nil
}

func (r *repository) listIdentities(ctx context.Context, q queryer) ([]Identity, error) {
	rows, err := q.QueryContext(ctx, `SELECT id, name, description, enabled, created_at, updated_at
		FROM machine_identities ORDER BY name`)
	if err != nil {
		return nil, database.ClassifyError(fmt.Errorf("list machine identities: %w", err))
	}
	defer rows.Close()
	identities := make([]Identity, 0)
	for rows.Next() {
		identity, err := scanIdentity(rows)
		if err != nil {
			return nil, database.ClassifyError(fmt.Errorf("scan machine identity: %w", err))
		}
		identities = append(identities, identity)
	}
	if err := rows.Err(); err != nil {
		return nil, database.ClassifyError(fmt.Errorf("iterate machine identities: %w", err))
	}
	return identities, nil
}

func (r *repository) identity(ctx context.Context, q queryer, identityID string) (Identity, error) {
	identity, err := scanIdentity(q.QueryRowContext(ctx, `SELECT id, name, description, enabled, created_at, updated_at
		FROM machine_identities WHERE id = ?`, identityID))
	if errors.Is(err, sql.ErrNoRows) {
		return Identity{}, ErrNotFound
	}
	if err != nil {
		return Identity{}, database.ClassifyError(fmt.Errorf("load machine identity: %w", err))
	}
	return identity, nil
}

func (r *repository) grants(ctx context.Context, q queryer, identityID string) ([]EnvironmentGrant, error) {
	rows, err := q.QueryContext(ctx, `SELECT project_id, environment_id FROM machine_grants
		WHERE identity_id = ? ORDER BY project_id, environment_id`, identityID)
	if err != nil {
		return nil, database.ClassifyError(fmt.Errorf("list machine grants: %w", err))
	}
	defer rows.Close()
	grants := make([]EnvironmentGrant, 0)
	for rows.Next() {
		var grant EnvironmentGrant
		if err := rows.Scan(&grant.ProjectID, &grant.EnvironmentID); err != nil {
			return nil, database.ClassifyError(fmt.Errorf("scan machine grant: %w", err))
		}
		grants = append(grants, grant)
	}
	if err := rows.Err(); err != nil {
		return nil, database.ClassifyError(fmt.Errorf("iterate machine grants: %w", err))
	}
	return grants, nil
}

func (r *repository) tokens(ctx context.Context, q queryer, identityID string) ([]TokenMetadata, error) {
	rows, err := q.QueryContext(ctx, `SELECT id, name, prefix, expires_at, revoked_at, created_at
		FROM access_tokens WHERE identity_id = ? ORDER BY created_at, name`, identityID)
	if err != nil {
		return nil, database.ClassifyError(fmt.Errorf("list machine tokens: %w", err))
	}
	defer rows.Close()
	tokens := make([]TokenMetadata, 0)
	for rows.Next() {
		var token TokenMetadata
		var expiresAt, createdAt int64
		var revokedAt sql.NullInt64
		if err := rows.Scan(&token.ID, &token.Name, &token.Prefix, &expiresAt, &revokedAt, &createdAt); err != nil {
			return nil, database.ClassifyError(fmt.Errorf("scan machine token: %w", err))
		}
		token.ExpiresAt = unixTime(expiresAt)
		token.CreatedAt = unixTime(createdAt)
		if revokedAt.Valid {
			revoked := unixTime(revokedAt.Int64)
			token.RevokedAt = &revoked
		}
		tokens = append(tokens, token)
	}
	if err := rows.Err(); err != nil {
		return nil, database.ClassifyError(fmt.Errorf("iterate machine tokens: %w", err))
	}
	return tokens, nil
}

func (r *repository) authenticateForEnvironment(ctx context.Context, q queryer, tokenHash []byte, environmentID string, now int64) (Identity, bool, error) {
	var identity Identity
	var enabled, allowed int
	var createdAt, updatedAt int64
	err := q.QueryRowContext(ctx, `SELECT mi.id, mi.name, mi.description, mi.enabled, mi.created_at, mi.updated_at,
		EXISTS (
			SELECT 1 FROM machine_grants mg
			JOIN environments e ON e.id = mg.environment_id AND e.project_id = mg.project_id
			WHERE mg.identity_id = mi.id AND e.id = ?
		)
		FROM access_tokens at
		JOIN machine_identities mi ON mi.id = at.identity_id
		WHERE at.token_hash = ? AND at.revoked_at IS NULL AND at.expires_at > ? AND mi.enabled = 1`,
		environmentID, tokenHash, now).
		Scan(&identity.ID, &identity.Name, &identity.Description, &enabled, &createdAt, &updatedAt, &allowed)
	if errors.Is(err, sql.ErrNoRows) {
		return Identity{}, false, ErrInvalidToken
	}
	if err != nil {
		return Identity{}, false, database.ClassifyError(fmt.Errorf("authenticate machine token: %w", err))
	}
	identity.Enabled = enabled == 1
	identity.CreatedAt = unixTime(createdAt)
	identity.UpdatedAt = unixTime(updatedAt)
	return identity, allowed == 1, nil
}

func (r *repository) authenticateForProjectEnvironment(ctx context.Context, q queryer, tokenHash []byte, projectSlug, environmentSlug string, now int64) (Identity, string, error) {
	var identity Identity
	var environmentID sql.NullString
	var enabled int
	var createdAt, updatedAt int64
	err := q.QueryRowContext(ctx, `SELECT mi.id, mi.name, mi.description, mi.enabled, mi.created_at, mi.updated_at,
		(
			SELECT e.id FROM machine_grants mg
			JOIN environments e ON e.id = mg.environment_id AND e.project_id = mg.project_id
			JOIN projects p ON p.id = e.project_id
			WHERE mg.identity_id = mi.id AND p.slug = ? AND e.slug = ?
			LIMIT 1
		)
		FROM access_tokens at
		JOIN machine_identities mi ON mi.id = at.identity_id
		WHERE at.token_hash = ? AND at.revoked_at IS NULL AND at.expires_at > ? AND mi.enabled = 1`,
		projectSlug, environmentSlug, tokenHash, now).
		Scan(&identity.ID, &identity.Name, &identity.Description, &enabled, &createdAt, &updatedAt, &environmentID)
	if errors.Is(err, sql.ErrNoRows) {
		return Identity{}, "", ErrInvalidToken
	}
	if err != nil {
		return Identity{}, "", database.ClassifyError(fmt.Errorf("authenticate machine token route: %w", err))
	}
	identity.Enabled = enabled == 1
	identity.CreatedAt = unixTime(createdAt)
	identity.UpdatedAt = unixTime(updatedAt)
	if !environmentID.Valid {
		return identity, "", ErrScopeDenied
	}
	return identity, environmentID.String, nil
}

func (r *repository) currentConfig(ctx context.Context, q queryer, environmentID, service string) (int64, map[string]string, error) {
	var revisionID sql.NullString
	err := q.QueryRowContext(ctx, `SELECT current_revision_id FROM environments WHERE id = ?`, environmentID).Scan(&revisionID)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil, ErrScopeDenied
	}
	if err != nil {
		return 0, nil, database.ClassifyError(fmt.Errorf("load machine current revision pointer: %w", err))
	}
	if !revisionID.Valid {
		return 0, map[string]string{}, nil
	}
	var version int64
	err = q.QueryRowContext(ctx, `SELECT version FROM revisions WHERE id = ? AND environment_id = ?`, revisionID.String, environmentID).Scan(&version)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil, ErrDataIntegrity
	}
	if err != nil {
		return 0, nil, database.ClassifyError(fmt.Errorf("load machine current revision: %w", err))
	}
	if version <= 0 {
		return 0, nil, ErrDataIntegrity
	}
	query := `SELECT re.key, re.value FROM revision_entries re
		JOIN revisions r ON r.id = re.revision_id
		WHERE re.revision_id = ? AND r.environment_id = ?`
	args := []any{revisionID.String, environmentID}
	if service != "" {
		query += ` AND re.service = ?`
		args = append(args, service)
	}
	query += ` ORDER BY re.key`
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return 0, nil, database.ClassifyError(fmt.Errorf("load machine config entries: %w", err))
	}
	defer rows.Close()
	values := make(map[string]string)
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return 0, nil, database.ClassifyError(fmt.Errorf("scan machine config entry: %w", err))
		}
		values[key] = value
	}
	if err := rows.Err(); err != nil {
		return 0, nil, database.ClassifyError(fmt.Errorf("iterate machine config entries: %w", err))
	}
	return version, values, nil
}

func scanIdentity(scanner interface{ Scan(...any) error }) (Identity, error) {
	var identity Identity
	var enabled int
	var createdAt, updatedAt int64
	if err := scanner.Scan(&identity.ID, &identity.Name, &identity.Description, &enabled, &createdAt, &updatedAt); err != nil {
		return Identity{}, err
	}
	identity.Enabled = enabled == 1
	identity.CreatedAt = unixTime(createdAt)
	identity.UpdatedAt = unixTime(updatedAt)
	return identity, nil
}

func unixTime(value int64) time.Time {
	return time.Unix(value, 0).UTC()
}
