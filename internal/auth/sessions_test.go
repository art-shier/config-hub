package auth

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"confighub.local/internal/database"
)

func TestSessionCreateAuthenticateRevoke(t *testing.T) {
	ctx := context.Background()
	store := testStoreWithUser(t, "admin", "admin")
	manager := NewSessionManager(store, []byte("01234567890123456789012345678901"), time.Hour)
	issued, err := manager.Create(ctx, loadUserByUsername(t, store, "admin").User)
	if err != nil {
		t.Fatal(err)
	}
	user, err := manager.Authenticate(ctx, issued.CookieValue)
	if err != nil || user.Username != "admin" {
		t.Fatalf("user=%+v err=%v", user, err)
	}
	if err := manager.Revoke(ctx, issued.CookieValue); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Authenticate(ctx, issued.CookieValue); !errors.Is(err, ErrInvalidSession) {
		t.Fatalf("error=%v", err)
	}
}

func TestSessionCSRFStorageAndValidation(t *testing.T) {
	store := testStoreWithUser(t, "admin", "admin")
	manager := NewSessionManager(store, []byte("01234567890123456789012345678901"), time.Hour)
	issued, err := manager.Create(context.Background(), loadUserByUsername(t, store, "admin").User)
	if err != nil {
		t.Fatal(err)
	}
	if !manager.ValidateCSRF(issued.CookieValue, issued.CSRFToken) {
		t.Fatal("issued CSRF token did not validate")
	}
	if manager.ValidateCSRF(issued.CookieValue, issued.CSRFToken+"x") || manager.ValidateCSRF(issued.CookieValue+"x", issued.CSRFToken) {
		t.Fatal("malformed cookie or CSRF token validated")
	}
	derived, ok := manager.CSRFToken(issued.CookieValue)
	if !ok || derived != issued.CSRFToken {
		t.Fatalf("derived=%q ok=%v", derived, ok)
	}
	random := strings.Split(issued.CookieValue, ".")[0]
	var tokenHash, csrfHash []byte
	if err := store.DB().QueryRow(`SELECT token_hash, csrf_hash FROM sessions`).Scan(&tokenHash, &csrfHash); err != nil {
		t.Fatal(err)
	}
	if string(tokenHash) != string(SHA256(random)) || string(csrfHash) != string(SHA256(issued.CSRFToken)) {
		t.Fatal("session hashes do not match issued credentials")
	}
	var plaintextCount int
	if err := store.DB().QueryRow(`SELECT count(*) FROM sessions WHERE CAST(token_hash AS TEXT) IN (?, ?) OR CAST(csrf_hash AS TEXT) IN (?, ?)`, issued.CookieValue, random, issued.CSRFToken, issued.CookieValue).Scan(&plaintextCount); err != nil || plaintextCount != 0 {
		t.Fatalf("plaintext session material stored: count=%d err=%v", plaintextCount, err)
	}
}

func TestSessionRejectsExpiredDisabledWrongKeyAndTampering(t *testing.T) {
	ctx := context.Background()
	store := testStoreWithUser(t, "admin", "admin")
	key := []byte("01234567890123456789012345678901")
	manager := NewSessionManager(store, key, time.Hour)
	issued, err := manager.Create(ctx, loadUserByUsername(t, store, "admin").User)
	if err != nil {
		t.Fatal(err)
	}
	wrongKey := NewSessionManager(store, []byte("abcdefghijklmnopqrstuvwxyzABCDEF"), time.Hour)
	malformed := []string{"", "a.b", issued.CookieValue + "x", strings.Repeat("A", 1<<20)}
	tampered := issued.CookieValue[:len(issued.CookieValue)-1] + "A"
	malformed = append(malformed, tampered)
	for _, cookie := range malformed {
		if _, err := manager.Authenticate(ctx, cookie); !errors.Is(err, ErrInvalidSession) {
			t.Fatalf("cookie length=%d error=%v", len(cookie), err)
		}
	}
	if _, err := wrongKey.Authenticate(ctx, issued.CookieValue); !errors.Is(err, ErrInvalidSession) {
		t.Fatalf("wrong key error=%v", err)
	}
	if _, err := store.DB().Exec(`UPDATE sessions SET expires_at = ?`, time.Now().Add(-time.Minute).Unix()); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Authenticate(ctx, issued.CookieValue); !errors.Is(err, ErrInvalidSession) {
		t.Fatalf("expired error=%v", err)
	}
	if _, err := store.DB().Exec(`UPDATE sessions SET expires_at = ?`, time.Now().Add(time.Hour).Unix()); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB().Exec(`UPDATE users SET enabled = 0 WHERE username = 'admin'`); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Authenticate(ctx, issued.CookieValue); !errors.Is(err, ErrInvalidSession) {
		t.Fatalf("disabled error=%v", err)
	}
}

