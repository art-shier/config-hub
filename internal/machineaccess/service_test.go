package machineaccess

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"confighub.local/internal/auth"
	"confighub.local/internal/database"
	"confighub.local/internal/revisions"
)

var machineTestNow = time.Date(2026, time.August, 29, 12, 0, 0, 0, time.UTC)

type machineServiceFixture struct {
	service                 *Service
	store                   *database.Store
	admin, member, disabled auth.User
	allowedEnv, deniedEnv   testEnvironment
	otherProjectEnvironment testEnvironment
}

type testEnvironment struct {
	ID, ProjectID, ProjectSlug, Slug string
}

func TestIssuedTokenIsShownOnceAndScopeIsEnforced(t *testing.T) {
	fixture := newMachineServiceFixture(t)
	identity := createMachineIdentity(t, fixture, "shop-ci")
	replaceMachineGrants(t, fixture, identity.ID, []EnvironmentGrant{{ProjectID: fixture.allowedEnv.ProjectID, EnvironmentID: fixture.allowedEnv.ID}})

	issued, err := fixture.service.IssueToken(context.Background(), fixture.admin, identity.ID, IssueToken{Name: "primary", ExpiresAt: machineTestNow.Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(issued.Plaintext, "ch_") || len(issued.Plaintext) != 46 {
		t.Fatalf("token format prefix=%t length=%d", strings.HasPrefix(issued.Plaintext, "ch_"), len(issued.Plaintext))
	}
	decoded, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(issued.Plaintext, "ch_"))
	if err != nil || len(decoded) != 32 || base64.RawURLEncoding.EncodeToString(decoded) != strings.TrimPrefix(issued.Plaintext, "ch_") {
		t.Fatalf("token is not canonical RawURL Base64: decoded=%d err=%v", len(decoded), err)
	}
	if issued.Prefix != issued.Plaintext[:10] {
		t.Fatalf("prefix=%q want=%q", issued.Prefix, issued.Plaintext[:10])
	}
	if _, err := fixture.service.AuthenticateForEnvironment(context.Background(), issued.Plaintext, fixture.allowedEnv.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.AuthenticateForEnvironment(context.Background(), issued.Plaintext, fixture.deniedEnv.ID); !errors.Is(err, ErrScopeDenied) {
		t.Fatalf("error=%v", err)
	}

	detail, err := fixture.service.GetIdentity(context.Background(), fixture.admin, identity.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(detail.Tokens) != 1 || detail.Tokens[0].ID != issued.ID || detail.Tokens[0].Prefix != issued.Prefix {
		t.Fatalf("token metadata=%+v", detail.Tokens)
	}
	if strings.Contains(fmt.Sprintf("%+v", detail), issued.Plaintext) {
		t.Fatal("identity detail returned plaintext")
	}
}

func TestMachineCurrentConfigBindsProjectEnvironmentAndFiltersService(t *testing.T) {
	fixture := newMachineServiceFixture(t)
	identity := createMachineIdentity(t, fixture, "reader-ci")
	replaceMachineGrants(t, fixture, identity.ID, []EnvironmentGrant{{ProjectID: fixture.allowedEnv.ProjectID, EnvironmentID: fixture.allowedEnv.ID}})
	issued := issueMachineToken(t, fixture, identity.ID, "primary", machineTestNow.Add(time.Hour))
	if _, err := fixture.store.DB().Exec(`INSERT INTO revisions (id, environment_id, version, message, created_by, created_at)
		VALUES ('machine-revision', ?, 7, 'machine contract', ?, ?)`, fixture.allowedEnv.ID, fixture.admin.ID, machineTestNow.Unix()); err != nil {
		t.Fatal(err)
	}
	for _, entry := range []struct{ key, value, service string }{
		{"DATABASE_URL", "postgres://do-not-log", "api"},
		{"PORT", "8080", ""},
		{"WORKERS", "4", "worker"},
	} {
		var service any
		if entry.service != "" {
			service = entry.service
		}
		if _, err := fixture.store.DB().Exec(`INSERT INTO revision_entries (revision_id, key, value, service) VALUES ('machine-revision', ?, ?, ?)`, entry.key, entry.value, service); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := fixture.store.DB().Exec(`UPDATE environments SET current_revision_id = 'machine-revision' WHERE id = ?`, fixture.allowedEnv.ID); err != nil {
		t.Fatal(err)
	}

	config, err := fixture.service.ReadCurrentForProject(context.Background(), issued.Plaintext, fixture.allowedEnv.ProjectSlug, fixture.allowedEnv.Slug, "api")
	if err != nil {
		t.Fatal(err)
	}
	if config.Project != "shop" || config.Environment != "production" || config.Revision != 7 || len(config.Values) != 1 || config.Values["DATABASE_URL"] != "postgres://do-not-log" {
		t.Fatalf("config=%+v", config)
	}
	for _, route := range []struct{ project, environment string }{
		{project: fixture.otherProjectEnvironment.ProjectSlug, environment: fixture.otherProjectEnvironment.Slug},
		{project: fixture.otherProjectEnvironment.ProjectSlug, environment: fixture.allowedEnv.Slug},
		{project: fixture.allowedEnv.ProjectSlug, environment: "missing"},
	} {
		if _, err := fixture.service.ReadCurrentForProject(context.Background(), issued.Plaintext, route.project, route.environment, ""); !errors.Is(err, ErrScopeDenied) {
			t.Fatalf("route=%+v error=%v", route, err)
		}
	}
	if _, err := fixture.service.ReadCurrentForProject(context.Background(), "ch_invalid", fixture.allowedEnv.ProjectSlug, fixture.allowedEnv.Slug, ""); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("invalid token error=%v", err)
	}
}

func TestMachineCurrentConfigRejectsInvalidCurrentRevisionMetadata(t *testing.T) {
	fixture := newMachineServiceFixture(t)
	identity := createMachineIdentity(t, fixture, "integrity-ci")
	replaceMachineGrants(t, fixture, identity.ID, []EnvironmentGrant{{ProjectID: fixture.allowedEnv.ProjectID, EnvironmentID: fixture.allowedEnv.ID}})
	issued := issueMachineToken(t, fixture, identity.ID, "primary", machineTestNow.Add(time.Hour))
	if _, err := fixture.store.DB().Exec(`INSERT INTO revisions (id, environment_id, version, created_by, created_at)
		VALUES ('invalid-machine-revision', ?, 0, ?, ?)`, fixture.allowedEnv.ID, fixture.admin.ID, machineTestNow.Unix()); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.store.DB().Exec(`INSERT INTO revision_entries (revision_id, key, value) VALUES ('invalid-machine-revision', 'SECRET', 'do-not-return')`); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.store.DB().Exec(`UPDATE environments SET current_revision_id = 'invalid-machine-revision' WHERE id = ?`, fixture.allowedEnv.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.ReadCurrentForProject(context.Background(), issued.Plaintext, fixture.allowedEnv.ProjectSlug, fixture.allowedEnv.Slug, ""); !errors.Is(err, ErrDataIntegrity) {
		t.Fatalf("error=%v", err)
	}
}

func TestMachineCurrentConfigRejectsCorruptStoredSnapshot(t *testing.T) {
	for _, test := range []struct {
		name    string
		service string
		insert  func(*testing.T, *machineServiceFixture)
	}{
		{
			name: "unsealed revision",
			insert: func(t *testing.T, fixture *machineServiceFixture) {
				t.Helper()
				if _, err := fixture.store.DB().Exec(`DROP TRIGGER environments_seal_current_revision_update`); err != nil {
					t.Fatal(err)
				}
				if _, err := fixture.store.DB().Exec(`INSERT INTO revision_entries (revision_id, key, value) VALUES ('corrupt-machine-revision', 'SECRET', 'unsealed-secret')`); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name:    "filtered invalid UTF-8 value",
			service: "api",
			insert: func(t *testing.T, fixture *machineServiceFixture) {
				t.Helper()
				if _, err := fixture.store.DB().Exec(`INSERT INTO revision_entries (revision_id, key, value, service) VALUES
					('corrupt-machine-revision', 'VISIBLE', 'safe', 'api'),
					('corrupt-machine-revision', 'HIDDEN', CAST(X'FF' AS TEXT), 'worker')`); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "invalid key",
			insert: func(t *testing.T, fixture *machineServiceFixture) {
				t.Helper()
				if _, err := fixture.store.DB().Exec(`INSERT INTO revision_entries (revision_id, key, value) VALUES ('corrupt-machine-revision', 'INVALID-KEY', 'invalid-key-secret')`); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "aggregate budget exceeded",
			insert: func(t *testing.T, fixture *machineServiceFixture) {
				t.Helper()
				if _, err := fixture.store.DB().Exec(`INSERT INTO revision_entries (revision_id, key, value) VALUES
					('corrupt-machine-revision', 'ONE', ?), ('corrupt-machine-revision', 'TWO', ?)`,
					strings.Repeat("1", revisions.MaxSnapshotBytes/2), strings.Repeat("2", revisions.MaxSnapshotBytes/2)); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name:    "filtered non-canonical service",
			service: "api",
			insert: func(t *testing.T, fixture *machineServiceFixture) {
				t.Helper()
				if _, err := fixture.store.DB().Exec(`INSERT INTO revision_entries (revision_id, key, value, service) VALUES
					('corrupt-machine-revision', 'VISIBLE', 'safe', 'api'),
					('corrupt-machine-revision', 'HIDDEN', 'non-canonical-service-secret', ' worker ')`); err != nil {
					t.Fatal(err)
				}
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newMachineServiceFixture(t)
			identity := createMachineIdentity(t, fixture, "corrupt-reader-ci")
			replaceMachineGrants(t, fixture, identity.ID, []EnvironmentGrant{{ProjectID: fixture.allowedEnv.ProjectID, EnvironmentID: fixture.allowedEnv.ID}})
			issued := issueMachineToken(t, fixture, identity.ID, "primary", machineTestNow.Add(time.Hour))
			if _, err := fixture.store.DB().Exec(`INSERT INTO revisions (id, environment_id, version, created_by, created_at)
				VALUES ('corrupt-machine-revision', ?, 9, ?, ?)`, fixture.allowedEnv.ID, fixture.admin.ID, machineTestNow.Unix()); err != nil {
				t.Fatal(err)
			}
			test.insert(t, fixture)
			if _, err := fixture.store.DB().Exec(`UPDATE environments SET current_revision_id = 'corrupt-machine-revision' WHERE id = ?`, fixture.allowedEnv.ID); err != nil {
				t.Fatal(err)
			}

			config, err := fixture.service.ReadCurrentForProject(context.Background(), issued.Plaintext, fixture.allowedEnv.ProjectSlug, fixture.allowedEnv.Slug, test.service)
			if !errors.Is(err, ErrDataIntegrity) {
				t.Fatalf("config=%+v error=%v", config, err)
			}
			for _, secret := range []string{"unsealed-secret", "invalid-key-secret", "non-canonical-service-secret"} {
				if strings.Contains(fmt.Sprint(err), secret) {
					t.Fatalf("integrity error leaked %q: %v", secret, err)
				}
			}
		})
	}
}

func TestMachineCurrentReadKeepsSnapshotWithoutBlockingSecurityOrConfigWrites(t *testing.T) {
	fixture := newMachineServiceFixture(t)
	identity := createMachineIdentity(t, fixture, "snapshot-ci")
	replaceMachineGrants(t, fixture, identity.ID, []EnvironmentGrant{{ProjectID: fixture.allowedEnv.ProjectID, EnvironmentID: fixture.allowedEnv.ID}})
	issued := issueMachineToken(t, fixture, identity.ID, "primary", machineTestNow.Add(time.Hour))
	revisionService := revisions.NewService(fixture.store)
	first, err := revisionService.ReplaceForProject(context.Background(), fixture.admin, fixture.allowedEnv.ProjectSlug, fixture.allowedEnv.Slug, revisions.ReplaceInput{
		Entries: []revisions.Entry{{Key: "VALUE", Value: "before"}},
	})
	if err != nil {
		t.Fatal(err)
	}

	readStarted := make(chan struct{})
	releaseRead := make(chan struct{})
	var once sync.Once
	fixture.service.afterMachineAuthorization = func() {
		once.Do(func() {
			close(readStarted)
			<-releaseRead
		})
	}
	type readResult struct {
		config CurrentConfig
		err    error
	}
	result := make(chan readResult, 1)
	go func() {
		config, err := fixture.service.ReadCurrentForProject(context.Background(), issued.Plaintext, fixture.allowedEnv.ProjectSlug, fixture.allowedEnv.Slug, "")
		result <- readResult{config: config, err: err}
	}()
	<-readStarted

	writes := make(chan error, 1)
	go func() {
		if _, err := revisionService.ReplaceForProject(context.Background(), fixture.admin, fixture.allowedEnv.ProjectSlug, fixture.allowedEnv.Slug, revisions.ReplaceInput{
			BaseRevision: first.Version, Entries: []revisions.Entry{{Key: "VALUE", Value: "after"}},
		}); err != nil {
			writes <- err
			return
		}
		if err := fixture.service.ReplaceGrants(context.Background(), fixture.admin, identity.ID, nil); err != nil {
			writes <- err
			return
		}
		writes <- fixture.service.RevokeToken(context.Background(), fixture.admin, identity.ID, issued.ID)
	}()
	select {
	case err := <-writes:
		if err != nil {
			close(releaseRead)
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		close(releaseRead)
		<-result
		t.Fatal("machine read blocked revoke, grant, or config write")
	}
	close(releaseRead)
	read := <-result
	if read.err != nil || read.config.Revision != 1 || read.config.Values["VALUE"] != "before" {
		t.Fatalf("snapshot config=%+v error=%v", read.config, read.err)
	}
	if _, err := fixture.service.ReadCurrentForProject(context.Background(), issued.Plaintext, fixture.allowedEnv.ProjectSlug, fixture.allowedEnv.Slug, ""); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("revocation after snapshot error=%v", err)
	}
}

func TestCanonicalInvalidTokenReadDoesNotBlockWriter(t *testing.T) {
	fixture := newMachineServiceFixture(t)
	identity := createMachineIdentity(t, fixture, "invalid-reader-ci")
	issued := issueMachineToken(t, fixture, identity.ID, "primary", machineTestNow.Add(time.Hour))
	unknown := issued.Plaintext
	if unknown[3] == 'A' {
		unknown = unknown[:3] + "B" + unknown[4:]
	} else {
		unknown = unknown[:3] + "A" + unknown[4:]
	}
	readStarted := make(chan struct{})
	releaseRead := make(chan struct{})
	fixture.service.afterReadTxBegin = func() {
		close(readStarted)
		<-releaseRead
	}
	readResult := make(chan error, 1)
	go func() {
		_, err := fixture.service.ReadCurrentForProject(context.Background(), unknown, fixture.allowedEnv.ProjectSlug, fixture.allowedEnv.Slug, "")
		readResult <- err
	}()
	<-readStarted

	writeResult := make(chan error, 1)
	go func() {
		writeResult <- fixture.service.RevokeToken(context.Background(), fixture.admin, identity.ID, issued.ID)
	}()
	select {
	case err := <-writeResult:
		if err != nil {
			close(releaseRead)
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		close(releaseRead)
		<-readResult
		t.Fatal("canonical invalid token read blocked writer")
	}
	close(releaseRead)
	if err := <-readResult; !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("read error=%v", err)
	}
}

func TestTokenPlaintextIsNeverPersisted(t *testing.T) {
	fixture := newMachineServiceFixture(t)
	identity := createMachineIdentity(t, fixture, "persist-ci")
	issued, err := fixture.service.IssueToken(context.Background(), fixture.admin, identity.ID, IssueToken{Name: "primary", ExpiresAt: machineTestNow.Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}

	var hash []byte
	var prefix string
	if err := fixture.store.DB().QueryRow(`SELECT token_hash, prefix FROM access_tokens WHERE id = ?`, issued.ID).Scan(&hash, &prefix); err != nil {
		t.Fatal(err)
	}
	wantHash := sha256.Sum256([]byte(issued.Plaintext))
	if string(hash) != string(wantHash[:]) || prefix != issued.Plaintext[:10] || string(hash) == issued.Plaintext {
		t.Fatalf("stored hash length=%d prefix=%q", len(hash), prefix)
	}
	rows, err := fixture.store.DB().Query(`PRAGMA table_info(access_tokens)`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, kind string
		var notNull, primaryKey int
		var defaultValue any
		if err := rows.Scan(&cid, &name, &kind, &notNull, &defaultValue, &primaryKey); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(strings.ToLower(name), "plain") || name == "token" {
			t.Fatalf("plaintext-capable column exists: %q", name)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
}

func TestTokenStateChangesTakeEffectImmediatelyAndRotationOverlaps(t *testing.T) {
	fixture := newMachineServiceFixture(t)
	identity := createMachineIdentity(t, fixture, "rotate-ci")
	replaceMachineGrants(t, fixture, identity.ID, []EnvironmentGrant{{ProjectID: fixture.allowedEnv.ProjectID, EnvironmentID: fixture.allowedEnv.ID}})
	primary := issueMachineToken(t, fixture, identity.ID, "primary", machineTestNow.Add(time.Hour))
	secondary := issueMachineToken(t, fixture, identity.ID, "secondary", machineTestNow.Add(2*time.Hour))
	for _, token := range []string{primary.Plaintext, secondary.Plaintext} {
		if _, err := fixture.service.AuthenticateForEnvironment(context.Background(), token, fixture.allowedEnv.ID); err != nil {
			t.Fatalf("overlapping token rejected: %v", err)
		}
	}

	if err := fixture.service.RevokeToken(context.Background(), fixture.admin, identity.ID, primary.ID); err != nil {
		t.Fatal(err)
	}
	if err := fixture.service.RevokeToken(context.Background(), fixture.admin, identity.ID, primary.ID); err != nil {
		t.Fatalf("idempotent revoke failed: %v", err)
	}
	if _, err := fixture.service.AuthenticateForEnvironment(context.Background(), primary.Plaintext, fixture.allowedEnv.ID); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("revoked error=%v", err)
	}
	if _, err := fixture.service.AuthenticateForEnvironment(context.Background(), secondary.Plaintext, fixture.allowedEnv.ID); err != nil {
		t.Fatalf("secondary token affected by primary revoke: %v", err)
	}

	replaceMachineGrants(t, fixture, identity.ID, []EnvironmentGrant{{ProjectID: fixture.deniedEnv.ProjectID, EnvironmentID: fixture.deniedEnv.ID}})
	if _, err := fixture.service.AuthenticateForEnvironment(context.Background(), secondary.Plaintext, fixture.allowedEnv.ID); !errors.Is(err, ErrScopeDenied) {
		t.Fatalf("removed grant error=%v", err)
	}
	if _, err := fixture.service.AuthenticateForEnvironment(context.Background(), secondary.Plaintext, fixture.deniedEnv.ID); err != nil {
		t.Fatalf("replacement grant did not take effect: %v", err)
	}

	if _, err := fixture.service.UpdateIdentity(context.Background(), fixture.admin, identity.ID, UpdateIdentityInput{Description: "paused", Enabled: false}); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.AuthenticateForEnvironment(context.Background(), secondary.Plaintext, fixture.deniedEnv.ID); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("disabled identity error=%v", err)
	}
}

func TestTokenExpiryUsesStrictBoundaryAndMalformedTokensAreUniformlyRejected(t *testing.T) {
	fixture := newMachineServiceFixture(t)
	identity := createMachineIdentity(t, fixture, "expiry-ci")
	replaceMachineGrants(t, fixture, identity.ID, []EnvironmentGrant{{ProjectID: fixture.allowedEnv.ProjectID, EnvironmentID: fixture.allowedEnv.ID}})
	issued := issueMachineToken(t, fixture, identity.ID, "boundary", machineTestNow.Add(time.Second))

	fixture.service.now = func() time.Time { return machineTestNow.Add(999 * time.Millisecond) }
	if _, err := fixture.service.AuthenticateForEnvironment(context.Background(), issued.Plaintext, fixture.allowedEnv.ID); err != nil {
		t.Fatalf("token rejected before expiry: %v", err)
	}
	fixture.service.now = func() time.Time { return machineTestNow.Add(time.Second) }
	if _, err := fixture.service.AuthenticateForEnvironment(context.Background(), issued.Plaintext, fixture.allowedEnv.ID); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("boundary error=%v", err)
	}

	raw := strings.TrimPrefix(issued.Plaintext, "ch_")
	malformed := []string{"", "ch_", "xx_" + raw, issued.Plaintext + "=", "ch_" + raw[:42], "ch_" + raw[:42] + "A", "Bearer " + issued.Plaintext}
	for _, token := range malformed {
		if token == issued.Plaintext {
			continue
		}
		if _, err := fixture.service.AuthenticateForEnvironment(context.Background(), token, fixture.allowedEnv.ID); !errors.Is(err, ErrInvalidToken) {
			t.Fatalf("token length=%d error=%v", len(token), err)
		}
	}
	unknown := issued.Plaintext
	if unknown[3] == 'A' {
		unknown = unknown[:3] + "B" + unknown[4:]
	} else {
		unknown = unknown[:3] + "A" + unknown[4:]
	}
	if _, err := fixture.service.AuthenticateForEnvironment(context.Background(), unknown, ""); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("unknown canonical token with invalid environment error=%v", err)
	}
	if _, err := fixture.service.ReadCurrentForProject(context.Background(), unknown, "INVALID", "", ""); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("unknown canonical token with invalid route error=%v", err)
	}
	if _, err := fixture.service.ReadCurrentForProject(context.Background(), unknown, fixture.allowedEnv.ProjectSlug, fixture.allowedEnv.Slug, strings.Repeat("x", MaxNameBytes+1)); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("unknown canonical token with invalid filter error=%v", err)
	}
}

func TestIssueTokenValidatesExpiryAndUniqueName(t *testing.T) {
	fixture := newMachineServiceFixture(t)
	identity := createMachineIdentity(t, fixture, "issue-ci")
	for _, test := range []struct {
		name      string
		input     IssueToken
		wantError error
	}{
		{name: "not future", input: IssueToken{Name: "past", ExpiresAt: machineTestNow}, wantError: ErrInvalid},
		{name: "too far", input: IssueToken{Name: "far", ExpiresAt: machineTestNow.Add(MaxTokenLifetime + time.Second)}, wantError: ErrInvalid},
		{name: "blank name", input: IssueToken{Name: " ", ExpiresAt: machineTestNow.Add(time.Hour)}, wantError: ErrInvalid},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := fixture.service.IssueToken(context.Background(), fixture.admin, identity.ID, test.input); !errors.Is(err, test.wantError) {
				t.Fatalf("error=%v", err)
			}
		})
	}
	issueMachineToken(t, fixture, identity.ID, "same", machineTestNow.Add(time.Hour))
	if _, err := fixture.service.IssueToken(context.Background(), fixture.admin, identity.ID, IssueToken{Name: " same ", ExpiresAt: machineTestNow.Add(2 * time.Hour)}); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate token name error=%v", err)
	}
	if _, err := fixture.service.UpdateIdentity(context.Background(), fixture.admin, identity.ID, UpdateIdentityInput{Enabled: false}); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.IssueToken(context.Background(), fixture.admin, identity.ID, IssueToken{Name: "disabled", ExpiresAt: machineTestNow.Add(time.Hour)}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("disabled identity issue error=%v", err)
	}
	if _, err := fixture.service.IssueToken(context.Background(), fixture.admin, "missing", IssueToken{Name: "missing", ExpiresAt: machineTestNow.Add(time.Hour)}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing identity issue error=%v", err)
	}
}

func TestIssueTokenRevalidatesExpiryAfterWaitingForWriteLock(t *testing.T) {
	for _, test := range []struct {
		name, tokenName        string
		expiresAt, postLockNow time.Time
	}{
		{name: "expired while waiting", tokenName: "expired", expiresAt: machineTestNow.Add(time.Second), postLockNow: machineTestNow.Add(time.Second)},
		{name: "too far after clock rollback", tokenName: "too-far", expiresAt: machineTestNow.Add(MaxTokenLifetime), postLockNow: machineTestNow.Add(-time.Second)},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newMachineServiceFixture(t)
			identity := createMachineIdentity(t, fixture, "delayed-issue-ci")
			fixture.service.random = bytes.NewReader(make([]byte, tokenRandomBytes))

			writer, err := fixture.store.DB().Begin()
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = writer.Rollback() }()
			if _, err := writer.Exec(`UPDATE machine_identities SET updated_at = updated_at WHERE id = ?`, identity.ID); err != nil {
				t.Fatal(err)
			}

			clockCalls := make(chan time.Time, 4)
			call := 0
			fixture.service.now = func() time.Time {
				call++
				value := machineTestNow
				if call > 1 {
					value = test.postLockNow
				}
				clockCalls <- value
				return value
			}
			type issueResult struct {
				issued IssuedToken
				err    error
			}
			result := make(chan issueResult, 1)
			go func() {
				issued, err := fixture.service.IssueToken(context.Background(), fixture.admin, identity.ID, IssueToken{Name: test.tokenName, ExpiresAt: test.expiresAt})
				result <- issueResult{issued: issued, err: err}
			}()

			if got := <-clockCalls; !got.Equal(machineTestNow) {
				t.Fatalf("initial validation time=%s", got)
			}
			select {
			case got := <-clockCalls:
				t.Fatalf("issuance time sampled before writer lock was acquired: %s", got)
			case <-time.After(100 * time.Millisecond):
			}
			if err := writer.Rollback(); err != nil {
				t.Fatal(err)
			}

			completed := <-result
			if !errors.Is(completed.err, ErrInvalid) || completed.issued.Plaintext != "" {
				t.Fatalf("issued=%+v error=%v", completed.issued, completed.err)
			}
			var tokenCount int
			if err := fixture.store.DB().QueryRow(`SELECT COUNT(*) FROM access_tokens WHERE identity_id = ?`, identity.ID).Scan(&tokenCount); err != nil || tokenCount != 0 {
				t.Fatalf("token count=%d error=%v", tokenCount, err)
			}
		})
	}
}

