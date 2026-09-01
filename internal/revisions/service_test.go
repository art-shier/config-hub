package revisions

import (
	"context"
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"confighub.local/internal/auth"
	"confighub.local/internal/database"
)

func TestReplaceCreatesAtomicSnapshotAndRejectsStaleBase(t *testing.T) {
	fixture := newRevisionFixture(t)
	ctx := context.Background()

	first, err := fixture.service.Replace(ctx, fixture.editor, fixture.environmentID, ReplaceInput{
		BaseRevision: 0,
		Message:      "  initial snapshot  ",
		Entries: []Entry{
			{Key: " PORT ", Value: " 8080\n"},
			{Key: "DATABASE_URL", Value: "postgres://db\nnext", Service: " api "},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if first.Version != 1 || first.Message != "initial snapshot" || first.CreatedBy != fixture.editor.ID || first.CreatedByType != "user" {
		t.Fatalf("created snapshot metadata version=%d message_matches=%t actor_matches=%t actor_type=%q", first.Version, first.Message == "initial snapshot", first.CreatedBy == fixture.editor.ID, first.CreatedByType)
	}
	if len(first.Entries) != 2 || first.Entries[0].Key != "DATABASE_URL" || first.Entries[0].Service != "api" || first.Entries[0].Value != "postgres://db\nnext" || first.Entries[1].Key != "PORT" || first.Entries[1].Value != " 8080\n" {
		t.Fatalf("created snapshot entries did not retain expected metadata and values")
	}

	_, err = fixture.service.Replace(ctx, fixture.editor, fixture.environmentID, ReplaceInput{
		BaseRevision: 0,
		Entries:      []Entry{{Key: "PORT", Value: "9090"}},
	})
	if !errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("stale replace error=%v", err)
	}
	current, err := fixture.service.Current(ctx, fixture.editor, fixture.environmentID, "")
	if err != nil || current.Version != 1 || len(current.Entries) != 2 || current.Entries[1].Value != " 8080\n" {
		t.Fatalf("current snapshot version=%d entry_count=%d err=%v", current.Version, len(current.Entries), err)
	}

	filtered, err := fixture.service.Current(ctx, fixture.viewer, fixture.environmentID, "api")
	if err != nil || filtered.Version != 1 || len(filtered.Entries) != 1 || filtered.Entries[0].Key != "DATABASE_URL" {
		t.Fatalf("filtered snapshot version=%d entry_count=%d err=%v", filtered.Version, len(filtered.Entries), err)
	}
	none, err := fixture.service.Current(ctx, fixture.viewer, fixture.environmentID, " api ")
	if err != nil || len(none.Entries) != 0 {
		t.Fatalf("exact service filter entry_count=%d err=%v", len(none.Entries), err)
	}
}

func TestMachineOwnedRevisionUsesMachineActorAcrossDetailAndList(t *testing.T) {
	fixture := newRevisionFixture(t)
	const machineID = "m1"
	const sentinelValue = "task1-machine-read-sentinel"
	if _, err := fixture.store.DB().Exec(`INSERT INTO machine_identities (id, name, enabled, created_at, updated_at)
		VALUES (?, 'machine-one', 1, 1, 1)`, machineID); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.store.DB().Exec(`INSERT INTO revisions
		(id, environment_id, version, created_by_machine_identity_id, created_at)
		VALUES ('machine-revision', ?, 1, ?, 1)`, fixture.environmentID, machineID); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.store.DB().Exec(`INSERT INTO revision_entries (revision_id, key, value)
		VALUES ('machine-revision', 'SENTINEL', ?)`, sentinelValue); err != nil {
		t.Fatal(err)
	}

	detail, err := fixture.service.Get(context.Background(), fixture.viewer, fixture.environmentID, 1)
	if err != nil {
		t.Fatalf("get machine revision: %v", err)
	}
	if detail.CreatedBy != machineID || detail.CreatedByType != "machine" {
		t.Fatalf("detail actor_id=%q actor_type=%q", detail.CreatedBy, detail.CreatedByType)
	}
	list, err := fixture.service.List(context.Background(), fixture.viewer, fixture.environmentID)
	if err != nil {
		t.Fatalf("list machine revision: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("machine revision list count=%d", len(list))
	}
	if list[0].CreatedBy != machineID || list[0].CreatedByType != "machine" {
		t.Fatalf("list actor_id=%q actor_type=%q", list[0].CreatedBy, list[0].CreatedByType)
	}
}

func TestValidateStoredEntriesRejectsNonCanonicalOrUnsafeSnapshots(t *testing.T) {
	if err := ValidateStoredEntries([]Entry{{Key: "DATABASE_URL", Value: "postgres://db", Service: "api"}, {Key: "PORT", Value: "8080"}}); err != nil {
		t.Fatalf("valid stored entries error=%v", err)
	}

	tooMany := make([]Entry, MaxEntryCount+1)
	for index := range tooMany {
		tooMany[index] = Entry{Key: fmt.Sprintf("KEY_%d", index)}
	}
	for _, test := range []struct {
		name    string
		entries []Entry
	}{
		{name: "invalid key", entries: []Entry{{Key: "NOT-A-KEY", Value: "secret"}}},
		{name: "duplicate key", entries: []Entry{{Key: "DUPLICATE"}, {Key: "DUPLICATE"}}},
		{name: "invalid UTF-8 value", entries: []Entry{{Key: "SECRET", Value: string([]byte{0xff})}}},
		{name: "non-canonical service", entries: []Entry{{Key: "SECRET", Value: "hidden", Service: " api "}}},
		{name: "oversized value", entries: []Entry{{Key: "SECRET", Value: strings.Repeat("x", MaxValueBytes+1)}}},
		{name: "aggregate too large", entries: []Entry{
			{Key: "ONE", Value: strings.Repeat("1", MaxSnapshotBytes/2)},
			{Key: "TWO", Value: strings.Repeat("2", MaxSnapshotBytes/2)},
		}},
		{name: "too many entries", entries: tooMany},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := ValidateStoredEntries(test.entries)
			if !errors.Is(err, ErrDataIntegrity) {
				t.Fatalf("error=%v", err)
			}
			if strings.Contains(fmt.Sprint(err), "secret") || strings.Contains(fmt.Sprint(err), "hidden") {
				t.Fatalf("integrity error leaked entry content: %v", err)
			}
		})
	}
}

func TestCurrentReturnsEmptySnapshotBeforeFirstSave(t *testing.T) {
	fixture := newRevisionFixture(t)
	revision, err := fixture.service.Current(context.Background(), fixture.viewer, fixture.environmentID, "")
	if err != nil {
		t.Fatal(err)
	}
	if revision.Version != 0 || revision.EnvironmentID != fixture.environmentID || revision.Entries == nil || len(revision.Entries) != 0 {
		t.Fatalf("revision=%+v", revision)
	}
}

func TestCurrentFailsClosedWhenPointerIsMissingOrBelongsToAnotherEnvironment(t *testing.T) {
	tests := []struct {
		name       string
		pointer    func(*testing.T, *revisionFixture) string
		secretText string
	}{
		{
			name: "cross-environment pointer",
			pointer: func(t *testing.T, fixture *revisionFixture) string {
				t.Helper()
				revision, err := fixture.service.Replace(context.Background(), fixture.admin, fixture.hiddenEnvironmentID, ReplaceInput{
					Entries: []Entry{{Key: "HIDDEN_SECRET", Value: "cross-project-secret"}},
				})
				if err != nil {
					t.Fatal(err)
				}
				return revision.ID
			},
			secretText: "cross-project-secret",
		},
		{name: "missing pointer", pointer: func(*testing.T, *revisionFixture) string { return "missing-revision-id" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newRevisionFixture(t)
			pointer := test.pointer(t, fixture)
			if _, err := fixture.store.DB().Exec(`DROP TRIGGER environments_current_revision_update`); err != nil {
				t.Fatal(err)
			}
			if _, err := fixture.store.DB().Exec(`UPDATE environments SET current_revision_id = ? WHERE id = ?`, pointer, fixture.environmentID); err != nil {
				t.Fatal(err)
			}
			revision, err := fixture.service.Current(context.Background(), fixture.viewer, fixture.environmentID, "")
			if !errors.Is(err, ErrDataIntegrity) {
				t.Fatalf("revision=%+v error=%v", revision, err)
			}
			if test.secretText != "" && strings.Contains(err.Error(), test.secretText) {
				t.Fatalf("data integrity error leaked secret: %v", err)
			}
		})
	}
}

func TestCurrentAndReplaceFailClosedWhenCurrentVersionIsNotPositive(t *testing.T) {
	for _, version := range []int64{0, -1} {
		t.Run(fmt.Sprintf("version %d", version), func(t *testing.T) {
			fixture := newRevisionFixture(t)
			if _, err := fixture.store.DB().Exec(`INSERT INTO revisions
				(id, environment_id, version, message, created_by, created_at)
				VALUES ('invalid-version', ?, ?, '', ?, 1)`, fixture.environmentID, version, fixture.editor.ID); err != nil {
				t.Fatal(err)
			}
			if _, err := fixture.store.DB().Exec(`INSERT INTO revision_entries (revision_id, key, value)
				VALUES ('invalid-version', 'VALUE', 'original')`); err != nil {
				t.Fatal(err)
			}
			if _, err := fixture.store.DB().Exec(`UPDATE environments SET current_revision_id = 'invalid-version' WHERE id = ?`, fixture.environmentID); err != nil {
				t.Fatal(err)
			}

			if _, err := fixture.service.Current(context.Background(), fixture.viewer, fixture.environmentID, ""); !errors.Is(err, ErrDataIntegrity) {
				t.Fatalf("current error=%v", err)
			}
			_, err := fixture.service.Replace(context.Background(), fixture.editor, fixture.environmentID, ReplaceInput{
				BaseRevision: 0,
				Entries:      []Entry{{Key: "VALUE", Value: "must not save"}},
			})
			if !errors.Is(err, ErrDataIntegrity) {
				t.Fatalf("replace error=%v", err)
			}
			var revisions int
			var currentID string
			if err := fixture.store.DB().QueryRow(`SELECT COUNT(r.id), e.current_revision_id
				FROM environments e JOIN revisions r ON r.environment_id = e.id
				WHERE e.id = ? GROUP BY e.id`, fixture.environmentID).Scan(&revisions, &currentID); err != nil {
				t.Fatal(err)
			}
			if revisions != 1 || currentID != "invalid-version" {
				t.Fatalf("revisions=%d current=%q", revisions, currentID)
			}
		})
	}
}

func TestReplaceRejectsInvalidDuplicateAndOversizedEntries(t *testing.T) {
	fixture := newRevisionFixture(t)
	invalidUTF8 := string([]byte{0xff})
	tests := []struct {
		name  string
		input ReplaceInput
		field string
	}{
		{name: "invalid key", input: ReplaceInput{Entries: []Entry{{Key: "BAD-KEY", Value: "value"}}}, field: "entries[0].key"},
		{name: "duplicate normalized key", input: ReplaceInput{Entries: []Entry{{Key: "PORT", Value: "1"}, {Key: " PORT ", Value: "2", Service: "api"}}}, field: "entries[1].key"},
		{name: "invalid value UTF-8", input: ReplaceInput{Entries: []Entry{{Key: "PORT", Value: invalidUTF8}}}, field: "entries[0].value"},
		{name: "invalid service UTF-8", input: ReplaceInput{Entries: []Entry{{Key: "PORT", Value: "1", Service: invalidUTF8}}}, field: "entries[0].service"},
		{name: "message too long", input: ReplaceInput{Message: strings.Repeat("m", MaxMessageBytes+1)}, field: "message"},
		{name: "service too long", input: ReplaceInput{Entries: []Entry{{Key: "PORT", Value: "1", Service: strings.Repeat("s", MaxServiceBytes+1)}}}, field: "entries[0].service"},
		{name: "value too long", input: ReplaceInput{Entries: []Entry{{Key: "PORT", Value: strings.Repeat("v", MaxValueBytes+1)}}}, field: "entries[0].value"},
		{name: "aggregate too large", input: ReplaceInput{Entries: []Entry{{Key: "ONE", Value: strings.Repeat("1", MaxSnapshotBytes/2)}, {Key: "TWO", Value: strings.Repeat("2", MaxSnapshotBytes/2+1)}}}, field: "entries"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := fixture.service.Replace(context.Background(), fixture.editor, fixture.environmentID, test.input)
			var validation *ValidationError
			if !errors.As(err, &validation) || validation.Fields[test.field] == "" {
				t.Fatalf("error=%v", err)
			}
		})
	}

	entries := make([]Entry, MaxEntryCount+1)
	for index := range entries {
		entries[index] = Entry{Key: "KEY_" + strings.Repeat("X", index%10), Value: "value"}
	}
	_, err := fixture.service.Replace(context.Background(), fixture.editor, fixture.environmentID, ReplaceInput{Entries: entries})
	var validation *ValidationError
	if !errors.As(err, &validation) || validation.Fields["entries"] == "" {
		t.Fatalf("entry count error=%v", err)
	}
}

