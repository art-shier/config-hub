package projects

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"modernc.org/sqlite"

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

func (r *repository) currentActor(ctx context.Context, q queryer, actorID string) (auth.User, error) {
	var actor auth.User
	var enabled int
	err := q.QueryRowContext(ctx, `SELECT id, username, display_name, role, enabled FROM users WHERE id = ?`, actorID).
		Scan(&actor.ID, &actor.Username, &actor.DisplayName, &actor.Role, &enabled)
	if errors.Is(err, sql.ErrNoRows) {
		return auth.User{}, ErrForbidden
	}
	if err != nil {
		return auth.User{}, database.ClassifyError(fmt.Errorf("load current actor: %w", err))
	}
	actor.Enabled = enabled == 1
	if !actor.Enabled {
		return auth.User{}, ErrForbidden
	}
	return actor, nil
}

func (r *repository) listProjects(ctx context.Context, actor auth.User) ([]Project, error) {
	query := `SELECT p.id, p.slug, p.name, p.description, p.created_at, p.updated_at
		FROM projects p ORDER BY p.slug`
	args := []any(nil)
	if actor.Role == "member" {
		query = `SELECT p.id, p.slug, p.name, p.description, p.created_at, p.updated_at
			FROM projects p JOIN project_members pm ON pm.project_id = p.id
			WHERE pm.user_id = ? ORDER BY p.slug`
		args = []any{actor.ID}
	}
	rows, err := r.store.DB().QueryContext(ctx, query, args...)
	if err != nil {
		return nil, database.ClassifyError(fmt.Errorf("list projects: %w", err))
	}
	defer rows.Close()
	projects := make([]Project, 0)
	for rows.Next() {
		project, err := scanProject(rows)
		if err != nil {
			return nil, fmt.Errorf("scan project: %w", err)
		}
		projects = append(projects, project)
	}
	if err := rows.Err(); err != nil {
		return nil, database.ClassifyError(fmt.Errorf("iterate projects: %w", err))
	}
	return projects, nil
}

func (r *repository) projectAndGrant(ctx context.Context, q queryer, slug, actorID string) (Project, string, error) {
	var project Project
	var grant string
	var createdAt, updatedAt int64
	err := q.QueryRowContext(ctx, `SELECT p.id, p.slug, p.name, p.description, p.created_at, p.updated_at,
		COALESCE(pm.permission, '') FROM projects p
		LEFT JOIN project_members pm ON pm.project_id = p.id AND pm.user_id = ?
		WHERE p.slug = ?`, actorID, slug).
		Scan(&project.ID, &project.Slug, &project.Name, &project.Description, &createdAt, &updatedAt, &grant)
	if errors.Is(err, sql.ErrNoRows) {
		return Project{}, "", ErrNotFound
	}
	if err != nil {
		return Project{}, "", database.ClassifyError(fmt.Errorf("load project: %w", err))
	}
	project.CreatedAt = unixTime(createdAt)
	project.UpdatedAt = unixTime(updatedAt)
	return project, grant, nil
}

func (r *repository) environments(ctx context.Context, projectID string) ([]Environment, error) {
	rows, err := r.store.DB().QueryContext(ctx, `SELECT id, project_id, slug, name, current_revision_id, created_at, updated_at
		FROM environments WHERE project_id = ? ORDER BY slug`, projectID)
	if err != nil {
		return nil, database.ClassifyError(fmt.Errorf("list environments: %w", err))
	}
	defer rows.Close()
	environments := make([]Environment, 0)
	for rows.Next() {
		environment, err := scanEnvironment(rows)
		if err != nil {
			return nil, fmt.Errorf("scan environment: %w", err)
		}
		environments = append(environments, environment)
	}
	if err := rows.Err(); err != nil {
		return nil, database.ClassifyError(fmt.Errorf("iterate environments: %w", err))
	}
	return environments, nil
}

func (r *repository) members(ctx context.Context, projectID string) ([]MemberGrant, error) {
	rows, err := r.store.DB().QueryContext(ctx, `SELECT u.id, u.username, u.display_name, pm.permission
		FROM project_members pm JOIN users u ON u.id = pm.user_id
		WHERE pm.project_id = ? ORDER BY u.username`, projectID)
	if err != nil {
		return nil, database.ClassifyError(fmt.Errorf("list project members: %w", err))
	}
	defer rows.Close()
	members := make([]MemberGrant, 0)
	for rows.Next() {
		var member MemberGrant
		if err := rows.Scan(&member.UserID, &member.Username, &member.DisplayName, &member.Permission); err != nil {
			return nil, fmt.Errorf("scan project member: %w", err)
		}
		members = append(members, member)
	}
	if err := rows.Err(); err != nil {
		return nil, database.ClassifyError(fmt.Errorf("iterate project members: %w", err))
	}
	return members, nil
}

func scanProject(scanner interface{ Scan(...any) error }) (Project, error) {
	var project Project
	var createdAt, updatedAt int64
	if err := scanner.Scan(&project.ID, &project.Slug, &project.Name, &project.Description, &createdAt, &updatedAt); err != nil {
		return Project{}, err
	}
	project.CreatedAt = unixTime(createdAt)
	project.UpdatedAt = unixTime(updatedAt)
	return project, nil
}

func scanEnvironment(scanner interface{ Scan(...any) error }) (Environment, error) {
	var environment Environment
	var revision sql.NullString
	var createdAt, updatedAt int64
	if err := scanner.Scan(&environment.ID, &environment.ProjectID, &environment.Slug, &environment.Name, &revision, &createdAt, &updatedAt); err != nil {
		return Environment{}, err
	}
	if revision.Valid {
		environment.CurrentRevisionID = &revision.String
	}
	environment.CreatedAt = unixTime(createdAt)
	environment.UpdatedAt = unixTime(updatedAt)
	return environment, nil
}

func unixTime(value int64) time.Time {
	return time.Unix(value, 0).UTC()
}

func isConstraintError(err error) bool {
	var sqliteErr *sqlite.Error
	return errors.As(err, &sqliteErr) && sqliteErr.Code()&0xff == 19
}