func TestIssueTokenUsesOnePostLockTimeForValidationAndCreation(t *testing.T) {
	fixture := newMachineServiceFixture(t)
	identity := createMachineIdentity(t, fixture, "created-at-ci")
	fixture.service.random = bytes.NewReader(make([]byte, tokenRandomBytes))

	writer, err := fixture.store.DB().Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = writer.Rollback() }()
	if _, err := writer.Exec(`UPDATE machine_identities SET updated_at = updated_at WHERE id = ?`, identity.ID); err != nil {
		t.Fatal(err)
	}

	postLockNow := machineTestNow.Add(time.Hour)
	clockCalls := make(chan time.Time, 4)
	call := 0
	fixture.service.now = func() time.Time {
		call++
		value := machineTestNow
		if call > 1 {
			value = postLockNow
		}
		clockCalls <- value
		return value
	}
	result := make(chan struct {
		issued IssuedToken
		err    error
	}, 1)
	go func() {
		issued, err := fixture.service.IssueToken(context.Background(), fixture.admin, identity.ID, IssueToken{Name: "created-at", ExpiresAt: machineTestNow.Add(2 * time.Hour)})
		result <- struct {
			issued IssuedToken
			err    error
		}{issued: issued, err: err}
	}()

	if got := <-clockCalls; !got.Equal(machineTestNow) {
		t.Fatalf("initial validation time=%s", got)
	}
	select {
	case got := <-clockCalls:
		t.Fatalf("issuance time sampled before writer lock was acquired: %s", got)
	case <-time.After(100 * time.Millisecond):
	}
	if err := writer.Rollback(); err != nil {
		t.Fatal(err)
	}

	completed := <-result
	if completed.err != nil {
		t.Fatal(completed.err)
	}
	var createdAt int64
	if err := fixture.store.DB().QueryRow(`SELECT created_at FROM access_tokens WHERE id = ?`, completed.issued.ID).Scan(&createdAt); err != nil {
		t.Fatal(err)
	}
	if createdAt != postLockNow.Unix() || call != 2 {
		t.Fatalf("created_at=%d clock calls=%d", createdAt, call)
	}
}