func TestSnapshotBudgetRejectsIntegerSizedComponentWithoutOverflow(t *testing.T) {
	remaining := int64(MaxSnapshotBytes)
	after, ok := consumeSnapshotBudget(remaining, math.MaxInt)
	if ok || after != remaining {
		t.Fatalf("remaining=%d ok=%v", after, ok)
	}
}

func TestReplaceFailsClosedWhenCurrentVersionCannotIncrement(t *testing.T) {
	fixture := newRevisionFixture(t)
	if _, err := fixture.store.DB().Exec(`INSERT INTO revisions
		(id, environment_id, version, message, created_by, created_at)
		VALUES ('max-revision', ?, ?, '', ?, 1)`, fixture.environmentID, int64(math.MaxInt64), fixture.editor.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.store.DB().Exec(`INSERT INTO revision_entries (revision_id, key, value) VALUES ('max-revision', 'VALUE', 'original')`); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.store.DB().Exec(`UPDATE environments SET current_revision_id = 'max-revision' WHERE id = ?`, fixture.environmentID); err != nil {
		t.Fatal(err)
	}

	_, err := fixture.service.Replace(context.Background(), fixture.editor, fixture.environmentID, ReplaceInput{
		BaseRevision: math.MaxInt64,
		Entries:      []Entry{{Key: "VALUE", Value: "must not save"}},
	})
	if !errors.Is(err, ErrDataIntegrity) {
		t.Fatalf("replace error=%v", err)
	}
	current, err := fixture.service.Current(context.Background(), fixture.viewer, fixture.environmentID, "")
	if err != nil || current.ID != "max-revision" || current.Version != math.MaxInt64 || len(current.Entries) != 1 || current.Entries[0].Value != "original" {
		t.Fatalf("current=%+v error=%v", current, err)
	}
	var revisions, negativeVersions int
	if err := fixture.store.DB().QueryRow(`SELECT COUNT(*), COALESCE(SUM(CASE WHEN version < 0 THEN 1 ELSE 0 END), 0)
		FROM revisions WHERE environment_id = ?`, fixture.environmentID).Scan(&revisions, &negativeVersions); err != nil {
		t.Fatal(err)
	}
	if revisions != 1 || negativeVersions != 0 {
		t.Fatalf("revisions=%d negative=%d", revisions, negativeVersions)
	}
}

func TestRollbackFailsClosedWhenCurrentVersionCannotIncrement(t *testing.T) {
	fixture := newRevisionFixture(t)
	if _, err := fixture.store.DB().Exec(`INSERT INTO revisions
		(id, environment_id, version, message, created_by, created_at)
		VALUES ('target-revision', ?, 1, '', ?, 1),
		       ('max-revision', ?, ?, '', ?, 2)`,
		fixture.environmentID, fixture.editor.ID,
		fixture.environmentID, int64(math.MaxInt64), fixture.editor.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.store.DB().Exec(`INSERT INTO revision_entries (revision_id, key, value)
		VALUES ('target-revision', 'VALUE', 'target'), ('max-revision', 'VALUE', 'original')`); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.store.DB().Exec(`UPDATE environments SET current_revision_id = 'max-revision' WHERE id = ?`, fixture.environmentID); err != nil {
		t.Fatal(err)
	}

	_, err := fixture.service.Rollback(context.Background(), fixture.editor, fixture.environmentID, 1, "rollback")
	if !errors.Is(err, ErrDataIntegrity) {
		t.Fatalf("rollback error=%v", err)
	}
	var revisions, negativeVersions int
	var currentID string
	if err := fixture.store.DB().QueryRow(`SELECT COUNT(r.id),
			COALESCE(SUM(CASE WHEN r.version < 0 THEN 1 ELSE 0 END), 0), e.current_revision_id
		FROM environments e JOIN revisions r ON r.environment_id = e.id
		WHERE e.id = ? GROUP BY e.id`, fixture.environmentID).Scan(&revisions, &negativeVersions, &currentID); err != nil {
		t.Fatal(err)
	}
	if revisions != 2 || negativeVersions != 0 || currentID != "max-revision" {
		t.Fatalf("revisions=%d negative=%d current=%q", revisions, negativeVersions, currentID)
	}
}

func TestRevisionPermissionsAndUnauthorizedIsolation(t *testing.T) {
	fixture := newRevisionFixture(t)
	ctx := context.Background()

	if _, err := fixture.service.Replace(ctx, fixture.viewer, fixture.environmentID, ReplaceInput{}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("viewer replace error=%v", err)
	}
	if _, err := fixture.service.Rollback(ctx, fixture.viewer, fixture.environmentID, 1, "denied"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("viewer rollback error=%v", err)
	}
	for _, environmentID := range []string{fixture.hiddenEnvironmentID, "missing-environment"} {
		if _, err := fixture.service.Current(ctx, fixture.editor, environmentID, ""); !errors.Is(err, ErrForbidden) {
			t.Fatalf("member environment=%q error=%v", environmentID, err)
		}
	}
	if _, err := fixture.service.Current(ctx, fixture.admin, "missing-environment", ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("admin missing error=%v", err)
	}
	if _, err := fixture.service.Current(ctx, fixture.disabled, fixture.environmentID, ""); !errors.Is(err, ErrForbidden) {
		t.Fatalf("disabled input actor error=%v", err)
	}
	if _, err := fixture.service.Replace(ctx, fixture.disabled, fixture.environmentID, ReplaceInput{}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("disabled replace error=%v", err)
	}
	if _, err := fixture.service.List(ctx, fixture.disabled, fixture.environmentID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("disabled list error=%v", err)
	}
	if _, err := fixture.service.Get(ctx, fixture.disabled, fixture.environmentID, 1); !errors.Is(err, ErrForbidden) {
		t.Fatalf("disabled get error=%v", err)
	}
	if _, err := fixture.service.Diff(ctx, fixture.disabled, fixture.environmentID, 1); !errors.Is(err, ErrForbidden) {
		t.Fatalf("disabled diff error=%v", err)
	}
	if _, err := fixture.service.Rollback(ctx, fixture.disabled, fixture.environmentID, 1, "denied"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("disabled rollback error=%v", err)
	}
	if _, err := fixture.service.Replace(ctx, fixture.admin, fixture.hiddenEnvironmentID, ReplaceInput{}); err != nil {
		t.Fatalf("admin replace error=%v", err)
	}
	if _, err := fixture.store.DB().Exec(`UPDATE users SET enabled = 0 WHERE id = ?`, fixture.editor.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.Current(ctx, fixture.editor, fixture.environmentID, ""); !errors.Is(err, ErrForbidden) {
		t.Fatalf("database-disabled actor error=%v", err)
	}
}

func TestDiffUsesHistoricalBeforeAndCurrentAfterWithFullValues(t *testing.T) {
	fixture := newRevisionFixture(t)
	ctx := context.Background()
	first, err := fixture.service.Replace(ctx, fixture.editor, fixture.environmentID, ReplaceInput{Entries: []Entry{
		{Key: "A", Value: "before", Service: "api"},
		{Key: "DELETE_ME", Value: "deleted value", Service: "worker"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	second, err := fixture.service.Replace(ctx, fixture.editor, fixture.environmentID, ReplaceInput{BaseRevision: first.Version, Entries: []Entry{
		{Key: "A", Value: "after", Service: "worker"},
		{Key: "NEW_KEY", Value: "new value", Service: "api"},
	}})
	if err != nil {
		t.Fatal(err)
	}

	changes, err := fixture.service.Diff(ctx, fixture.viewer, fixture.environmentID, first.Version)
	if err != nil {
		t.Fatal(err)
	}
	want := []Change{
		{Key: "A", Kind: "changed", Before: "before", After: "after", BeforeService: "api", AfterService: "worker"},
		{Key: "DELETE_ME", Kind: "deleted", Before: "deleted value", BeforeService: "worker"},
		{Key: "NEW_KEY", Kind: "added", After: "new value", AfterService: "api"},
	}
	if len(changes) != len(want) {
		t.Fatalf("changes=%+v", changes)
	}
	for index := range want {
		if changes[index] != want[index] {
			t.Fatalf("change[%d]=%+v want=%+v", index, changes[index], want[index])
		}
	}
	empty, err := fixture.service.Diff(ctx, fixture.viewer, fixture.environmentID, second.Version)
	if err != nil || empty == nil || len(empty) != 0 {
		t.Fatalf("current diff=%+v err=%v", empty, err)
	}
	if _, err := fixture.service.Diff(ctx, fixture.viewer, fixture.environmentID, 99); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing version error=%v", err)
	}
}

func TestRollbackCopiesHistoricalSnapshotAsNextVersion(t *testing.T) {
	fixture := newRevisionFixture(t)
	ctx := context.Background()
	first, err := fixture.service.Replace(ctx, fixture.editor, fixture.environmentID, ReplaceInput{Entries: []Entry{{Key: "VALUE", Value: " first\n", Service: " api "}}})
	if err != nil {
		t.Fatal(err)
	}
	second, err := fixture.service.Replace(ctx, fixture.editor, fixture.environmentID, ReplaceInput{BaseRevision: first.Version, Entries: []Entry{{Key: "VALUE", Value: "second"}}})
	if err != nil {
		t.Fatal(err)
	}
	rolledBack, err := fixture.service.Rollback(ctx, fixture.editor, fixture.environmentID, first.Version, "  restore first  ")
	if err != nil {
		t.Fatal(err)
	}
	if rolledBack.Version != second.Version+1 || rolledBack.Message != "restore first" || len(rolledBack.Entries) != 1 || rolledBack.Entries[0].Value != " first\n" || rolledBack.Entries[0].Service != "api" {
		t.Fatalf("rolledBack=%+v", rolledBack)
	}
	current, err := fixture.service.Current(ctx, fixture.viewer, fixture.environmentID, "")
	if err != nil || current.ID != rolledBack.ID || current.Entries[0].Value != first.Entries[0].Value {
		t.Fatalf("current=%+v err=%v", current, err)
	}

	list, err := fixture.service.List(ctx, fixture.viewer, fixture.environmentID)
	if err != nil || len(list) != 3 || list[0].Version != 3 || list[2].Version != 1 {
		t.Fatalf("list=%+v err=%v", list, err)
	}
	detail, err := fixture.service.Get(ctx, fixture.viewer, fixture.environmentID, 1)
	if err != nil || detail.ID != first.ID || detail.Entries[0].Value != " first\n" {
		t.Fatalf("detail=%+v err=%v", detail, err)
	}
}

func TestReplaceFailureRollsBackRevisionAndCurrentPointer(t *testing.T) {
	fixture := newRevisionFixture(t)
	ctx := context.Background()
	first, err := fixture.service.Replace(ctx, fixture.editor, fixture.environmentID, ReplaceInput{Entries: []Entry{{Key: "GOOD", Value: "one"}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.store.DB().Exec(`CREATE TRIGGER inject_revision_entry_failure BEFORE INSERT ON revision_entries
		WHEN NEW.key = 'FAIL' BEGIN SELECT RAISE(ABORT, 'injected entry failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.Replace(ctx, fixture.editor, fixture.environmentID, ReplaceInput{BaseRevision: first.Version, Entries: []Entry{{Key: "FAIL", Value: "secret must not leak"}}}); err == nil {
		t.Fatal("replace unexpectedly succeeded")
	}
	current, err := fixture.service.Current(ctx, fixture.editor, fixture.environmentID, "")
	if err != nil || current.ID != first.ID || current.Version != 1 || current.Entries[0].Key != "GOOD" {
		t.Fatalf("current=%+v err=%v", current, err)
	}
	var revisions, unsealed int
	if err := fixture.store.DB().QueryRow(`SELECT COUNT(*), COALESCE(SUM(CASE WHEN sealed = 0 THEN 1 ELSE 0 END), 0) FROM revisions WHERE environment_id = ?`, fixture.environmentID).Scan(&revisions, &unsealed); err != nil {
		t.Fatal(err)
	}
	if revisions != 1 || unsealed != 0 {
		t.Fatalf("revisions=%d unsealed=%d", revisions, unsealed)
	}
}

func TestRollbackFailureLeavesCurrentAndHistoryUnchanged(t *testing.T) {
	fixture := newRevisionFixture(t)
	ctx := context.Background()
	target, err := fixture.service.Replace(ctx, fixture.editor, fixture.environmentID, ReplaceInput{Entries: []Entry{
		{Key: "A_FIRST", Value: "target first"},
		{Key: "FAIL_COPY", Value: "target secret value", Service: "api"},
		{Key: "Z_LAST", Value: "target last"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	currentBefore, err := fixture.service.Replace(ctx, fixture.editor, fixture.environmentID, ReplaceInput{
		BaseRevision: target.Version,
		Entries:      []Entry{{Key: "CURRENT", Value: "current value", Service: "worker"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.store.DB().Exec(`CREATE TRIGGER inject_rollback_entry_failure BEFORE INSERT ON revision_entries
		WHEN NEW.key = 'FAIL_COPY'
		 AND (SELECT version FROM revisions WHERE id = NEW.revision_id) = 3
		BEGIN SELECT RAISE(ABORT, 'injected rollback copy failure'); END`); err != nil {
		t.Fatal(err)
	}

	_, err = fixture.service.Rollback(ctx, fixture.editor, fixture.environmentID, target.Version, "restore target")
	if err == nil || errors.Is(err, ErrForbidden) || errors.Is(err, ErrNotFound) || errors.Is(err, ErrRevisionConflict) || strings.Contains(err.Error(), "target secret value") {
		t.Fatalf("rollback error=%v", err)
	}
	currentAfter, err := fixture.service.Current(ctx, fixture.viewer, fixture.environmentID, "")
	if err != nil {
		t.Fatal(err)
	}
	if currentAfter.ID != currentBefore.ID || currentAfter.Version != currentBefore.Version || len(currentAfter.Entries) != 1 || currentAfter.Entries[0] != currentBefore.Entries[0] {
		t.Fatalf("current before=%+v after=%+v", currentBefore, currentAfter)
	}
	var revisions, unsealed int
	if err := fixture.store.DB().QueryRow(`SELECT COUNT(*), COALESCE(SUM(CASE WHEN sealed = 0 THEN 1 ELSE 0 END), 0)
		FROM revisions WHERE environment_id = ?`, fixture.environmentID).Scan(&revisions, &unsealed); err != nil {
		t.Fatal(err)
	}
	if revisions != 2 || unsealed != 0 {
		t.Fatalf("revisions=%d unsealed=%d", revisions, unsealed)
	}
	var copiedEntries int
	if err := fixture.store.DB().QueryRow(`SELECT COUNT(*) FROM revision_entries re
		JOIN revisions r ON r.id = re.revision_id WHERE r.environment_id = ? AND r.version = 3`, fixture.environmentID).Scan(&copiedEntries); err != nil {
		t.Fatal(err)
	}
	if copiedEntries != 0 {
		t.Fatalf("rollback left copied entries=%d", copiedEntries)
	}
}

func TestConcurrentEditorsUsingSameBaseAllowExactlyOneSave(t *testing.T) {
	fixture := newRevisionFixture(t)
	ctx := context.Background()
	first, err := fixture.service.Replace(ctx, fixture.editor, fixture.environmentID, ReplaceInput{Entries: []Entry{{Key: "VALUE", Value: "initial"}}})
	if err != nil {
		t.Fatal(err)
	}

	start := make(chan struct{})
	errorsByWriter := make([]error, 2)
	actors := []auth.User{fixture.editor, fixture.editorTwo}
	var wait sync.WaitGroup
	for index := range errorsByWriter {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			<-start
			_, errorsByWriter[index] = fixture.service.Replace(ctx, actors[index], fixture.environmentID, ReplaceInput{
				BaseRevision: first.Version,
				Entries:      []Entry{{Key: "VALUE", Value: strings.Repeat("x", index+1)}},
			})
		}(index)
	}
	close(start)
	wait.Wait()
	succeeded, conflicted := 0, 0
	for _, err := range errorsByWriter {
		switch {
		case err == nil:
			succeeded++
		case errors.Is(err, ErrRevisionConflict):
			conflicted++
		default:
			t.Fatalf("unexpected concurrent error=%v", err)
		}
	}
	if succeeded != 1 || conflicted != 1 {
		t.Fatalf("success=%d conflict=%d errors=%v", succeeded, conflicted, errorsByWriter)
	}
}

func TestSavedRevisionEntriesRemainSealed(t *testing.T) {
	fixture := newRevisionFixture(t)
	revision, err := fixture.service.Replace(context.Background(), fixture.editor, fixture.environmentID, ReplaceInput{Entries: []Entry{{Key: "SEALED", Value: "original"}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.store.DB().Exec(`UPDATE revision_entries SET value = 'changed' WHERE revision_id = ? AND key = 'SEALED'`, revision.ID); err == nil {
		t.Fatal("sealed revision entry was mutable")
	}
	if _, err := fixture.store.DB().Exec(`DELETE FROM revision_entries WHERE revision_id = ? AND key = 'SEALED'`, revision.ID); err == nil {
		t.Fatal("sealed revision entry was deletable")
	}
}

type revisionFixture struct {
	service                                    *Service
	store                                      *database.Store
	admin, editor, editorTwo, viewer, disabled auth.User
	environmentID, hiddenEnvironmentID         string
}

func newRevisionFixture(t *testing.T) *revisionFixture {
	t.Helper()
	store, err := database.Open(filepath.Join(t.TempDir(), "database", "revisions.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	fixture := &revisionFixture{
		store:         store,
		admin:         auth.User{ID: "admin-id", Username: "admin", DisplayName: "Admin", Role: "admin", Enabled: true},
		editor:        auth.User{ID: "editor-id", Username: "editor", DisplayName: "Editor", Role: "member", Enabled: true},
		editorTwo:     auth.User{ID: "editor-two-id", Username: "editor-two", DisplayName: "Editor Two", Role: "member", Enabled: true},
		viewer:        auth.User{ID: "viewer-id", Username: "viewer", DisplayName: "Viewer", Role: "member", Enabled: true},
		disabled:      auth.User{ID: "disabled-id", Username: "disabled", DisplayName: "Disabled", Role: "member", Enabled: false},
		environmentID: "environment-id", hiddenEnvironmentID: "hidden-environment-id",
	}
	for _, user := range []auth.User{fixture.admin, fixture.editor, fixture.editorTwo, fixture.viewer, fixture.disabled} {
		enabled := 0
		if user.Enabled {
			enabled = 1
		}
		if _, err := store.DB().Exec(`INSERT INTO users (id, username, display_name, password_hash, role, enabled, created_at, updated_at)
			VALUES (?, ?, ?, 'hash', ?, ?, 1, 1)`, user.ID, user.Username, user.DisplayName, user.Role, enabled); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.DB().Exec(`INSERT INTO projects (id, slug, name, description, created_by, created_at, updated_at)
		VALUES ('project-id', 'visible', 'Visible', '', ?, 1, 1), ('hidden-project-id', 'hidden', 'Hidden', '', ?, 1, 1)`, fixture.admin.ID, fixture.admin.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB().Exec(`INSERT INTO environments (id, project_id, slug, name, created_at, updated_at)
		VALUES (?, 'project-id', 'production', 'Production', 1, 1), (?, 'hidden-project-id', 'production', 'Production', 1, 1)`, fixture.environmentID, fixture.hiddenEnvironmentID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB().Exec(`INSERT INTO project_members (project_id, user_id, permission)
		VALUES ('project-id', ?, 'editor'), ('project-id', ?, 'editor'), ('project-id', ?, 'viewer')`, fixture.editor.ID, fixture.editorTwo.ID, fixture.viewer.ID); err != nil {
		t.Fatal(err)
	}
	fixture.service = NewService(store)
	return fixture
}
