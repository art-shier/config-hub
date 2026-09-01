package machineaccess

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"modernc.org/sqlite"

	"confighub.local/internal/auth"
	"confighub.local/internal/database"
)

const (
	MaxNameBytes        = 128
	MaxDescriptionBytes = 1024
	MaxIdentifierBytes  = 128
	MaxGrantCount       = 256
	MaxTokenLifetime    = 365 * 24 * time.Hour
	tokenRandomBytes    = 32
	tokenPrefix         = "ch_"
	tokenLength         = len(tokenPrefix) + 43
	displayPrefixLength = 10
	GrantRead           = "read"
	GrantWrite          = "write"
)

var (
	ErrForbidden     = errors.New("machine access forbidden")
	ErrNotFound      = errors.New("machine access resource not found")
	ErrConflict      = errors.New("machine access resource conflict")
	ErrInvalid       = errors.New("invalid machine access input")
	ErrInvalidToken  = errors.New("invalid machine token")
	ErrScopeDenied   = errors.New("machine token scope denied")
	ErrDataIntegrity = errors.New("machine config data integrity failure")
)

type Identity struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Enabled     bool      `json:"enabled"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type IdentityDetail struct {
	Identity
	Grants []EnvironmentGrant `json:"grants"`
	Tokens []TokenMetadata    `json:"tokens"`
}

type CreateIdentity struct {
	Name, Description string
	Enabled           bool
}

type UpdateIdentityInput struct {
	Description string
	Enabled     bool
}

type EnvironmentGrant struct {
	ProjectID     string `json:"project_id"`
	EnvironmentID string `json:"environment_id"`
	Permission    string `json:"permission"`
}

type TokenMetadata struct {
	ID        string     `json:"id"`
	Name      string     `json:"name"`
	Prefix    string     `json:"prefix"`
	ExpiresAt time.Time  `json:"expires_at"`
	RevokedAt *time.Time `json:"revoked_at"`
	CreatedAt time.Time  `json:"created_at"`
}

type IssuedToken struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Prefix    string    `json:"prefix"`
	Plaintext string    `json:"plaintext"`
	ExpiresAt time.Time `json:"expires_at"`
	CreatedAt time.Time `json:"created_at"`
}

type IssueToken struct {
	Name      string
	ExpiresAt time.Time
}

type CurrentConfig struct {
	Project     string            `json:"project"`
	Environment string            `json:"environment"`
	Revision    int64             `json:"revision"`
	Values      map[string]string `json:"values"`
}

type ValidationError struct {
	Fields map[string]string
}

func (e *ValidationError) Error() string { return ErrInvalid.Error() }
func (e *ValidationError) Unwrap() error { return ErrInvalid }

type Service struct {
	repository *repository
	now        func() time.Time
	random     io.Reader
	// Hooks are package-test observers for deterministic transaction tests.
	afterReadTxBegin          func()
	afterMachineAuthorization func()
}

func NewService(store *database.Store) *Service {
	return &Service{repository: newRepository(store), now: time.Now, random: rand.Reader}
}

func (s *Service) CreateIdentity(ctx context.Context, actor auth.User, input CreateIdentity) (Identity, error) {
	if err := s.readyActor(actor); err != nil {
		return Identity{}, err
	}
	normalized, err := validateCreateIdentity(input)
	if err != nil {
		return Identity{}, err
	}
	now := s.now().UTC().Truncate(time.Second)
	identity := Identity{
		ID: uuid.NewString(), Name: normalized.Name, Description: normalized.Description, Enabled: normalized.Enabled,
		CreatedAt: now, UpdatedAt: now,
	}
	err = s.repository.store.InTx(ctx, func(tx *sql.Tx) error {
		if _, err := s.repository.currentAdmin(ctx, tx, actor.ID); err != nil {
			return err
		}
		enabled := 0
		if identity.Enabled {
			enabled = 1
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO machine_identities (id, name, description, enabled, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?)`, identity.ID, identity.Name, identity.Description, enabled, now.Unix(), now.Unix())
		if isConstraintError(err) {
			return errors.Join(ErrConflict, err)
		}
		return err
	})
	if err != nil {
		return Identity{}, fmt.Errorf("create machine identity: %w", err)
	}
	return identity, nil
}

