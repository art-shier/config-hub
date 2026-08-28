package auth

import (
	"bytes"
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
	ErrUserFileRead        = errors.New("user file read error")
	errUserSnapshotChanged = errors.New("user snapshot changed during reconciliation")
	errUserSyncConflict    = errors.New("user configuration changed concurrently")
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
	store          *database.Store
	hashPassword   func(string) (string, error)
	verifyPassword func(string, string) bool

	// afterMutation is only a package-test observer for deterministic
	// transaction-boundary tests. Production syncers leave it nil.
	afterMutation func()
}

func NewUserSyncer(store *database.Store) *UserSyncer {
	return &UserSyncer{store: store, hashPassword: HashPassword, verifyPassword: VerifyPassword}
}

// LoadAndSync decodes one strict YAML account document and reconciles it with
// the database.
func (s *UserSyncer) LoadAndSync(ctx context.Context, path string) (SyncResult, error) {
	contents, err := os.ReadFile(path)
	if err != nil {
		return SyncResult{}, errors.Join(ErrUserFileRead, fmt.Errorf("read users file: %w", err))
	}

	decoder := yaml.NewDecoder(bytes.NewReader(contents))
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

	for attempt := 0; attempt < 3; attempt++ {
		if err := ctx.Err(); err != nil {
			return SyncResult{}, err
		}
		snapshot, err := loadUsers(ctx, s.store.DB())
		if err != nil {
			return SyncResult{}, fmt.Errorf("load users for credential preparation: %w", err)
		}
		plan, err := s.prepareReconciliation(ctx, snapshot, specs)
		if err != nil {
			return SyncResult{}, err
		}

		now := time.Now().UTC()
		result := SyncResult{SyncedAt: now}
		err = s.store.InTx(ctx, func(tx *sql.Tx) error {
			current, err := loadUsers(ctx, tx)
			if err != nil {
				return err
			}
			if !sameUserSnapshot(snapshot, current) {
				return errUserSnapshotChanged
			}
			return s.applyPlanTx(ctx, tx, current, plan, now.Unix(), &result)
		})
		if err == nil {
			return result, nil
		}
		if errors.Is(err, errUserSnapshotChanged) {
			if err := ctx.Err(); err != nil {
				return SyncResult{}, err
			}
			continue
		}
		return SyncResult{}, fmt.Errorf("reconcile configured users: %w", err)
	}
	return SyncResult{}, errUserSyncConflict
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
	createdAt    int64
	updatedAt    int64
}

type reconciliationPlan struct {
	users      []plannedUser
	configured map[string]struct{}
}

type plannedUser struct {
	spec            normalizedUserSpec
	exists          bool
	passwordHash    string
	passwordChanged bool
	fieldsChanged   bool
}

func (s *UserSyncer) prepareReconciliation(ctx context.Context, existing map[string]databaseUser, specs []normalizedUserSpec) (reconciliationPlan, error) {
	plan := reconciliationPlan{users: make([]plannedUser, 0, len(specs)), configured: make(map[string]struct{}, len(specs))}
	for _, spec := range specs {
		if err := ctx.Err(); err != nil {
			return reconciliationPlan{}, err
		}
		plan.configured[spec.username] = struct{}{}
		current, exists := existing[spec.username]
		if !exists {
			if err := ctx.Err(); err != nil {
				return reconciliationPlan{}, err
			}
			hash, err := s.hashPassword(spec.password)
			if err != nil {
				return reconciliationPlan{}, fmt.Errorf("hash password for new user: %w", err)
			}
			if err := ctx.Err(); err != nil {
				return reconciliationPlan{}, err
			}
			plan.users = append(plan.users, plannedUser{spec: spec, passwordHash: hash})
			continue
		}

		if err := ctx.Err(); err != nil {
			return reconciliationPlan{}, err
		}
		matches := s.verifyPassword(current.passwordHash, spec.password)
		if err := ctx.Err(); err != nil {
			return reconciliationPlan{}, err
		}
		passwordChanged := !matches
		hash := current.passwordHash
		if passwordChanged {
			if err := ctx.Err(); err != nil {
				return reconciliationPlan{}, err
			}
			var err error
			hash, err = s.hashPassword(spec.password)
			if err != nil {
				return reconciliationPlan{}, fmt.Errorf("hash password for configured user: %w", err)
			}
			if err := ctx.Err(); err != nil {
				return reconciliationPlan{}, err
			}
		}
		plan.users = append(plan.users, plannedUser{
			spec: spec, exists: true, passwordHash: hash, passwordChanged: passwordChanged,
			fieldsChanged: current.DisplayName != spec.displayName || current.Role != spec.role || current.Enabled != spec.enabled,
		})
	}
	return plan, nil
}