func TestReplaceGrantsRejectsMismatchAtomicallyAndDeduplicates(t *testing.T) {
	fixture := newMachineServiceFixture(t)
	identity := createMachineIdentity(t, fixture, "grant-ci")
	original := EnvironmentGrant{ProjectID: fixture.allowedEnv.ProjectID, EnvironmentID: fixture.allowedEnv.ID}
	replaceMachineGrants(t, fixture, identity.ID, []EnvironmentGrant{original})

	err := fixture.service.ReplaceGrants(context.Background(), fixture.admin, identity.ID, []EnvironmentGrant{
		{ProjectID: fixture.deniedEnv.ProjectID, EnvironmentID: fixture.deniedEnv.ID},
		{ProjectID: fixture.allowedEnv.ProjectID, EnvironmentID: fixture.otherProjectEnvironment.ID},
	})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("mismatch error=%v", err)
	}
	detail, err := fixture.service.GetIdentity(context.Background(), fixture.admin, identity.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(detail.Grants) != 1 || detail.Grants[0] != original {
		t.Fatalf("grants changed after failed replacement: %+v", detail.Grants)
	}

	replaceMachineGrants(t, fixture, identity.ID, []EnvironmentGrant{original, original})
	detail, err = fixture.service.GetIdentity(context.Background(), fixture.admin, identity.ID)
	if err != nil || len(detail.Grants) != 1 {
		t.Fatalf("deduplicated grants=%+v err=%v", detail.Grants, err)
	}
	tooMany := make([]EnvironmentGrant, MaxGrantCount+1)
	for index := range tooMany {
		tooMany[index] = EnvironmentGrant{ProjectID: "project", EnvironmentID: fmt.Sprintf("environment-%d", index)}
	}
	if err := fixture.service.ReplaceGrants(context.Background(), fixture.admin, identity.ID, tooMany); !errors.Is(err, ErrInvalid) {
		t.Fatalf("too many grants error=%v", err)
	}
}

