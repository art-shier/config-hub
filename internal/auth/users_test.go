package auth

import (
	"context"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"confighub.local/internal/database"
)

type storedUser struct {
	User
	PasswordHash string
}

func TestSyncUsersChangesPasswordAndRevokesSessions(t *testing.T) {
	ctx := context.Background()
	store := testStore(t)
	syncer := NewUserSyncer(store)
	first := UserFile{Users: []UserSpec{{Username: "admin", DisplayName: "Admin", Password: "first", Role: "admin", Enabled: true}}}
	if _, err := syncer.Sync(ctx, first); err != nil {
		t.Fatal(err)
	}
	user := loadUserByUsername(t, store, "admin")
	originalHash := user.PasswordHash
	insertSessionFixture(t, store, user.ID)

	second := UserFile{Users: []UserSpec{{Username: "admin", DisplayName: "Admin", Password: "second", Role: "admin", Enabled: true}}}
	result, err := syncer.Sync(ctx, second)
	if err != nil {
		t.Fatal(err)
	}
	updated := loadUserByUsername(t, store, "admin")
	if result.PasswordsChanged != 1 || result.Updated != 1 || countSessions(t, store, user.ID) != 0 {
		t.Fatalf("result=%+v sessions=%d", result, countSessions(t, store, user.ID))
	}
	if updated.PasswordHash == originalHash || !VerifyPassword(updated.PasswordHash, "second") || VerifyPassword(updated.PasswordHash, "first") {
		t.Fatal("password hash did not change safely")
	}
}

func TestSyncUsersCreatesAndIsIdempotent(t *testing.T) {
	store := testStore(t)
	syncer := NewUserSyncer(store)
	file := UserFile{Users: []UserSpec{{Username: "admin", DisplayName: " Admin ", Password: "secret", Role: "admin", Enabled: true}}}
	first, err := syncer.Sync(context.Background(), file)
	if err != nil {
		t.Fatal(err)
	}
	user := loadUserByUsername(t, store, "admin")
	if first.Created != 1 || first.Updated != 0 || user.DisplayName != "Admin" || !VerifyPassword(user.PasswordHash, "secret") {
		t.Fatalf("result=%+v user=%+v", first, user.User)
	}
	second, err := syncer.Sync(context.Background(), file)
	if err != nil {
		t.Fatal(err)
	}
	unchanged := loadUserByUsername(t, store, "admin")
	if second.Created != 0 || second.Updated != 0 || second.Disabled != 0 || second.PasswordsChanged != 0 || unchanged.PasswordHash != user.PasswordHash {
		t.Fatalf("result=%+v unchanged=%+v", second, unchanged.User)
	}
}

func TestSyncUsersDisablesExplicitAndMissingUsers(t *testing.T) {
	store := testStore(t)
	syncer := NewUserSyncer(store)
	initial := UserFile{Users: []UserSpec{
		{Username: "admin", DisplayName: "Admin", Password: "admin-secret", Role: "admin", Enabled: true},
		{Username: "member", DisplayName: "Member", Password: "member-secret", Role: "member", Enabled: true},
		{Username: "removed", DisplayName: "Removed", Password: "removed-secret", Role: "member", Enabled: true},
	}}
	if _, err := syncer.Sync(context.Background(), initial); err != nil {
		t.Fatal(err)
	}
	member := loadUserByUsername(t, store, "member")
	removed := loadUserByUsername(t, store, "removed")
	insertSessionFixture(t, store, member.ID)
	insertSessionFixture(t, store, removed.ID)

	disabled := UserFile{Users: []UserSpec{
		{Username: "admin", DisplayName: "Admin", Password: "admin-secret", Role: "admin", Enabled: true},
		{Username: "member", DisplayName: "Member", Password: "member-secret", Role: "member", Enabled: false},
	}}
	result, err := syncer.Sync(context.Background(), disabled)
	if err != nil {
		t.Fatal(err)
	}
	if result.Disabled != 2 || result.Updated != 2 || loadUserByUsername(t, store, "member").Enabled || loadUserByUsername(t, store, "removed").Enabled || countSessions(t, store, member.ID) != 0 || countSessions(t, store, removed.ID) != 0 {
		t.Fatalf("result=%+v", result)
	}
	if repeat, err := syncer.Sync(context.Background(), disabled); err != nil || repeat.Disabled != 0 || repeat.Updated != 0 {
		t.Fatalf("repeat=%+v err=%v", repeat, err)
	}

	result, err = syncer.Sync(context.Background(), UserFile{Users: []UserSpec{{Username: "admin", DisplayName: "Admin", Password: "admin-secret", Role: "admin", Enabled: true}}})
	if err != nil {
		t.Fatal(err)
	}
	if result.Disabled != 0 || result.Updated != 0 || loadUserByUsername(t, store, "member").Enabled {
		t.Fatalf("missing disabled user should be idempotent: %+v", result)
	}
}

