package projects

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"confighub.local/internal/auth"
	"confighub.local/internal/database"
	"confighub.local/internal/permissions"
)

const (
	MaxNameBytes        = 128
	MaxDescriptionBytes = 1024
)

var (
	ErrForbidden = errors.New("project access forbidden")
	ErrNotFound  = errors.New("project resource not found")
	ErrConflict  = errors.New("project resource conflict")
	ErrInvalid   = errors.New("invalid project input")

	slugPattern     = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)
	usernamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
)

type Project struct {
	ID          string    `json:"id"`
	Slug        string    `json:"slug"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type Environment struct {
	ID                string    `json:"id"`
	ProjectID         string    `json:"project_id"`
	Slug              string    `json:"slug"`
	Name              string    `json:"name"`
	CurrentRevisionID *string   `json:"current_revision_id"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
}

type MemberGrant struct {
	UserID      string `json:"user_id"`
	Username    string `json:"username"`
	DisplayName string `json:"display_name"`
	Permission  string `json:"permission"`
}

type ProjectDetail struct {
	Project
	Permission   string        `json:"permission"`
	Environments []Environment `json:"environments"`
}

type CreateProject struct {
	Slug, Name, Description string
}

type CreateEnvironment struct {
	Slug, Name string
}

type ValidationError struct {
	Fields map[string]string
}

func (e *ValidationError) Error() string { return ErrInvalid.Error() }
func (e *ValidationError) Unwrap() error { return ErrInvalid }

type Service struct {
	repository *repository
}

func NewService(store *database.Store) *Service {
	return &Service{repository: newRepository(store)}
}

func (s *Service) ListVisible(ctx context.Context, actor auth.User) ([]Project, error) {
	if err := s.readyActor(actor); err != nil {
		return nil, err
	}
	current, err := s.repository.currentActor(ctx, s.repository.store.DB(), actor.ID)
	if err != nil {
		return nil, err
	}
	if current.Role != "admin" && current.Role != "member" {
		return nil, ErrForbidden
	}
	return s.repository.listProjects(ctx, current)
}