func TestIdentityManagementValidatesAndRereadsAdminState(t *testing.T) {
	fixture := newMachineServiceFixture(t)
	identity, err := fixture.service.CreateIdentity(context.Background(), fixture.admin, CreateIdentity{Name: " build-ci ", Description: " deploys ", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if identity.Name != "build-ci" || identity.Description != "deploys" || identity.CreatedAt.IsZero() || identity.UpdatedAt.IsZero() {
		t.Fatalf("identity=%+v", identity)
	}
	if _, err := fixture.service.CreateIdentity(context.Background(), fixture.admin, CreateIdentity{Name: "build-ci", Enabled: true}); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate identity error=%v", err)
	}
	if _, err := fixture.service.CreateIdentity(context.Background(), fixture.admin, CreateIdentity{Name: " ", Enabled: true}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid identity error=%v", err)
	}

	list, err := fixture.service.ListIdentities(context.Background(), fixture.admin)
	if err != nil || len(list) != 1 || list[0].ID != identity.ID {
		t.Fatalf("list=%+v err=%v", list, err)
	}
	updated, err := fixture.service.UpdateIdentity(context.Background(), fixture.admin, identity.ID, UpdateIdentityInput{Description: " updated ", Enabled: true})
	if err != nil || updated.Description != "updated" {
		t.Fatalf("updated=%+v err=%v", updated, err)
	}

	if _, err := fixture.service.ListIdentities(context.Background(), fixture.member); !errors.Is(err, ErrForbidden) {
		t.Fatalf("member list error=%v", err)
	}
	if _, err := fixture.store.DB().Exec(`UPDATE users SET role = 'member' WHERE id = ?`, fixture.admin.ID); err != nil {
		t.Fatal(err)
	}
	for name, operation := range map[string]func() error{
		"create": func() error {
			_, err := fixture.service.CreateIdentity(context.Background(), fixture.admin, CreateIdentity{Name: "denied", Enabled: true})
			return err
		},
		"list": func() error {
			_, err := fixture.service.ListIdentities(context.Background(), fixture.admin)
			return err
		},
		"get": func() error {
			_, err := fixture.service.GetIdentity(context.Background(), fixture.admin, identity.ID)
			return err
		},
		"update": func() error {
			_, err := fixture.service.UpdateIdentity(context.Background(), fixture.admin, identity.ID, UpdateIdentityInput{Enabled: true})
			return err
		},
		"grants": func() error {
			return fixture.service.ReplaceGrants(context.Background(), fixture.admin, identity.ID, nil)
		},
		"issue": func() error {
			_, err := fixture.service.IssueToken(context.Background(), fixture.admin, identity.ID, IssueToken{Name: "denied", ExpiresAt: machineTestNow.Add(time.Hour)})
			return err
		},
		"revoke": func() error {
			return fixture.service.RevokeToken(context.Background(), fixture.admin, identity.ID, "missing")
		},
	} {
		t.Run(name, func(t *testing.T) {
			if err := operation(); !errors.Is(err, ErrForbidden) {
				t.Fatalf("error=%v", err)
			}
		})
	}
	if _, err := fixture.store.DB().Exec(`UPDATE users SET role = 'admin', enabled = 0 WHERE id = ?`, fixture.admin.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.ListIdentities(context.Background(), fixture.admin); !errors.Is(err, ErrForbidden) {
		t.Fatalf("database-disabled admin list error=%v", err)
	}
	if _, err := fixture.service.CreateIdentity(context.Background(), fixture.admin, CreateIdentity{Name: "disabled-admin", Enabled: true}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("database-disabled admin create error=%v", err)
	}
}

func TestConcurrentTokenAuthenticationAndGrantReplacementIsRaceFree(t *testing.T) {
	fixture := newMachineServiceFixture(t)
	identity := createMachineIdentity(t, fixture, "concurrent-ci")
	replaceMachineGrants(t, fixture, identity.ID, []EnvironmentGrant{{ProjectID: fixture.allowedEnv.ProjectID, EnvironmentID: fixture.allowedEnv.ID}})
	issued := issueMachineToken(t, fixture, identity.ID, "primary", machineTestNow.Add(time.Hour))

	var group sync.WaitGroup
	group.Add(2)
	go func() {
		defer group.Done()
		for range 25 {
			_, err := fixture.service.AuthenticateForEnvironment(context.Background(), issued.Plaintext, fixture.allowedEnv.ID)
			if err != nil && !errors.Is(err, ErrScopeDenied) {
				t.Errorf("authenticate: %v", err)
			}
		}
	}()
	go func() {
		defer group.Done()
		for index := range 10 {
			grants := []EnvironmentGrant(nil)
			if index%2 == 0 {
				grants = []EnvironmentGrant{{ProjectID: fixture.allowedEnv.ProjectID, EnvironmentID: fixture.allowedEnv.ID}}
			}
			if err := fixture.service.ReplaceGrants(context.Background(), fixture.admin, identity.ID, grants); err != nil {
				t.Errorf("replace grants: %v", err)
			}
		}
	}()
	group.Wait()
}

func newMachineServiceFixture(t *testing.T) *machineServiceFixture {
	t.Helper()
	store, err := database.Open(filepath.Join(t.TempDir(), "machine-access.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	fixture := &machineServiceFixture{
		store:                   store,
		admin:                   auth.User{ID: "admin-id", Username: "admin", DisplayName: "Admin", Role: "admin", Enabled: true},
		member:                  auth.User{ID: "member-id", Username: "member", DisplayName: "Member", Role: "member", Enabled: true},
		disabled:                auth.User{ID: "disabled-id", Username: "disabled", DisplayName: "Disabled", Role: "admin", Enabled: false},
		allowedEnv:              testEnvironment{ID: "shop-production-id", ProjectID: "shop-project-id", ProjectSlug: "shop", Slug: "production"},
		deniedEnv:               testEnvironment{ID: "shop-staging-id", ProjectID: "shop-project-id", ProjectSlug: "shop", Slug: "staging"},
		otherProjectEnvironment: testEnvironment{ID: "other-production-id", ProjectID: "other-project-id", ProjectSlug: "other", Slug: "production"},
	}
	for _, user := range []auth.User{fixture.admin, fixture.member, fixture.disabled} {
		enabled := 0
		if user.Enabled {
			enabled = 1
		}
		if _, err := store.DB().Exec(`INSERT INTO users (id, username, display_name, password_hash, role, enabled, created_at, updated_at)
			VALUES (?, ?, ?, 'hash', ?, ?, 1, 1)`, user.ID, user.Username, user.DisplayName, user.Role, enabled); err != nil {
			t.Fatal(err)
		}
	}
	for _, project := range []struct{ id, slug string }{{fixture.allowedEnv.ProjectID, fixture.allowedEnv.ProjectSlug}, {fixture.otherProjectEnvironment.ProjectID, fixture.otherProjectEnvironment.ProjectSlug}} {
		if _, err := store.DB().Exec(`INSERT INTO projects (id, slug, name, created_by, created_at, updated_at) VALUES (?, ?, ?, ?, 1, 1)`, project.id, project.slug, project.slug, fixture.admin.ID); err != nil {
			t.Fatal(err)
		}
	}
	for _, environment := range []testEnvironment{fixture.allowedEnv, fixture.deniedEnv, fixture.otherProjectEnvironment} {
		if _, err := store.DB().Exec(`INSERT INTO environments (id, project_id, slug, name, created_at, updated_at) VALUES (?, ?, ?, ?, 1, 1)`, environment.ID, environment.ProjectID, environment.Slug, environment.Slug); err != nil {
			t.Fatal(err)
		}
	}
	fixture.service = NewService(store)
	fixture.service.now = func() time.Time { return machineTestNow }
	return fixture
}

func createMachineIdentity(t *testing.T, fixture *machineServiceFixture, name string) Identity {
	t.Helper()
	identity, err := fixture.service.CreateIdentity(context.Background(), fixture.admin, CreateIdentity{Name: name, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	return identity
}

func replaceMachineGrants(t *testing.T, fixture *machineServiceFixture, identityID string, grants []EnvironmentGrant) {
	t.Helper()
	if err := fixture.service.ReplaceGrants(context.Background(), fixture.admin, identityID, grants); err != nil {
		t.Fatal(err)
	}
}

func issueMachineToken(t *testing.T, fixture *machineServiceFixture, identityID, name string, expiresAt time.Time) IssuedToken {
	t.Helper()
	issued, err := fixture.service.IssueToken(context.Background(), fixture.admin, identityID, IssueToken{Name: name, ExpiresAt: expiresAt})
	if err != nil {
		t.Fatal(err)
	}
	return issued
}
