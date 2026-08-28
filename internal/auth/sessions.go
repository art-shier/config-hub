package auth

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"confighub.local/internal/database"
)

const (
	sessionRandomBytes  = 32
	sessionEncodedBytes = 43
	sessionCookieLength = sessionEncodedBytes*2 + 1
)

var (
	ErrInvalidSession     = errors.New("invalid session")
	ErrInvalidCredentials = errors.New("invalid credentials")
)

type IssuedSession struct {
	CookieValue string
	CSRFToken   string
	ExpiresAt   time.Time
}

type SessionManager struct {
	store *database.Store
	key   []byte
	ttl   time.Duration
}

type CredentialService struct {
	store *database.Store
}

func NewCredentialService(store *database.Store) *CredentialService {
	return &CredentialService{store: store}
}

func (s *CredentialService) Authenticate(ctx context.Context, username, password string) (User, error) {
	if s == nil {
		return User{}, ErrInvalidCredentials
	}
	return AuthenticateCredentials(ctx, s.store, username, password)
}

// AuthenticateCredentials verifies one enabled user using an exact username
// match. Account lookup and password mismatches share one public error.
func AuthenticateCredentials(ctx context.Context, store *database.Store, username, password string) (User, error) {
	if store == nil || username == "" || password == "" {
		return User{}, ErrInvalidCredentials
	}
	var user User
	var passwordHash string
	var enabled int
	err := store.DB().QueryRowContext(ctx, `SELECT id, username, display_name, role, enabled, password_hash FROM users WHERE username = ? AND enabled = 1`, username).
		Scan(&user.ID, &user.Username, &user.DisplayName, &user.Role, &enabled, &passwordHash)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrInvalidCredentials
	}
	if err != nil {
		return User{}, fmt.Errorf("authenticate credentials: %w", err)
	}
	if !VerifyPassword(passwordHash, password) {
		return User{}, ErrInvalidCredentials
	}
	user.Enabled = enabled != 0
	return user, nil
}

func NewSessionManager(store *database.Store, key []byte, ttl time.Duration) *SessionManager {
	return &SessionManager{store: store, key: append([]byte(nil), key...), ttl: ttl}
}

func (m *SessionManager) Create(ctx context.Context, user User) (IssuedSession, error) {
	if !m.valid() || !user.Enabled || user.ID == "" {
		return IssuedSession{}, errors.New("cannot create session")
	}
	random, err := RandomOpaque("", sessionRandomBytes)
	if err != nil {
		return IssuedSession{}, err
	}
	cookie := random + "." + m.mac("session:", random)
	csrf := m.mac("csrf:", random)
	now := time.Now().UTC().Truncate(time.Second)
	expires := now.Add(m.ttl)
	err = m.store.InTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO sessions (id, user_id, token_hash, csrf_hash, expires_at, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
			uuid.NewString(), user.ID, SHA256(random), SHA256(csrf), expires.Unix(), now.Unix())
		return err
	})
	if err != nil {
		return IssuedSession{}, fmt.Errorf("create session: %w", err)
	}
	return IssuedSession{CookieValue: cookie, CSRFToken: csrf, ExpiresAt: expires}, nil
}

func (m *SessionManager) Authenticate(ctx context.Context, cookie string) (User, error) {
	user, _, err := m.AuthenticateWithExpiry(ctx, cookie)
	return user, err
}

// AuthenticateWithExpiry authenticates cookie and returns its server-side
// expiry for session bootstrap responses.
func (m *SessionManager) AuthenticateWithExpiry(ctx context.Context, cookie string) (User, time.Time, error) {
	random, ok := m.parseCookie(cookie)
	if !ok {
		return User{}, time.Time{}, ErrInvalidSession
	}
	var user User
	var enabled int
	var expires int64
	err := m.store.DB().QueryRowContext(ctx, `SELECT u.id, u.username, u.display_name, u.role, u.enabled, s.expires_at
		FROM sessions s JOIN users u ON u.id = s.user_id WHERE s.token_hash = ? AND u.enabled = 1`, SHA256(random)).
		Scan(&user.ID, &user.Username, &user.DisplayName, &user.Role, &enabled, &expires)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && expires <= time.Now().Unix()) {
		return User{}, time.Time{}, ErrInvalidSession
	}
	if err != nil {
		return User{}, time.Time{}, fmt.Errorf("authenticate session: %w", err)
	}
	user.Enabled = enabled != 0
	return user, time.Unix(expires, 0).UTC(), nil
}

func (m *SessionManager) ValidateCSRF(cookie, token string) bool {
	random, ok := m.parseCookie(cookie)
	if !ok || len(token) != sessionEncodedBytes {
		return false
	}
	decoded, err := base64.RawURLEncoding.Strict().DecodeString(token)
	if err != nil || len(decoded) != sha256.Size || base64.RawURLEncoding.EncodeToString(decoded) != token {
		return false
	}
	expected := m.mac("csrf:", random)
	return subtle.ConstantTimeCompare([]byte(expected), []byte(token)) == 1
}

// CSRFToken derives the CSRF token associated with an authenticated cookie.
func (m *SessionManager) CSRFToken(cookie string) (string, bool) {
	random, ok := m.parseCookie(cookie)
	if !ok {
		return "", false
	}
	return m.mac("csrf:", random), true
}

func (m *SessionManager) Revoke(ctx context.Context, cookie string) error {
	random, ok := m.parseCookie(cookie)
	if !ok {
		return ErrInvalidSession
	}
	_, err := m.store.DB().ExecContext(ctx, `DELETE FROM sessions WHERE token_hash = ?`, SHA256(random))
	if err != nil {
		return fmt.Errorf("revoke session: %w", err)
	}
	return nil
}

func (m *SessionManager) valid() bool {
	return m != nil && m.store != nil && len(m.key) >= 32 && m.ttl > 0
}

func (m *SessionManager) mac(domain, value string) string {
	mac := hmac.New(sha256.New, m.key)
	_, _ = mac.Write([]byte(domain))
	_, _ = mac.Write([]byte(value))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func (m *SessionManager) parseCookie(cookie string) (string, bool) {
	if !m.valid() || len(cookie) != sessionCookieLength || strings.Count(cookie, ".") != 1 {
		return "", false
	}
	random, signature, _ := strings.Cut(cookie, ".")
	if !canonicalBase64URL(random, sessionRandomBytes) || !canonicalBase64URL(signature, sha256.Size) {
		return "", false
	}
	expected := m.mac("session:", random)
	if subtle.ConstantTimeCompare([]byte(expected), []byte(signature)) != 1 {
		return "", false
	}
	return random, true
}

func canonicalBase64URL(value string, size int) bool {
	decoded, err := base64.RawURLEncoding.Strict().DecodeString(value)
	return err == nil && len(decoded) == size && base64.RawURLEncoding.EncodeToString(decoded) == value
}