func (s *Service) CreateProject(ctx context.Context, actor auth.User, input CreateProject) (Project, error) {
	if err := requireAdminActor(actor); err != nil {
		return Project{}, err
	}
	normalized, err := validateCreateProject(input)
	if err != nil {
		return Project{}, err
	}
	project := Project{
		ID: uuid.NewString(), Slug: normalized.Slug, Name: normalized.Name, Description: normalized.Description,
	}
	now := time.Now().UTC().Truncate(time.Second)
	project.CreatedAt, project.UpdatedAt = now, now
	err = s.repository.store.InTx(ctx, func(tx *sql.Tx) error {
		current, err := s.repository.currentActor(ctx, tx, actor.ID)
		if err != nil || !permissions.Allowed(current.Role, "", permissions.ActionManageProject) {
			if err != nil && !errors.Is(err, ErrForbidden) {
				return err
			}
			return ErrForbidden
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO projects (id, slug, name, description, created_by, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?)`, project.ID, project.Slug, project.Name, project.Description, current.ID, now.Unix(), now.Unix())
		if isConstraintError(err) {
			return errors.Join(ErrConflict, err)
		}
		return err
	})
	if err != nil {
		return Project{}, fmt.Errorf("create project: %w", err)
	}
	return project, nil
}

func (s *Service) GetProject(ctx context.Context, actor auth.User, projectSlug string) (ProjectDetail, error) {
	if err := s.readyActor(actor); err != nil {
		return ProjectDetail{}, err
	}
	if err := validateSlugField("project", projectSlug); err != nil {
		if actor.Role == "member" {
			return ProjectDetail{}, ErrForbidden
		}
		return ProjectDetail{}, ErrNotFound
	}
	current, err := s.repository.currentActor(ctx, s.repository.store.DB(), actor.ID)
	if err != nil {
		return ProjectDetail{}, err
	}
	project, grant, err := s.repository.projectAndGrant(ctx, s.repository.store.DB(), projectSlug, current.ID)
	if err != nil {
		if errors.Is(err, ErrNotFound) && current.Role == "member" {
			return ProjectDetail{}, ErrForbidden
		}
		return ProjectDetail{}, err
	}
	if !permissions.Allowed(current.Role, grant, permissions.ActionReadConfig) {
		return ProjectDetail{}, ErrForbidden
	}
	environments, err := s.repository.environments(ctx, project.ID)
	if err != nil {
		return ProjectDetail{}, err
	}
	permission := grant
	if current.Role == "admin" {
		permission = "admin"
	}
	return ProjectDetail{Project: project, Permission: permission, Environments: environments}, nil
}

func (s *Service) CreateEnvironment(ctx context.Context, actor auth.User, projectSlug string, input CreateEnvironment) (Environment, error) {
	if err := requireAdminActor(actor); err != nil {
		return Environment{}, err
	}
	normalized, err := validateCreateEnvironment(input)
	if err != nil {
		return Environment{}, err
	}
	if err := validateSlugField("project", projectSlug); err != nil {
		return Environment{}, ErrNotFound
	}
	environment := Environment{ID: uuid.NewString(), Slug: normalized.Slug, Name: normalized.Name}
	now := time.Now().UTC().Truncate(time.Second)
	environment.CreatedAt, environment.UpdatedAt = now, now
	err = s.repository.store.InTx(ctx, func(tx *sql.Tx) error {
		current, err := s.repository.currentActor(ctx, tx, actor.ID)
		if err != nil || !permissions.Allowed(current.Role, "", permissions.ActionManageEnvironment) {
			if err != nil && !errors.Is(err, ErrForbidden) {
				return err
			}
			return ErrForbidden
		}
		project, _, err := s.repository.projectAndGrant(ctx, tx, projectSlug, current.ID)
		if err != nil {
			return err
		}
		environment.ProjectID = project.ID
		_, err = tx.ExecContext(ctx, `INSERT INTO environments (id, project_id, slug, name, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?)`, environment.ID, environment.ProjectID, environment.Slug, environment.Name, now.Unix(), now.Unix())
		if isConstraintError(err) {
			return errors.Join(ErrConflict, err)
		}
		return err
	})
	if err != nil {
		return Environment{}, fmt.Errorf("create environment: %w", err)
	}
	return environment, nil
}

func (s *Service) ListMembers(ctx context.Context, actor auth.User, projectSlug string) ([]MemberGrant, error) {
	if err := s.readyActor(actor); err != nil {
		return nil, err
	}
	if err := validateSlugField("project", projectSlug); err != nil {
		if actor.Role == "member" {
			return nil, ErrForbidden
		}
		return nil, ErrNotFound
	}
	current, err := s.repository.currentActor(ctx, s.repository.store.DB(), actor.ID)
	if err != nil {
		return nil, err
	}
	project, grant, err := s.repository.projectAndGrant(ctx, s.repository.store.DB(), projectSlug, current.ID)
	if err != nil {
		if errors.Is(err, ErrNotFound) && current.Role == "member" {
			return nil, ErrForbidden
		}
		return nil, err
	}
	if !permissions.Allowed(current.Role, grant, permissions.ActionReadConfig) {
		return nil, ErrForbidden
	}
	return s.repository.members(ctx, project.ID)
}

func (s *Service) SetMember(ctx context.Context, actor auth.User, projectSlug, username, permission string) error {
	if err := requireAdminActor(actor); err != nil {
		return err
	}
	fields := make(map[string]string)
	if !usernamePattern.MatchString(username) {
		fields["username"] = "must be a valid username"
	}
	if permission != "viewer" && permission != "editor" {
		fields["permission"] = "must be viewer or editor"
	}
	if len(fields) > 0 {
		return &ValidationError{Fields: fields}
	}
	if err := validateSlugField("project", projectSlug); err != nil {
		return ErrNotFound
	}
	err := s.repository.store.InTx(ctx, func(tx *sql.Tx) error {
		current, err := s.repository.currentActor(ctx, tx, actor.ID)
		if err != nil || !permissions.Allowed(current.Role, "", permissions.ActionManageMembers) {
			if err != nil && !errors.Is(err, ErrForbidden) {
				return err
			}
			return ErrForbidden
		}
		project, _, err := s.repository.projectAndGrant(ctx, tx, projectSlug, current.ID)
		if err != nil {
			return err
		}
		var targetID, targetRole string
		var enabled int
		err = tx.QueryRowContext(ctx, `SELECT id, role, enabled FROM users WHERE username = ?`, username).Scan(&targetID, &targetRole, &enabled)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if enabled != 1 || targetRole != "member" {
			return &ValidationError{Fields: map[string]string{"username": "must identify an enabled member"}}
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO project_members (project_id, user_id, permission) VALUES (?, ?, ?)
			ON CONFLICT(project_id, user_id) DO UPDATE SET permission = excluded.permission`, project.ID, targetID, permission)
		return err
	})
	if err != nil {
		return fmt.Errorf("set project member: %w", err)
	}
	return nil
}