func (s *Service) ListIdentities(ctx context.Context, actor auth.User) ([]Identity, error) {
	if err := s.readyActor(actor); err != nil {
		return nil, err
	}
	var identities []Identity
	err := s.repository.store.InReadTx(ctx, func(tx *sql.Tx) error {
		if _, err := s.repository.currentAdmin(ctx, tx, actor.ID); err != nil {
			return err
		}
		var err error
		identities, err = s.repository.listIdentities(ctx, tx)
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("list machine identities: %w", err)
	}
	return identities, nil
}

func (s *Service) GetIdentity(ctx context.Context, actor auth.User, identityID string) (IdentityDetail, error) {
	if err := s.readyActor(actor); err != nil {
		return IdentityDetail{}, err
	}
	if err := validateIdentifier("identity", identityID); err != nil {
		return IdentityDetail{}, ErrNotFound
	}
	var detail IdentityDetail
	err := s.repository.store.InReadTx(ctx, func(tx *sql.Tx) error {
		if _, err := s.repository.currentAdmin(ctx, tx, actor.ID); err != nil {
			return err
		}
		identity, err := s.repository.identity(ctx, tx, identityID)
		if err != nil {
			return err
		}
		detail.Identity = identity
		detail.Grants, err = s.repository.grants(ctx, tx, identityID)
		if err != nil {
			return err
		}
		detail.Tokens, err = s.repository.tokens(ctx, tx, identityID)
		return err
	})
	if err != nil {
		return IdentityDetail{}, fmt.Errorf("get machine identity: %w", err)
	}
	return detail, nil
}

func (s *Service) UpdateIdentity(ctx context.Context, actor auth.User, identityID string, input UpdateIdentityInput) (Identity, error) {
	if err := s.readyActor(actor); err != nil {
		return Identity{}, err
	}
	if err := validateIdentifier("identity", identityID); err != nil {
		return Identity{}, ErrNotFound
	}
	description, err := validateDescription(input.Description)
	if err != nil {
		return Identity{}, err
	}
	var updated Identity
	err = s.repository.store.InTx(ctx, func(tx *sql.Tx) error {
		if _, err := s.repository.currentAdmin(ctx, tx, actor.ID); err != nil {
			return err
		}
		current, err := s.repository.identity(ctx, tx, identityID)
		if err != nil {
			return err
		}
		now := s.now().UTC().Truncate(time.Second)
		enabled := 0
		if input.Enabled {
			enabled = 1
		}
		if _, err := tx.ExecContext(ctx, `UPDATE machine_identities SET description = ?, enabled = ?, updated_at = ? WHERE id = ?`, description, enabled, now.Unix(), identityID); err != nil {
			return database.ClassifyError(err)
		}
		current.Description = description
		current.Enabled = input.Enabled
		current.UpdatedAt = now
		updated = current
		return nil
	})
	if err != nil {
		return Identity{}, fmt.Errorf("update machine identity: %w", err)
	}
	return updated, nil
}

func (s *Service) ReplaceGrants(ctx context.Context, actor auth.User, identityID string, grants []EnvironmentGrant) error {
	if err := s.readyActor(actor); err != nil {
		return err
	}
	if err := validateIdentifier("identity", identityID); err != nil {
		return ErrNotFound
	}
	normalized, err := validateGrants(grants)
	if err != nil {
		return err
	}
	err = s.repository.store.InTx(ctx, func(tx *sql.Tx) error {
		if _, err := s.repository.currentAdmin(ctx, tx, actor.ID); err != nil {
			return err
		}
		if _, err := s.repository.identity(ctx, tx, identityID); err != nil {
			return err
		}
		fields := make(map[string]string)
		for index, grant := range normalized {
			var exists int
			err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM environments WHERE id = ? AND project_id = ?)`, grant.EnvironmentID, grant.ProjectID).Scan(&exists)
			if err != nil {
				return database.ClassifyError(err)
			}
			if exists != 1 {
				fields[fmt.Sprintf("grants[%d].environment_id", index)] = "must belong to the selected project"
			}
		}
		if len(fields) > 0 {
			return &ValidationError{Fields: fields}
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM machine_grants WHERE identity_id = ?`, identityID); err != nil {
			return database.ClassifyError(err)
		}
		for _, grant := range normalized {
			if _, err := tx.ExecContext(ctx, `INSERT INTO machine_grants (identity_id, project_id, environment_id, permission) VALUES (?, ?, ?, ?)`, identityID, grant.ProjectID, grant.EnvironmentID, grant.Permission); err != nil {
				return database.ClassifyError(err)
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("replace machine grants: %w", err)
	}
	return nil
}

func (s *Service) IssueToken(ctx context.Context, actor auth.User, identityID string, input IssueToken) (IssuedToken, error) {
	if err := s.readyActor(actor); err != nil {
		return IssuedToken{}, err
	}
	if err := validateIdentifier("identity", identityID); err != nil {
		return IssuedToken{}, ErrNotFound
	}
	normalized, err := s.validateIssueToken(input)
	if err != nil {
		return IssuedToken{}, err
	}
	plaintext, err := s.generateToken()
	if err != nil {
		return IssuedToken{}, fmt.Errorf("generate machine token: %w", err)
	}
	hash := sha256.Sum256([]byte(plaintext))
	issued := IssuedToken{
		ID: uuid.NewString(), Name: normalized.Name, Prefix: plaintext[:displayPrefixLength], Plaintext: plaintext, ExpiresAt: normalized.ExpiresAt,
	}
	err = s.repository.store.InTx(ctx, func(tx *sql.Tx) error {
		if _, err := s.repository.currentAdmin(ctx, tx, actor.ID); err != nil {
			return err
		}
		identity, err := s.repository.identity(ctx, tx, identityID)
		if err != nil {
			return err
		}
		if !identity.Enabled {
			return &ValidationError{Fields: map[string]string{"identity": "must be enabled to issue a token"}}
		}
		now := s.now().UTC().Truncate(time.Second)
		issued.CreatedAt = now
		if err := validateTokenExpiry(issued.ExpiresAt, now); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO access_tokens (id, identity_id, name, prefix, token_hash, expires_at, created_at)
			VALUES (?, ?, ?, ?, ?, ?, ?)`, issued.ID, identityID, issued.Name, issued.Prefix, hash[:], issued.ExpiresAt.Unix(), now.Unix())
		if isConstraintError(err) {
			return errors.Join(ErrConflict, err)
		}
		return err
	})
	if err != nil {
		return IssuedToken{}, fmt.Errorf("issue machine token: %w", err)
	}
	return issued, nil
}

func (s *Service) RevokeToken(ctx context.Context, actor auth.User, identityID, tokenID string) error {
	if err := s.readyActor(actor); err != nil {
		return err
	}
	if validateIdentifier("identity", identityID) != nil || validateIdentifier("token", tokenID) != nil {
		return ErrNotFound
	}
	err := s.repository.store.InTx(ctx, func(tx *sql.Tx) error {
		if _, err := s.repository.currentAdmin(ctx, tx, actor.ID); err != nil {
			return err
		}
		if _, err := s.repository.identity(ctx, tx, identityID); err != nil {
			return err
		}
		var exists int
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM access_tokens WHERE id = ? AND identity_id = ?)`, tokenID, identityID).Scan(&exists); err != nil {
			return database.ClassifyError(err)
		}
		if exists != 1 {
			return ErrNotFound
		}
		_, err := tx.ExecContext(ctx, `UPDATE access_tokens SET revoked_at = COALESCE(revoked_at, ?) WHERE id = ? AND identity_id = ?`, s.now().UTC().Unix(), tokenID, identityID)
		return database.ClassifyError(err)
	})
	if err != nil {
		return fmt.Errorf("revoke machine token: %w", err)
	}
	return nil
}

func (s *Service) AuthenticateForEnvironment(ctx context.Context, plaintext, environmentID string) (Identity, error) {
	if err := s.ready(); err != nil {
		return Identity{}, err
	}
	hash, err := parseToken(plaintext)
	if err != nil {
		return Identity{}, ErrInvalidToken
	}
	identity, allowed, err := s.repository.authenticateForEnvironment(ctx, s.repository.store.DB(), hash[:], environmentID, s.now().UTC().Unix())
	if err != nil {
		return Identity{}, err
	}
	if !allowed {
		return Identity{}, ErrScopeDenied
	}
	return identity, nil
}

func (s *Service) ReadCurrentForProject(ctx context.Context, plaintext, projectSlug, environmentSlug, service string) (CurrentConfig, error) {
	if err := s.ready(); err != nil {
		return CurrentConfig{}, err
	}
	hash, err := parseToken(plaintext)
	if err != nil {
		return CurrentConfig{}, ErrInvalidToken
	}
	config := CurrentConfig{Project: projectSlug, Environment: environmentSlug, Values: map[string]string{}}
	err = s.repository.store.InReadTx(ctx, func(tx *sql.Tx) error {
		if s.afterReadTxBegin != nil {
			s.afterReadTxBegin()
		}
		_, environmentID, err := s.repository.authenticateForProjectEnvironment(ctx, tx, hash[:], projectSlug, environmentSlug, s.now().UTC().Unix())
		if err != nil {
			return err
		}
		if s.afterMachineAuthorization != nil {
			s.afterMachineAuthorization()
		}
		if !utf8.ValidString(service) || len(service) > MaxNameBytes {
			return &ValidationError{Fields: map[string]string{"service": fmt.Sprintf("must be valid UTF-8 and at most %d bytes", MaxNameBytes)}}
		}
		config.Revision, config.Values, err = s.repository.currentConfig(ctx, tx, environmentID, service)
		return err
	})
	if err != nil {
		return CurrentConfig{}, fmt.Errorf("read machine current config: %w", err)
	}
	return config, nil
}

func (s *Service) ready() error {
	if s == nil || s.repository == nil || s.repository.store == nil || s.now == nil || s.random == nil {
		return errors.New("machine access service has no database store")
	}
	return nil
}

func (s *Service) readyActor(actor auth.User) error {
	if err := s.ready(); err != nil {
		return err
	}
	if actor.ID == "" {
		return ErrForbidden
	}
	return nil
}

func validateCreateIdentity(input CreateIdentity) (CreateIdentity, error) {
	name, err := validateName("name", input.Name)
	fields := validationFields(err)
	description, descriptionErr := validateDescription(input.Description)
	for key, value := range validationFields(descriptionErr) {
		fields[key] = value
	}
	if len(fields) > 0 {
		return CreateIdentity{}, &ValidationError{Fields: fields}
	}
	return CreateIdentity{Name: name, Description: description, Enabled: input.Enabled}, nil
}

func validateName(field, value string) (string, error) {
	normalized := strings.TrimSpace(value)
	if normalized == "" || !utf8.ValidString(normalized) || len(normalized) > MaxNameBytes {
		return "", &ValidationError{Fields: map[string]string{field: fmt.Sprintf("must be valid UTF-8 between 1 and %d bytes", MaxNameBytes)}}
	}
	return normalized, nil
}

func validateDescription(value string) (string, error) {
	normalized := strings.TrimSpace(value)
	if !utf8.ValidString(normalized) || len(normalized) > MaxDescriptionBytes {
		return "", &ValidationError{Fields: map[string]string{"description": fmt.Sprintf("must be valid UTF-8 and at most %d bytes", MaxDescriptionBytes)}}
	}
	return normalized, nil
}

func validateIdentifier(field, value string) error {
	if value == "" || strings.TrimSpace(value) != value || !utf8.ValidString(value) || len(value) > MaxIdentifierBytes {
		return &ValidationError{Fields: map[string]string{field: "must be a valid identifier"}}
	}
	return nil
}

func validateGrants(grants []EnvironmentGrant) ([]EnvironmentGrant, error) {
	if len(grants) > MaxGrantCount {
		return nil, &ValidationError{Fields: map[string]string{"grants": fmt.Sprintf("must contain at most %d grants", MaxGrantCount)}}
	}
	fields := make(map[string]string)
	unique := make(map[string]EnvironmentGrant, len(grants))
	for index, grant := range grants {
		grant.ProjectID = strings.TrimSpace(grant.ProjectID)
		grant.EnvironmentID = strings.TrimSpace(grant.EnvironmentID)
		if grant.Permission == "" {
			grant.Permission = GrantRead
		}
		if validateIdentifier("project_id", grant.ProjectID) != nil {
			fields[fmt.Sprintf("grants[%d].project_id", index)] = "must be a valid identifier"
		}
		if validateIdentifier("environment_id", grant.EnvironmentID) != nil {
			fields[fmt.Sprintf("grants[%d].environment_id", index)] = "must be a valid identifier"
		}
		if grant.Permission != GrantRead && grant.Permission != GrantWrite {
			fields[fmt.Sprintf("grants[%d].permission", index)] = "must be read or write"
		}
		unique[grant.ProjectID+"\x00"+grant.EnvironmentID] = grant
	}
	if len(fields) > 0 {
		return nil, &ValidationError{Fields: fields}
	}
	normalized := make([]EnvironmentGrant, 0, len(unique))
	for _, grant := range unique {
		normalized = append(normalized, grant)
	}
	sort.Slice(normalized, func(left, right int) bool {
		if normalized[left].ProjectID == normalized[right].ProjectID {
			return normalized[left].EnvironmentID < normalized[right].EnvironmentID
		}
		return normalized[left].ProjectID < normalized[right].ProjectID
	})
	return normalized, nil
}

func (s *Service) validateIssueToken(input IssueToken) (IssueToken, error) {
	name, err := validateName("name", input.Name)
	fields := validationFields(err)
	expiresAt := input.ExpiresAt.UTC().Truncate(time.Second)
	if expiryValidation := validateTokenExpiry(expiresAt, s.now().UTC()); expiryValidation != nil {
		for field, message := range expiryValidation.Fields {
			fields[field] = message
		}
	}
	if len(fields) > 0 {
		return IssueToken{}, &ValidationError{Fields: fields}
	}
	return IssueToken{Name: name, ExpiresAt: expiresAt}, nil
}

func validateTokenExpiry(expiresAt, now time.Time) *ValidationError {
	fields := make(map[string]string)
	if !expiresAt.After(now) {
		fields["expires_at"] = "must be strictly in the future"
	} else if expiresAt.After(now.Add(MaxTokenLifetime)) {
		fields["expires_at"] = fmt.Sprintf("must be no more than %s in the future", MaxTokenLifetime)
	}
	if len(fields) > 0 {
		return &ValidationError{Fields: fields}
	}
	return nil
}

func validationFields(err error) map[string]string {
	fields := make(map[string]string)
	var validation *ValidationError
	if errors.As(err, &validation) {
		for key, value := range validation.Fields {
			fields[key] = value
		}
	}
	return fields
}

func (s *Service) generateToken() (string, error) {
	randomBytes := make([]byte, tokenRandomBytes)
	if _, err := io.ReadFull(s.random, randomBytes); err != nil {
		return "", err
	}
	return tokenPrefix + base64.RawURLEncoding.EncodeToString(randomBytes), nil
}

func parseToken(plaintext string) ([sha256.Size]byte, error) {
	var hash [sha256.Size]byte
	if len(plaintext) != tokenLength || !strings.HasPrefix(plaintext, tokenPrefix) {
		return hash, ErrInvalidToken
	}
	raw := plaintext[len(tokenPrefix):]
	decoded, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil || len(decoded) != tokenRandomBytes || base64.RawURLEncoding.EncodeToString(decoded) != raw {
		return hash, ErrInvalidToken
	}
	return sha256.Sum256([]byte(plaintext)), nil
}

func isConstraintError(err error) bool {
	var sqliteErr *sqlite.Error
	return errors.As(err, &sqliteErr) && sqliteErr.Code()&0xff == 19
}