func TestSyncUsersRejectsInvalidFileWithoutChangingDatabase(t *testing.T) {
	store := testStore(t)
	syncer := NewUserSyncer(store)
	valid := UserFile{Users: []UserSpec{{Username: "admin", DisplayName: "Admin", Password: "safe-secret", Role: "admin", Enabled: true}}}
	if _, err := syncer.Sync(context.Background(), valid); err != nil {
		t.Fatal(err)
	}
	admin := loadUserByUsername(t, store, "admin")
	insertSessionFixture(t, store, admin.ID)

	invalidFiles := []UserFile{
		{Users: []UserSpec{{Username: "not valid", DisplayName: "Admin", Password: "a", Role: "admin", Enabled: true}}},
		{Users: []UserSpec{{Username: "admin", DisplayName: "Admin", Password: "a", Role: "admin", Enabled: true}, {Username: "admin", DisplayName: "Other", Password: "b", Role: "member", Enabled: true}}},
		{Users: []UserSpec{{Username: "admin", DisplayName: "Admin", Password: "a", Role: "owner", Enabled: true}}},
		{Users: []UserSpec{{Username: "admin", DisplayName: "   ", Password: "a", Role: "admin", Enabled: true}}},
		{Users: []UserSpec{{Username: "admin", DisplayName: "Admin", Password: "", Role: "admin", Enabled: true}}},
		{Users: []UserSpec{{Username: "admin", DisplayName: "Admin", Password: "a", Role: "admin", Enabled: false}}},
	}
	for _, invalid := range invalidFiles {
		_, err := syncer.Sync(context.Background(), invalid)
		if !errors.Is(err, ErrInvalidUserFile) {
			t.Fatalf("expected invalid user file error, got %v", err)
		}
		current := loadUserByUsername(t, store, "admin")
		if current.PasswordHash != admin.PasswordHash || !current.Enabled || countSessions(t, store, admin.ID) != 1 {
			t.Fatal("invalid sync changed existing database state")
		}
	}
}

func TestSyncUsersProfileUpdateKeepsSessionsAndHash(t *testing.T) {
	store := testStore(t)
	syncer := NewUserSyncer(store)
	initial := UserFile{Users: []UserSpec{
		{Username: "admin", DisplayName: "Admin", Password: "admin-secret", Role: "admin", Enabled: true},
		{Username: "member", DisplayName: "Member", Password: "member-secret", Role: "member", Enabled: true},
	}}
	if _, err := syncer.Sync(context.Background(), initial); err != nil {
		t.Fatal(err)
	}
	member := loadUserByUsername(t, store, "member")
	insertSessionFixture(t, store, member.ID)
	changed := UserFile{Users: []UserSpec{
		{Username: "admin", DisplayName: "Admin", Password: "admin-secret", Role: "admin", Enabled: true},
		{Username: "member", DisplayName: "Renamed", Password: "member-secret", Role: "admin", Enabled: true},
	}}
	result, err := syncer.Sync(context.Background(), changed)
	if err != nil {
		t.Fatal(err)
	}
	updated := loadUserByUsername(t, store, "member")
	if result.Updated != 1 || result.PasswordsChanged != 0 || updated.PasswordHash != member.PasswordHash || countSessions(t, store, member.ID) != 1 {
		t.Fatalf("result=%+v updated=%+v", result, updated.User)
	}
}