func (s *Service) RemoveMember(ctx context.Context, actor auth.User, projectSlug, username string) error {
	if err := requireAdminActor(actor); err != nil {
		return err
	}
	if !usernamePattern.MatchString(username) {
		return &ValidationError{Fields: map[string]string{"username": "must be a valid username"}}
	}
	if err := validateSlugField("project", projectSlug); err != nil {
		return ErrNotFound
	}
	err := s.repository.store.InTx(ctx, func(tx *sql.Tx) error {
		current, err := s.repository.currentActor(ctx, tx, actor.ID)
		if err != nil || !permissions.Allowed(current.Role, "", permissions.ActionManageMembers) {
			if err != nil && !errors.Is(err, ErrForbidden) {
				return err
			}
			return ErrForbidden
		}
		project, _, err := s.repository.projectAndGrant(ctx, tx, projectSlug, current.ID)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `DELETE FROM project_members WHERE project_id = ?
			AND user_id = (SELECT id FROM users WHERE username = ?)`, project.ID, username)
		return err
	})
	if err != nil {
		return fmt.Errorf("remove project member: %w", err)
	}
	return nil
}

func (s *Service) readyActor(actor auth.User) error {
	if s == nil || s.repository == nil || s.repository.store == nil {
		return errors.New("project service has no database store")
	}
	if !actor.Enabled || actor.ID == "" {
		return ErrForbidden
	}
	return nil
}

func requireAdminActor(actor auth.User) error {
	if !actor.Enabled || actor.ID == "" || actor.Role != "admin" {
		return ErrForbidden
	}
	return nil
}

func validateCreateProject(input CreateProject) (CreateProject, error) {
	fields := make(map[string]string)
	if !slugPattern.MatchString(input.Slug) {
		fields["slug"] = "must contain lowercase letters, numbers, or hyphens and be at most 63 characters"
	}
	name := strings.TrimSpace(input.Name)
	if name == "" || !utf8.ValidString(name) || len(name) > MaxNameBytes {
		fields["name"] = fmt.Sprintf("must be valid UTF-8 between 1 and %d bytes", MaxNameBytes)
	}
	description := strings.TrimSpace(input.Description)
	if !utf8.ValidString(description) || len(description) > MaxDescriptionBytes {
		fields["description"] = fmt.Sprintf("must be valid UTF-8 and at most %d bytes", MaxDescriptionBytes)
	}
	if len(fields) > 0 {
		return CreateProject{}, &ValidationError{Fields: fields}
	}
	return CreateProject{Slug: input.Slug, Name: name, Description: description}, nil
}

func validateCreateEnvironment(input CreateEnvironment) (CreateEnvironment, error) {
	fields := make(map[string]string)
	if !slugPattern.MatchString(input.Slug) {
		fields["slug"] = "must contain lowercase letters, numbers, or hyphens and be at most 63 characters"
	}
	name := strings.TrimSpace(input.Name)
	if name == "" || !utf8.ValidString(name) || len(name) > MaxNameBytes {
		fields["name"] = fmt.Sprintf("must be valid UTF-8 between 1 and %d bytes", MaxNameBytes)
	}
	if len(fields) > 0 {
		return CreateEnvironment{}, &ValidationError{Fields: fields}
	}
	return CreateEnvironment{Slug: input.Slug, Name: name}, nil
}

func validateSlugField(field, value string) error {
	if !slugPattern.MatchString(value) {
		return &ValidationError{Fields: map[string]string{field: "must be a valid slug"}}
	}
	return nil
}