func (s *UserSyncer) applyPlanTx(ctx context.Context, tx *sql.Tx, existing map[string]databaseUser, plan reconciliationPlan, now int64, result *SyncResult) error {
	for _, planned := range plan.users {
		current, exists := existing[planned.spec.username]
		if !exists {
			if _, err := tx.ExecContext(ctx, `INSERT INTO users (id, username, display_name, password_hash, role, enabled, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
				uuid.NewString(), planned.spec.username, planned.spec.displayName, planned.passwordHash, planned.spec.role, boolAsInt(planned.spec.enabled), now, now); err != nil {
				return fmt.Errorf("create configured user %q: %w", planned.spec.username, err)
			}
			s.notifyMutation()
			result.Created++
			continue
		}
		if !planned.exists {
			return errUserSnapshotChanged
		}
		if planned.passwordChanged || !planned.spec.enabled {
			if err := s.deleteUserSessions(ctx, tx, current.ID); err != nil {
				return err
			}
		}
		if !planned.passwordChanged && !planned.fieldsChanged {
			continue
		}
		if _, err := tx.ExecContext(ctx, `UPDATE users SET display_name = ?, password_hash = ?, role = ?, enabled = ?, updated_at = ? WHERE id = ?`,
			planned.spec.displayName, planned.passwordHash, planned.spec.role, boolAsInt(planned.spec.enabled), now, current.ID); err != nil {
			return fmt.Errorf("update configured user %q: %w", planned.spec.username, err)
		}
		s.notifyMutation()
		result.Updated++
		if planned.passwordChanged {
			result.PasswordsChanged++
		}
		if current.Enabled && !planned.spec.enabled {
			result.Disabled++
		}
	}

	for username, current := range existing {
		if _, found := plan.configured[username]; found {
			continue
		}
		if err := s.deleteUserSessions(ctx, tx, current.ID); err != nil {
			return err
		}
		if !current.Enabled {
			continue
		}
		if _, err := tx.ExecContext(ctx, `UPDATE users SET enabled = 0, updated_at = ? WHERE id = ?`, now, current.ID); err != nil {
			return fmt.Errorf("disable missing configured user %q: %w", username, err)
		}
		s.notifyMutation()
		result.Updated++
		result.Disabled++
	}
	return nil
}

type userQueryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func loadUsers(ctx context.Context, queryer userQueryer) (map[string]databaseUser, error) {
	rows, err := queryer.QueryContext(ctx, `SELECT id, username, display_name, password_hash, role, enabled, created_at, updated_at FROM users`)
	if err != nil {
		return nil, fmt.Errorf("load existing users: %w", err)
	}
	defer rows.Close()

	users := make(map[string]databaseUser)
	for rows.Next() {
		var user databaseUser
		var enabled int
		if err := rows.Scan(&user.ID, &user.Username, &user.DisplayName, &user.passwordHash, &user.Role, &enabled, &user.createdAt, &user.updatedAt); err != nil {
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

func sameUserSnapshot(expected, actual map[string]databaseUser) bool {
	if len(expected) != len(actual) {
		return false
	}
	for username, before := range expected {
		after, found := actual[username]
		if !found || before.ID != after.ID || before.Username != after.Username || before.DisplayName != after.DisplayName || before.passwordHash != after.passwordHash || before.Role != after.Role || before.Enabled != after.Enabled || before.createdAt != after.createdAt || before.updatedAt != after.updatedAt {
			return false
		}
	}
	return true
}

func (s *UserSyncer) deleteUserSessions(ctx context.Context, tx *sql.Tx, userID string) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM sessions WHERE user_id = ?`, userID); err != nil {
		return fmt.Errorf("revoke user sessions: %w", err)
	}
	s.notifyMutation()
	return nil
}

func (s *UserSyncer) notifyMutation() {
	if s.afterMutation != nil {
		s.afterMutation()
	}
}

func boolAsInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