func TestSyncUsersRollsBackWhenDatabaseWriteFails(t *testing.T) {
	store := testStore(t)
	syncer := NewUserSyncer(store)
	initial := UserFile{Users: []UserSpec{{Username: "admin", DisplayName: "Admin", Password: "first", Role: "admin", Enabled: true}}}
	if _, err := syncer.Sync(context.Background(), initial); err != nil {
		t.Fatal(err)
	}
	admin := loadUserByUsername(t, store, "admin")
	if _, err := store.DB().Exec(`CREATE TRIGGER fail_user_update BEFORE UPDATE ON users BEGIN SELECT RAISE(ABORT, 'forced failure'); END`); err != nil {
		t.Fatal(err)
	}
	file := UserFile{Users: []UserSpec{
		{Username: "new-admin", DisplayName: "New Admin", Password: "new-secret", Role: "admin", Enabled: true},
		{Username: "admin", DisplayName: "Changed", Password: "second", Role: "admin", Enabled: true},
	}}
	if _, err := syncer.Sync(context.Background(), file); err == nil {
		t.Fatal("expected database write failure")
	}
	if got := loadUserByUsername(t, store, "admin"); got.DisplayName != "Admin" || got.PasswordHash != admin.PasswordHash {
		t.Fatal("failed transaction changed existing user")
	}
	var count int
	if err := store.DB().QueryRow(`SELECT count(*) FROM users WHERE username = 'new-admin'`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("failed transaction created user: count=%d err=%v", count, err)
	}
}

func TestLoadAndSyncRejectsUnsafeYAML(t *testing.T) {
	store := testStore(t)
	syncer := NewUserSyncer(store)
	dir := t.TempDir()
	path := filepath.Join(dir, "users.yaml")
	if err := os.WriteFile(path, []byte("users:\n  - username: admin\n    display_name: Admin\n    password: safe-secret\n    role: admin\n    enabled: true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := syncer.LoadAndSync(context.Background(), path); err != nil {
		t.Fatal(err)
	}
	admin := loadUserByUsername(t, store, "admin")
	insertSessionFixture(t, store, admin.ID)
	for _, contents := range []string{
		"",
		"users:\n  - username: admin\n    display_name: Admin\n    password: secret\n    role: admin\n    enabled: true\n    extra: no\n",
		"users: [\n",
		"users:\n  - username: admin\n    display_name: Admin\n    password: secret\n    role: admin\n    enabled: true\n---\nusers: []\n",
	} {
		if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
		_, err := syncer.LoadAndSync(context.Background(), path)
		if !errors.Is(err, ErrInvalidUserFile) {
			t.Fatalf("expected invalid YAML error, got %v", err)
		}
		if strings.Contains(err.Error(), "secret") {
			t.Fatalf("error leaked password: %v", err)
		}
		current := loadUserByUsername(t, store, "admin")
		if current.PasswordHash != admin.PasswordHash || countSessions(t, store, admin.ID) != 1 {
			t.Fatal("invalid YAML changed existing database state")
		}
	}
}

func TestLoadAndSyncReadErrorsPreserveErrorChainAndDatabaseState(t *testing.T) {
	store := testStore(t)
	syncer := NewUserSyncer(store)
	admin := seedAdminWithSession(t, store, syncer)
	dir := t.TempDir()
	missing := filepath.Join(dir, "missing.yaml")

	for _, test := range []struct {
		name string
		path string
		want error
	}{
		{name: "missing", path: missing, want: os.ErrNotExist},
		{name: "directory", path: dir, want: syscall.EISDIR},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := syncer.LoadAndSync(context.Background(), test.path)
			if !errors.Is(err, ErrUserFileRead) || !errors.Is(err, test.want) {
				t.Fatalf("error=%v; want user read and %v", err, test.want)
			}
			assertAdminSessionUnchanged(t, store, admin)
		})
	}
}