func TestSessionManagerRejectsInvalidCreationInputs(t *testing.T) {
	store := testStoreWithUser(t, "admin", "admin")
	user := loadUserByUsername(t, store, "admin").User
	for _, manager := range []*SessionManager{
		NewSessionManager(nil, []byte("01234567890123456789012345678901"), time.Hour),
		NewSessionManager(store, []byte("short"), time.Hour),
		NewSessionManager(store, []byte("01234567890123456789012345678901"), 0),
	} {
		if _, err := manager.Create(context.Background(), user); err == nil {
			t.Fatal("invalid manager created a session")
		}
	}
	user.Enabled = false
	if _, err := NewSessionManager(store, []byte("01234567890123456789012345678901"), time.Hour).Create(context.Background(), user); err == nil {
		t.Fatal("disabled user received a session")
	}
}

func TestAuthenticateCredentialsUsesExactUsernameAndUniformErrors(t *testing.T) {
	ctx := context.Background()
	store := testStoreWithUser(t, "Admin", "secret")
	user, err := AuthenticateCredentials(ctx, store, "Admin", "secret")
	if err != nil || user.Username != "Admin" {
		t.Fatalf("user=%+v err=%v", user, err)
	}
	for _, attempt := range []struct{ username, password string }{
		{"Admin", "wrong"}, {"admin", "secret"}, {"missing", "secret"}, {" Admin ", "secret"},
	} {
		if _, err := AuthenticateCredentials(ctx, store, attempt.username, attempt.password); !errors.Is(err, ErrInvalidCredentials) || strings.Contains(err.Error(), attempt.password) {
			t.Fatalf("attempt=%+v err=%v", attempt, err)
		}
	}
	if _, err := store.DB().Exec(`UPDATE users SET enabled = 0 WHERE username = 'Admin'`); err != nil {
		t.Fatal(err)
	}
	if _, err := AuthenticateCredentials(ctx, store, "Admin", "secret"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("disabled error=%v", err)
	}
}

func TestAuthenticateCredentialsDatabaseFailureIsNotCredentialFailure(t *testing.T) {
	store := testStoreWithUser(t, "admin", "secret")
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	_, err := AuthenticateCredentials(context.Background(), store, "admin", "secret")
	if err == nil || errors.Is(err, ErrInvalidCredentials) || errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("error=%v", err)
	}
}

func TestSessionIssuedExpiryMatchesAuthenticatedRecord(t *testing.T) {
	store := testStoreWithUser(t, "admin", "secret")
	manager := NewSessionManager(store, []byte("01234567890123456789012345678901"), time.Hour)
	issued, err := manager.Create(context.Background(), loadUserByUsername(t, store, "admin").User)
	if err != nil {
		t.Fatal(err)
	}
	_, expires, err := manager.AuthenticateWithExpiry(context.Background(), issued.CookieValue)
	if err != nil {
		t.Fatal(err)
	}
	if !expires.Equal(issued.ExpiresAt) {
		t.Fatalf("stored expiry=%s issued expiry=%s", expires, issued.ExpiresAt)
	}
}

func TestSessionDatabaseFailureIsNotInvalidSession(t *testing.T) {
	store := testStoreWithUser(t, "admin", "secret")
	manager := NewSessionManager(store, []byte("01234567890123456789012345678901"), time.Hour)
	issued, err := manager.Create(context.Background(), loadUserByUsername(t, store, "admin").User)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	_, err = manager.Authenticate(context.Background(), issued.CookieValue)
	if err == nil || errors.Is(err, ErrInvalidSession) {
		t.Fatalf("database error=%v", err)
	}
}

func testStoreWithUser(t *testing.T, username, password string) *database.Store {
	t.Helper()
	store := testStore(t)
	file := UserFile{Users: []UserSpec{{
		Username: username, DisplayName: "Administrator", Password: password, Role: "admin", Enabled: true,
	}}}
	if _, err := NewUserSyncer(store).Sync(context.Background(), file); err != nil {
		t.Fatal(err)
	}
	return store
}
