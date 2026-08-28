package auth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"gopkg.in/yaml.v3"

	"confighub.local/internal/database"
)

var (
	// ErrInvalidUserFile identifies invalid account configuration.
	ErrInvalidUserFile = errors.New("invalid user file")
	// ErrUserFileRead identifies failures opening account configuration.
	ErrUserFileRead = errors.New("user file read error")
)

var usernamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

type UserSpec struct {
	Username    string `yaml:"username"`
	DisplayName string `yaml:"display_name"`
	Password    string `yaml:"password"`
	Role        string `yaml:"role"`
	Enabled     bool   `yaml:"enabled"`
}

type UserFile struct {
	Users []UserSpec `yaml:"users"`
}

type User struct {
	ID, Username, DisplayName, Role string
	Enabled                         bool
}

type SyncResult struct {
	Created, Updated, Disabled, PasswordsChanged int
	SyncedAt                                     time.Time
}

type UserSyncer struct {
	store *database.Store
}

func NewUserSyncer(store *database.Store) *UserSyncer {
	return &UserSyncer{store: store}
}

// LoadAndSync decodes one strict YAML account document and reconciles it with
// the database.
func (s *UserSyncer) LoadAndSync(ctx context.Context, path string) (SyncResult, error) {
	file, err := os.Open(path)
	if err != nil {
		return SyncResult{}, fmt.Errorf("%w: open users file: %v", ErrUserFileRead, err)
	}
	defer file.Close()

	decoder := yaml.NewDecoder(file)
	decoder.KnownFields(true)
	var users UserFile
	if err := decoder.Decode(&users); err != nil {
		if errors.Is(err, io.EOF) {
			return SyncResult{}, fmt.Errorf("%w: users file is empty", ErrInvalidUserFile)
		}
		return SyncResult{}, fmt.Errorf("%w: decode users file", ErrInvalidUserFile)
	}
	var extra yaml.Node
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return SyncResult{}, fmt.Errorf("%w: users file must contain one document", ErrInvalidUserFile)
		}
		return SyncResult{}, fmt.Errorf("%w: decode users file", ErrInvalidUserFile)
	}

	return s.Sync(ctx, users)
}

// Sync validates and atomically reconciles configured accounts with the
// database. Users missing from the file are disabled, never deleted.
func (s *UserSyncer) Sync(ctx context.Context, file UserFile) (SyncResult, error) {
	if s == nil || s.store == nil {
		return SyncResult{}, errors.New("user syncer has no database store")
	}
	specs, err := validateUserFile(file)
	if err != nil {
		return SyncResult{}, err
	}

	now := time.Now().UTC()
	result := SyncResult{SyncedAt: now}
	if err := s.store.InTx(ctx, func(tx *sql.Tx) error {
		return s.syncTx(ctx, tx, specs, now.Unix(), &result)
	}); err != nil {
		return SyncResult{}, fmt.Errorf("reconcile configured users: %w", err)
	}
	return result, nil
}

type normalizedUserSpec struct {
	username    string
	displayName string
	password    string
	role        string
	enabled     bool
}

func validateUserFile(file UserFile) ([]normalizedUserSpec, error) {
	if len(file.Users) == 0 {
		return nil, fmt.Errorf("%w: at least one enabled admin is required", ErrInvalidUserFile)
	}

	seen := make(map[string]struct{}, len(file.Users))
	specs := make([]normalizedUserSpec, 0, len(file.Users))
	hasEnabledAdmin := false
	for _, user := range file.Users {
		if !usernamePattern.MatchString(user.Username) {
			return nil, fmt.Errorf("%w: invalid username", ErrInvalidUserFile)
		}
		if _, exists := seen[user.Username]; exists {
			return nil, fmt.Errorf("%w: duplicate username %q", ErrInvalidUserFile, user.Username)
		}
		seen[user.Username] = struct{}{}
		displayName := strings.TrimSpace(user.DisplayName)
		if displayName == "" {
			return nil, fmt.Errorf("%w: display name is required for %q", ErrInvalidUserFile, user.Username)
		}
		if user.Password == "" {
			return nil, fmt.Errorf("%w: password is required for %q", ErrInvalidUserFile, user.Username)
		}
		if user.Role != "admin" && user.Role != "member" {
			return nil, fmt.Errorf("%w: invalid role for %q", ErrInvalidUserFile, user.Username)
		}
		if user.Role == "admin" && user.Enabled {
			hasEnabledAdmin = true
		}
		specs = append(specs, normalizedUserSpec{
			username: user.Username, displayName: displayName, password: user.Password, role: user.Role, enabled: user.Enabled,
		})
	}
	if !hasEnabledAdmin {
		return nil, fmt.Errorf("%w: at least one enabled admin is required", ErrInvalidUserFile)
	}
	return specs, nil
}

