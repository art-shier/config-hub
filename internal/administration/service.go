// Package administration exposes the read-only global administration state.
package administration

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"confighub.local/internal/auth"
	"confighub.local/internal/database"
)

var ErrForbidden = errors.New("administration access forbidden")

type StatusSource interface {
	Live() bool
	Ready() bool
	LastSuccessfulUserSyncAt() time.Time
}

type UserStatus struct {
	ID          string    `json:"id"`
	Username    string    `json:"username"`
	DisplayName string    `json:"display_name"`
	Role        string    `json:"role"`
	Enabled     bool      `json:"enabled"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type UserRegister struct {
	Users                    []UserStatus `json:"users"`
	LastSuccessfulUserSyncAt time.Time    `json:"last_successful_user_sync_at"`
}

type SystemStatus struct {
	BuildVersion             string    `json:"build_version"`
	Live                     bool      `json:"live"`
	Ready                    bool      `json:"ready"`
	SQLiteReady              bool      `json:"sqlite_ready"`
	LastSuccessfulUserSyncAt time.Time `json:"last_successful_user_sync_at"`
}

type Service struct {
	store        *database.Store
	status       StatusSource
	buildVersion string
}

func NewService(store *database.Store, status StatusSource, buildVersion string) *Service {
	return &Service{store: store, status: status, buildVersion: buildVersion}
}

func (s *Service) ListUsers(ctx context.Context, actor auth.User) (UserRegister, error) {
	if err := s.ready(); err != nil {
		return UserRegister{}, err
	}
	register := UserRegister{Users: make([]UserStatus, 0)}
	err := s.store.InReadTx(ctx, func(tx *sql.Tx) error {
		if err := currentAdmin(ctx, tx, actor.ID); err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, `SELECT id, username, display_name, role, enabled, updated_at FROM users ORDER BY username`)
		if err != nil {
			return database.ClassifyError(fmt.Errorf("list synchronized users: %w", err))
		}
		defer rows.Close()
		for rows.Next() {
			var user UserStatus
			var enabled int
			var updatedAt int64
			if err := rows.Scan(&user.ID, &user.Username, &user.DisplayName, &user.Role, &enabled, &updatedAt); err != nil {
				return database.ClassifyError(fmt.Errorf("scan synchronized user: %w", err))
			}
			user.Enabled = enabled == 1
			user.UpdatedAt = time.Unix(updatedAt, 0).UTC()
			register.Users = append(register.Users, user)
		}
		if err := rows.Err(); err != nil {
			return database.ClassifyError(fmt.Errorf("iterate synchronized users: %w", err))
		}
		return nil
	})
	if err != nil {
		return UserRegister{}, fmt.Errorf("read synchronized user register: %w", err)
	}
	register.LastSuccessfulUserSyncAt = s.status.LastSuccessfulUserSyncAt()
	return register, nil
}

func (s *Service) System(ctx context.Context, actor auth.User) (SystemStatus, error) {
	if err := s.ready(); err != nil {
		return SystemStatus{}, err
	}
	err := s.store.InReadTx(ctx, func(tx *sql.Tx) error {
		if err := currentAdmin(ctx, tx, actor.ID); err != nil {
			return err
		}
		var one int
		if err := tx.QueryRowContext(ctx, `SELECT 1`).Scan(&one); err != nil {
			return database.ClassifyError(fmt.Errorf("check SQLite readiness: %w", err))
		}
		return nil
	})
	if err != nil {
		return SystemStatus{}, fmt.Errorf("read system status: %w", err)
	}
	return SystemStatus{
		BuildVersion:             s.buildVersion,
		Live:                     s.status.Live(),
		Ready:                    s.status.Ready(),
		SQLiteReady:              true,
		LastSuccessfulUserSyncAt: s.status.LastSuccessfulUserSyncAt(),
	}, nil
}

func (s *Service) ready() error {
	if s == nil || s.store == nil || s.status == nil {
		return errors.New("administration service dependencies are incomplete")
	}
	return nil
}

func currentAdmin(ctx context.Context, queryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, actorID string) error {
	var role string
	var enabled int
	err := queryer.QueryRowContext(ctx, `SELECT role, enabled FROM users WHERE id = ?`, actorID).Scan(&role, &enabled)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrForbidden
	}
	if err != nil {
		return database.ClassifyError(fmt.Errorf("load administration actor: %w", err))
	}
	if role != "admin" || enabled != 1 {
		return ErrForbidden
	}
	return nil
}