func TestSyncUsersCanceledContextLeavesDatabaseUnchanged(t *testing.T) {
	store := testStore(t)
	syncer := NewUserSyncer(store)
	admin := seedAdminWithSession(t, store, syncer)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	changed := UserFile{Users: []UserSpec{{Username: "admin", DisplayName: "Changed", Password: "second", Role: "admin", Enabled: true}}}
	_, err := syncer.Sync(ctx, changed)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error=%v; want context canceled", err)
	}
	assertAdminSessionUnchanged(t, store, admin)
}

func testStore(t *testing.T) *database.Store {
	t.Helper()
	store, err := database.Open(filepath.Join(t.TempDir(), "config-hub.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func loadUserByUsername(t *testing.T, store *database.Store, username string) storedUser {
	t.Helper()
	var user storedUser
	var enabled int
	err := store.DB().QueryRow(`SELECT id, username, display_name, password_hash, role, enabled FROM users WHERE username = ?`, username).
		Scan(&user.ID, &user.Username, &user.DisplayName, &user.PasswordHash, &user.Role, &enabled)
	if err != nil {
		t.Fatal(err)
	}
	user.Enabled = enabled != 0
	return user
}

func insertSessionFixture(t *testing.T, store *database.Store, userID string) {
	t.Helper()
	if _, err := store.DB().Exec(`INSERT INTO sessions (id, user_id, token_hash, csrf_hash, expires_at, created_at) VALUES (?, ?, ?, ?, 1, 1)`, "session-"+userID, userID, []byte("token-"+userID), []byte("csrf-"+userID)); err != nil {
		t.Fatal(err)
	}
}

func countSessions(t *testing.T, store *database.Store, userID string) int {
	t.Helper()
	var count int
	if err := store.DB().QueryRow(`SELECT count(*) FROM sessions WHERE user_id = ?`, userID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func seedAdminWithSession(t *testing.T, store *database.Store, syncer *UserSyncer) storedUser {
	t.Helper()
	file := UserFile{Users: []UserSpec{{Username: "admin", DisplayName: "Admin", Password: "first", Role: "admin", Enabled: true}}}
	if _, err := syncer.Sync(context.Background(), file); err != nil {
		t.Fatal(err)
	}
	admin := loadUserByUsername(t, store, "admin")
	insertSessionFixture(t, store, admin.ID)
	return admin
}

func assertAdminSessionUnchanged(t *testing.T, store *database.Store, want storedUser) {
	t.Helper()
	got := loadUserByUsername(t, store, "admin")
	if got.PasswordHash != want.PasswordHash || got.Enabled != want.Enabled || got.DisplayName != want.DisplayName || got.Role != want.Role || countSessions(t, store, want.ID) != 1 {
		t.Fatalf("database state changed: got=%+v", got.User)
	}
}

func TestPasswordFormatAndMalformedHashes(t *testing.T) {
	first, err := HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	second, err := HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(first, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" || parts[2] != "v=19" || parts[3] != "m=65536,t=3,p=2" {
		t.Fatalf("unexpected PHC fields: %q", first)
	}
	if len(parts[4]) != 22 || len(parts[5]) != 43 {
		t.Fatalf("unexpected PHC base64 lengths: salt=%d hash=%d", len(parts[4]), len(parts[5]))
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil || len(salt) != 16 {
		t.Fatalf("salt decode: len=%d err=%v", len(salt), err)
	}
	hash, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil || len(hash) != 32 {
		t.Fatalf("hash decode: len=%d err=%v", len(hash), err)
	}
	secondParts := strings.Split(second, "$")
	secondSalt, err := base64.RawStdEncoding.DecodeString(secondParts[4])
	if err != nil || string(salt) == string(secondSalt) {
		t.Fatalf("salts must differ: err=%v", err)
	}
	for _, malformed := range []string{
		"", "argon2id$v=19$m=65536,t=3,p=2$abc$def", first + "$extra", "$argon2id$v=19$m=65536,t=3,p=2$abc",
		"$argon2i$v=19$m=65536,t=3,p=2$" + parts[4] + "$" + parts[5],
		"$argon2id$v=18$m=65536,t=3,p=2$" + parts[4] + "$" + parts[5],
		"$argon2id$v=19$m=1,t=3,p=2$" + parts[4] + "$" + parts[5],
		"$argon2id$v=19$m=65536,t=4,p=2$" + parts[4] + "$" + parts[5],
		"$argon2id$v=19$m=65536,t=3,p=1$" + parts[4] + "$" + parts[5],
		"$argon2id$v=19$m=65536,t=3,p=2$%%%$" + parts[5],
		"$argon2id$v=19$m=65536,t=3,p=2$" + parts[4] + "$%%%",
		"$argon2id$v=19$m=65536,t=3,p=2$A" + parts[4] + "$" + parts[5],
		"$argon2id$v=19$m=65536,t=3,p=2$" + parts[4] + "$A" + parts[5],
		"$argon2id$v=19$m=65536,t=3,p=2$" + strings.Repeat("A", 1<<20) + "$" + parts[5],
		strings.Repeat("$", 1<<20),
	} {
		if VerifyPassword(malformed, "correct horse battery staple") {
			t.Fatalf("malformed hash verified: %q", malformed)
		}
	}
	withNewline := append([]string(nil), parts...)
	withNewline[4] += "\n"
	if VerifyPassword(strings.Join(withNewline, "$"), "correct horse battery staple") {
		t.Fatal("hash with base64 newline verified")
	}
	withNonCanonicalSalt := append([]string(nil), parts...)
	const base64Alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"
	last := withNonCanonicalSalt[4][len(withNonCanonicalSalt[4])-1]
	withNonCanonicalSalt[4] = withNonCanonicalSalt[4][:len(withNonCanonicalSalt[4])-1] + string(base64Alphabet[strings.IndexByte(base64Alphabet, last)+1])
	if VerifyPassword(strings.Join(withNonCanonicalSalt, "$"), "correct horse battery staple") {
		t.Fatal("hash with non-canonical base64 salt verified")
	}
	withNonCanonicalHash := append([]string(nil), parts...)
	last = withNonCanonicalHash[5][len(withNonCanonicalHash[5])-1]
	withNonCanonicalHash[5] = withNonCanonicalHash[5][:len(withNonCanonicalHash[5])-1] + string(base64Alphabet[strings.IndexByte(base64Alphabet, last)+1])
	if VerifyPassword(strings.Join(withNonCanonicalHash, "$"), "correct horse battery staple") {
		t.Fatal("hash with non-canonical base64 hash verified")
	}
	if _, err := HashPassword(""); err == nil || VerifyPassword(first, "") {
		t.Fatal("empty passwords must be rejected")
	}
}

func TestParsePasswordHashRejectsOversizedInputWithoutAllocation(t *testing.T) {
	overlong := "$argon2id$v=19$m=65536,t=3,p=2$" + strings.Repeat("A", 1<<20) + "$" + strings.Repeat("A", 43)
	for _, encoded := range []string{overlong, strings.Repeat("$", 1<<20)} {
		if allocations := testing.AllocsPerRun(5, func() {
			if _, _, ok := parsePasswordHash(encoded); ok {
				t.Fatal("oversized hash parsed")
			}
		}); allocations != 0 {
			t.Fatalf("oversized parse allocated %.0f times", allocations)
		}
	}
}
