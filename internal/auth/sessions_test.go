package auth

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"sync"
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
	replacement := byte('A')
	if issued.CookieValue[len(issued.CookieValue)-1] == replacement {
		replacement = 'B'
	}
	tampered := issued.CookieValue[:len(issued.CookieValue)-1] + string(replacement)
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

func TestCredentialServiceVerifiesEveryExistingAndMissingAccountOnce(t *testing.T) {
	store := testStoreWithUser(t, "admin", "secret")
	service := NewCredentialService(store)
	calls := 0
	service.verifyPassword = func(hash, password string) bool {
		calls++
		return VerifyPassword(hash, password)
	}

	assertInvalidOnce := func(username, password string) {
		t.Helper()
		calls = 0
		_, err := service.Verify(context.Background(), username, password)
		if !errors.Is(err, ErrInvalidCredentials) || calls != 1 {
			t.Fatalf("username=%q err=%v verify calls=%d", username, err, calls)
		}
	}
	assertInvalidOnce("admin", "wrong")
	assertInvalidOnce("missing", "wrong")
	if _, err := store.DB().Exec(`UPDATE users SET enabled = 0 WHERE username = 'admin'`); err != nil {
		t.Fatal(err)
	}
	assertInvalidOnce("admin", "secret")
	if _, err := store.DB().Exec(`UPDATE users SET enabled = 1, password_hash = 'malformed' WHERE username = 'admin'`); err != nil {
		t.Fatal(err)
	}
	assertInvalidOnce("admin", "secret")
}

func TestPasswordRotationBetweenVerifyAndSessionInsertRejectsOldCredential(t *testing.T) {
	ctx := context.Background()
	store := testStoreWithUser(t, "admin", "old-password")
	service := NewCredentialService(store)
	verified := make(chan struct{})
	resume := make(chan struct{})
	service.afterVerify = func() {
		close(verified)
		<-resume
	}

	type result struct {
		credential VerifiedCredential
		err        error
	}
	resultCh := make(chan result, 1)
	go func() {
		credential, err := service.Verify(ctx, "admin", "old-password")
		resultCh <- result{credential: credential, err: err}
	}()
	<-verified
	file := UserFile{Users: []UserSpec{{Username: "admin", DisplayName: "Admin", Password: "new-password", Role: "admin", Enabled: true}}}
	if _, err := NewUserSyncer(store).Sync(ctx, file); err != nil {
		t.Fatal(err)
	}
	close(resume)
	verifiedResult := <-resultCh
	if verifiedResult.err != nil {
		t.Fatal(verifiedResult.err)
	}
	manager := NewSessionManager(store, []byte("01234567890123456789012345678901"), time.Hour)
	if _, err := manager.CreateVerified(ctx, verifiedResult.credential); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("create error=%v", err)
	}
	if count := countSessions(t, store, verifiedResult.credential.User.ID); count != 0 {
		t.Fatalf("old credential created %d sessions", count)
	}
}

func TestCredentialServiceBoundsConcurrentPasswordVerification(t *testing.T) {
	store := testStoreWithUser(t, "admin", "secret")
	service := NewCredentialService(store)
	started := make(chan struct{}, credentialVerifyConcurrency+1)
	release := make(chan struct{})
	service.verifyPassword = func(string, string) bool {
		started <- struct{}{}
		<-release
		return false
	}

	var workers sync.WaitGroup
	for range credentialVerifyConcurrency {
		workers.Add(1)
		go func() {
			defer workers.Done()
			_, _ = service.Verify(context.Background(), "admin", "wrong")
		}()
	}
	for range credentialVerifyConcurrency {
		<-started
	}

	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		_, err := service.Verify(ctx, "admin", "wrong")
		result <- err
	}()
	cancel()
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatalf("queued verification error=%v", err)
	}
	select {
	case <-started:
		t.Fatal("verification concurrency exceeded gate capacity")
	default:
	}
	close(release)
	workers.Wait()
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