type databaseUser struct {
	User
	passwordHash string
}

func (s *UserSyncer) syncTx(ctx context.Context, tx *sql.Tx, specs []normalizedUserSpec, now int64, result *SyncResult) error {
	existing, err := loadUsers(ctx, tx)
	if err != nil {
		return err
	}

	configured := make(map[string]struct{}, len(specs))
	for _, spec := range specs {
		configured[spec.username] = struct{}{}
		current, exists := existing[spec.username]
		if !exists {
			hash, err := HashPassword(spec.password)
			if err != nil {
				return fmt.Errorf("hash password for new user: %w", err)
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO users (id, username, display_name, password_hash, role, enabled, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
				uuid.NewString(), spec.username, spec.displayName, hash, spec.role, boolAsInt(spec.enabled), now, now); err != nil {
				return fmt.Errorf("create configured user %q: %w", spec.username, err)
			}
			result.Created++
			continue
		}

		passwordChanged := !VerifyPassword(current.passwordHash, spec.password)
		fieldsChanged := current.DisplayName != spec.displayName || current.Role != spec.role || current.Enabled != spec.enabled
		if !spec.enabled {
			if err := deleteUserSessions(ctx, tx, current.ID); err != nil {
				return err
			}
		}
		if !passwordChanged && !fieldsChanged {
			continue
		}

		hash := current.passwordHash
		if passwordChanged {
			hash, err = HashPassword(spec.password)
			if err != nil {
				return fmt.Errorf("hash password for configured user: %w", err)
			}
			if err := deleteUserSessions(ctx, tx, current.ID); err != nil {
				return err
			}
			result.PasswordsChanged++
		}
		if _, err := tx.ExecContext(ctx, `UPDATE users SET display_name = ?, password_hash = ?, role = ?, enabled = ?, updated_at = ? WHERE id = ?`,
			spec.displayName, hash, spec.role, boolAsInt(spec.enabled), now, current.ID); err != nil {
			return fmt.Errorf("update configured user %q: %w", spec.username, err)
		}
		result.Updated++
		if current.Enabled && !spec.enabled {
			result.Disabled++
		}
	}

	for username, current := range existing {
		if _, found := configured[username]; found {
			continue
		}
		if err := deleteUserSessions(ctx, tx, current.ID); err != nil {
			return err
		}
		if !current.Enabled {
			continue
		}
		if _, err := tx.ExecContext(ctx, `UPDATE users SET enabled = 0, updated_at = ? WHERE id = ?`, now, current.ID); err != nil {
			return fmt.Errorf("disable missing configured user %q: %w", username, err)
		}
		result.Updated++
		result.Disabled++
	}
	return nil
}

func loadUsers(ctx context.Context, tx *sql.Tx) (map[string]databaseUser, error) {
	rows, err := tx.QueryContext(ctx, `SELECT id, username, display_name, password_hash, role, enabled FROM users`)
	if err != nil {
		return nil, fmt.Errorf("load existing users: %w", err)
	}
	defer rows.Close()

	users := make(map[string]databaseUser)
	for rows.Next() {
		var user databaseUser
		var enabled int
		if err := rows.Scan(&user.ID, &user.Username, &user.DisplayName, &user.passwordHash, &user.Role, &enabled); err != nil {
			return nil, fmt.Errorf("scan existing user: %w", err)
		}
		user.Enabled = enabled != 0
		users[user.Username] = user
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate existing users: %w", err)
	}
	return users, nil
}

func deleteUserSessions(ctx context.Context, tx *sql.Tx, userID string) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM sessions WHERE user_id = ?`, userID); err != nil {
		return fmt.Errorf("revoke user sessions: %w", err)
	}
	return nil
}

func boolAsInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
