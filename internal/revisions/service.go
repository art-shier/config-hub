package revisions

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"confighub.local/internal/auth"
	"confighub.local/internal/database"
	"confighub.local/internal/permissions"
)

const (
	MaxEntryCount    = 4096
	MaxKeyBytes      = 128
	MaxServiceBytes  = 128
	MaxValueBytes    = 1 << 20
	MaxMessageBytes  = 1024
	MaxSnapshotBytes = 1 << 20
)

var (
	ErrForbidden        = errors.New("revision access forbidden")
	ErrNotFound         = errors.New("revision resource not found")
	ErrRevisionConflict = errors.New("revision conflict")
	ErrInvalid          = errors.New("invalid revision input")
	ErrDataIntegrity    = errors.New("revision data integrity failure")

	keyPattern  = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	slugPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)
)

type Entry struct {
	Key     string `json:"key"`
	Value   string `json:"value"`
	Service string `json:"service"`
}

type Revision struct {
	ID            string    `json:"id"`
	EnvironmentID string    `json:"environment_id"`
	Message       string    `json:"message"`
	CreatedBy     string    `json:"created_by"`
	CreatedByType string    `json:"created_by_type"`
	Version       int64     `json:"version"`
	CreatedAt     time.Time `json:"created_at"`
	Entries       []Entry   `json:"entries"`
}

type RevisionSummary struct {
	ID            string    `json:"id"`
	EnvironmentID string    `json:"environment_id"`
	Message       string    `json:"message"`
	CreatedBy     string    `json:"created_by"`
	CreatedByType string    `json:"created_by_type"`
	Version       int64     `json:"version"`
	CreatedAt     time.Time `json:"created_at"`
}

type ReplaceInput struct {
	BaseRevision int64
	Message      string
	Entries      []Entry
}

type MachineWriteActor struct {
	IdentityID    string
	EnvironmentID string
}

type MachineWriteAuthorizer interface {
	AuthorizeMachineWrite(context.Context, *sql.Tx, string, string, string) (MachineWriteActor, error)
}

type ServiceOption func(*Service)

func WithMachineWriteAuthorizer(authorizer MachineWriteAuthorizer) ServiceOption {
	return func(service *Service) { service.machineWriteAuthorizer = authorizer }
}

type MutationOperation struct {
	Type    string  `json:"type"`
	Key     string  `json:"key"`
	Value   *string `json:"value,omitempty"`
	Service *string `json:"service,omitempty"`
}

type MachineMutationInput struct {
	BaseRevision int64             `json:"base_revision"`
	Message      string            `json:"message"`
	Operation    MutationOperation `json:"operation"`
}

type MutationResult struct {
	Project     string `json:"project"`
	Environment string `json:"environment"`
	Revision    int64  `json:"revision"`
	Created     bool   `json:"created"`
}

type Change struct {
	Key           string `json:"key"`
	Kind          string `json:"kind"`
	Before        string `json:"before"`
	After         string `json:"after"`
	BeforeService string `json:"before_service"`
	AfterService  string `json:"after_service"`
}

type DiffResult struct {
	BeforeRevision int64    `json:"before_revision"`
	AfterRevision  int64    `json:"after_revision"`
	Changes        []Change `json:"changes"`
}

type ValidationError struct {
	Fields map[string]string
}

func (e *ValidationError) Error() string { return ErrInvalid.Error() }
func (e *ValidationError) Unwrap() error { return ErrInvalid }

type Service struct {
	repository             *repository
	now                    func() time.Time
	machineWriteAuthorizer MachineWriteAuthorizer
}

type revisionCreator struct {
	userID, machineIdentityID string
}

type environmentLocator struct {
	id, projectSlug, environmentSlug string
}

func NewService(store *database.Store, options ...ServiceOption) *Service {
	service := &Service{repository: newRepository(store), now: time.Now}
	for _, option := range options {
		if option != nil {
			option(service)
		}
	}
	return service
}

func (s *Service) Current(ctx context.Context, actor auth.User, environmentID, service string) (Revision, error) {
	return s.current(ctx, actor, environmentLocator{id: environmentID}, service)
}

func (s *Service) CurrentForProject(ctx context.Context, actor auth.User, projectSlug, environmentSlug, service string) (Revision, error) {
	return s.current(ctx, actor, environmentLocator{projectSlug: projectSlug, environmentSlug: environmentSlug}, service)
}

func (s *Service) current(ctx context.Context, actor auth.User, locator environmentLocator, service string) (Revision, error) {
	if err := s.readyActor(actor); err != nil {
		return Revision{}, err
	}
	if err := validateServiceFilter(service); err != nil {
		return Revision{}, err
	}
	_, environment, err := s.authorize(ctx, s.repository.store.DB(), actor.ID, locator, permissions.ActionReadConfig)
	if err != nil {
		return Revision{}, err
	}
	revision, err := s.repository.currentRevision(ctx, s.repository.store.DB(), environment.ID, service)
	if err != nil {
		return Revision{}, fmt.Errorf("read current revision: %w", err)
	}
	return revision, nil
}

func (s *Service) Replace(ctx context.Context, actor auth.User, environmentID string, input ReplaceInput) (Revision, error) {
	return s.replace(ctx, actor, environmentLocator{id: environmentID}, input)
}

func (s *Service) ReplaceForProject(ctx context.Context, actor auth.User, projectSlug, environmentSlug string, input ReplaceInput) (Revision, error) {
	return s.replace(ctx, actor, environmentLocator{projectSlug: projectSlug, environmentSlug: environmentSlug}, input)
}

func (s *Service) replace(ctx context.Context, actor auth.User, locator environmentLocator, input ReplaceInput) (Revision, error) {
	if err := s.readyActor(actor); err != nil {
		return Revision{}, err
	}
	normalized, err := validateReplaceInput(input)
	if err != nil {
		return Revision{}, err
	}
	var created Revision
	err = s.repository.store.InTx(ctx, func(tx *sql.Tx) error {
		currentActor, environment, err := s.authorize(ctx, tx, actor.ID, locator, permissions.ActionWriteConfig)
		if err != nil {
			return err
		}
		current, err := s.repository.currentRevision(ctx, tx, environment.ID, "")
		if err != nil {
			return err
		}
		if current.Version != normalized.BaseRevision {
			return ErrRevisionConflict
		}
		created, err = s.createSnapshotTx(ctx, tx, revisionCreator{userID: currentActor.ID}, environment, current, normalized.Message, normalized.Entries)
		return err
	})
	if err != nil {
		return Revision{}, fmt.Errorf("replace revision: %w", err)
	}
	return created, nil
}

func (s *Service) MutateForMachine(ctx context.Context, plaintext, projectSlug, environmentSlug string, input MachineMutationInput) (MutationResult, error) {
	if s == nil || s.repository == nil || s.repository.store == nil || s.now == nil || s.machineWriteAuthorizer == nil {
		return MutationResult{}, errors.New("revision service has no machine write authorizer")
	}
	result := MutationResult{Project: projectSlug, Environment: environmentSlug}
	err := s.repository.store.InTx(ctx, func(tx *sql.Tx) error {
		authorized, err := s.machineWriteAuthorizer.AuthorizeMachineWrite(ctx, tx, plaintext, projectSlug, environmentSlug)
		if err != nil {
			return err
		}
		current, err := s.repository.currentRevision(ctx, tx, authorized.EnvironmentID, "")
		if err != nil {
			return err
		}
		if current.Version != input.BaseRevision {
			return ErrRevisionConflict
		}
		entries, message, err := mergeMachineMutation(current.Entries, input)
		if err != nil {
			return err
		}
		if entriesEqual(current.Entries, entries) {
			result.Revision = current.Version
			return nil
		}
		created, err := s.createSnapshotTx(ctx, tx, revisionCreator{machineIdentityID: authorized.IdentityID}, environmentRecord{ID: authorized.EnvironmentID}, current, message, entries)
		if err != nil {
			return err
		}
		result.Revision = created.Version
		result.Created = true
		return nil
	})
	if err != nil {
		return MutationResult{}, fmt.Errorf("mutate machine revision: %w", err)
	}
	return result, nil
}

func (s *Service) List(ctx context.Context, actor auth.User, environmentID string) ([]RevisionSummary, error) {
	return s.list(ctx, actor, environmentLocator{id: environmentID})
}

func (s *Service) ListForProject(ctx context.Context, actor auth.User, projectSlug, environmentSlug string) ([]RevisionSummary, error) {
	return s.list(ctx, actor, environmentLocator{projectSlug: projectSlug, environmentSlug: environmentSlug})
}

func (s *Service) list(ctx context.Context, actor auth.User, locator environmentLocator) ([]RevisionSummary, error) {
	if err := s.readyActor(actor); err != nil {
		return nil, err
	}
	_, environment, err := s.authorize(ctx, s.repository.store.DB(), actor.ID, locator, permissions.ActionReadConfig)
	if err != nil {
		return nil, err
	}
	result, err := s.repository.list(ctx, s.repository.store.DB(), environment.ID)
	if err != nil {
		return nil, fmt.Errorf("list revisions: %w", err)
	}
	return result, nil
}

func (s *Service) Get(ctx context.Context, actor auth.User, environmentID string, version int64) (Revision, error) {
	return s.get(ctx, actor, environmentLocator{id: environmentID}, version)
}

func (s *Service) GetForProject(ctx context.Context, actor auth.User, projectSlug, environmentSlug string, version int64) (Revision, error) {
	return s.get(ctx, actor, environmentLocator{projectSlug: projectSlug, environmentSlug: environmentSlug}, version)
}

func (s *Service) get(ctx context.Context, actor auth.User, locator environmentLocator, version int64) (Revision, error) {
	if err := s.readyActor(actor); err != nil {
		return Revision{}, err
	}
	if err := validateVersion(version); err != nil {
		return Revision{}, err
	}
	_, environment, err := s.authorize(ctx, s.repository.store.DB(), actor.ID, locator, permissions.ActionReadConfig)
	if err != nil {
		return Revision{}, err
	}
	revision, err := s.repository.revisionByVersion(ctx, s.repository.store.DB(), environment.ID, version)
	if err != nil {
		return Revision{}, fmt.Errorf("get revision: %w", err)
	}
	return revision, nil
}

func (s *Service) Diff(ctx context.Context, actor auth.User, environmentID string, version int64) ([]Change, error) {
	result, err := s.diff(ctx, actor, environmentLocator{id: environmentID}, version)
	return result.Changes, err
}

func (s *Service) DiffForProject(ctx context.Context, actor auth.User, projectSlug, environmentSlug string, version int64) ([]Change, error) {
	result, err := s.diff(ctx, actor, environmentLocator{projectSlug: projectSlug, environmentSlug: environmentSlug}, version)
	return result.Changes, err
}

func (s *Service) DiffResultForProject(ctx context.Context, actor auth.User, projectSlug, environmentSlug string, version int64) (DiffResult, error) {
	return s.diff(ctx, actor, environmentLocator{projectSlug: projectSlug, environmentSlug: environmentSlug}, version)
}

func (s *Service) diff(ctx context.Context, actor auth.User, locator environmentLocator, version int64) (DiffResult, error) {
	if err := s.readyActor(actor); err != nil {
		return DiffResult{}, err
	}
	if err := validateVersion(version); err != nil {
		return DiffResult{}, err
	}
	_, environment, err := s.authorize(ctx, s.repository.store.DB(), actor.ID, locator, permissions.ActionReadConfig)
	if err != nil {
		return DiffResult{}, err
	}
	before, err := s.repository.revisionByVersion(ctx, s.repository.store.DB(), environment.ID, version)
	if err != nil {
		return DiffResult{}, fmt.Errorf("load diff base revision: %w", err)
	}
	after, err := s.repository.currentRevision(ctx, s.repository.store.DB(), environment.ID, "")
	if err != nil {
		return DiffResult{}, fmt.Errorf("load diff current revision: %w", err)
	}
	return DiffResult{BeforeRevision: before.Version, AfterRevision: after.Version, Changes: diffEntries(before.Entries, after.Entries)}, nil
}

func (s *Service) Rollback(ctx context.Context, actor auth.User, environmentID string, version int64, message string) (Revision, error) {
	return s.rollback(ctx, actor, environmentLocator{id: environmentID}, version, message)
}

func (s *Service) RollbackForProject(ctx context.Context, actor auth.User, projectSlug, environmentSlug string, version int64, message string) (Revision, error) {
	return s.rollback(ctx, actor, environmentLocator{projectSlug: projectSlug, environmentSlug: environmentSlug}, version, message)
}

func (s *Service) rollback(ctx context.Context, actor auth.User, locator environmentLocator, version int64, message string) (Revision, error) {
	if err := s.readyActor(actor); err != nil {
		return Revision{}, err
	}
	if err := validateVersion(version); err != nil {
		return Revision{}, err
	}
	normalizedMessage, err := validateMessage(message)
	if err != nil {
		return Revision{}, err
	}
	var created Revision
	err = s.repository.store.InTx(ctx, func(tx *sql.Tx) error {
		currentActor, environment, err := s.authorize(ctx, tx, actor.ID, locator, permissions.ActionWriteConfig)
		if err != nil {
			return err
		}
		target, err := s.repository.revisionByVersion(ctx, tx, environment.ID, version)
		if err != nil {
			return err
		}
		current, err := s.repository.currentRevision(ctx, tx, environment.ID, "")
		if err != nil {
			return err
		}
		created, err = s.createSnapshotTx(ctx, tx, revisionCreator{userID: currentActor.ID}, environment, current, normalizedMessage, target.Entries)
		return err
	})
	if err != nil {
		return Revision{}, fmt.Errorf("rollback revision: %w", err)
	}
	return created, nil
}

func (s *Service) createSnapshotTx(ctx context.Context, tx *sql.Tx, creator revisionCreator, environment environmentRecord, current Revision, message string, entries []Entry) (Revision, error) {
	if (current.ID == "" && current.Version != 0) ||
		(current.ID != "" && (current.Version <= 0 || current.Version == math.MaxInt64)) {
		return Revision{}, ErrDataIntegrity
	}
	if (creator.userID == "") == (creator.machineIdentityID == "") {
		return Revision{}, ErrDataIntegrity
	}
	now := s.now().UTC().Truncate(time.Second)
	revision := Revision{
		ID: uuid.NewString(), EnvironmentID: environment.ID, Version: current.Version + 1,
		Message: message, CreatedAt: now, Entries: append([]Entry(nil), entries...),
	}
	var userID, machineIdentityID any
	if creator.userID != "" {
		userID = creator.userID
		revision.CreatedBy = creator.userID
		revision.CreatedByType = "user"
	} else {
		machineIdentityID = creator.machineIdentityID
		revision.CreatedBy = creator.machineIdentityID
		revision.CreatedByType = "machine"
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO revisions
		(id, environment_id, version, message, created_by, created_by_machine_identity_id, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`, revision.ID, revision.EnvironmentID, revision.Version, revision.Message, userID, machineIdentityID, revision.CreatedAt.Unix())
	if err != nil {
		return Revision{}, database.ClassifyError(fmt.Errorf("insert revision: %w", err))
	}
	for _, entry := range revision.Entries {
		var service any
		if entry.Service != "" {
			service = entry.Service
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO revision_entries (revision_id, key, value, service) VALUES (?, ?, ?, ?)`, revision.ID, entry.Key, entry.Value, service); err != nil {
			return Revision{}, database.ClassifyError(fmt.Errorf("insert revision entry: %w", err))
		}
	}
	var result sql.Result
	if current.ID == "" {
		result, err = tx.ExecContext(ctx, `UPDATE environments SET current_revision_id = ?, updated_at = ?
			WHERE id = ? AND current_revision_id IS NULL`, revision.ID, now.Unix(), environment.ID)
	} else {
		result, err = tx.ExecContext(ctx, `UPDATE environments SET current_revision_id = ?, updated_at = ?
			WHERE id = ? AND current_revision_id = ?`, revision.ID, now.Unix(), environment.ID, current.ID)
	}
	if err != nil {
		return Revision{}, database.ClassifyError(fmt.Errorf("update current revision: %w", err))
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return Revision{}, fmt.Errorf("read current revision update count: %w", err)
	}
	if affected != 1 {
		return Revision{}, ErrRevisionConflict
	}
	return revision, nil
}

func (s *Service) authorize(ctx context.Context, q queryer, actorID string, locator environmentLocator, action string) (auth.User, environmentRecord, error) {
	actor, err := s.repository.currentActor(ctx, q, actorID)
	if err != nil {
		return auth.User{}, environmentRecord{}, err
	}
	var environment environmentRecord
	if locator.id != "" {
		environment, err = s.repository.environmentByID(ctx, q, locator.id, actor.ID)
	} else {
		if !slugPattern.MatchString(locator.projectSlug) || !slugPattern.MatchString(locator.environmentSlug) {
			if actor.Role == "member" {
				return auth.User{}, environmentRecord{}, ErrForbidden
			}
			return auth.User{}, environmentRecord{}, ErrNotFound
		}
		environment, err = s.repository.environmentBySlugs(ctx, q, locator.projectSlug, locator.environmentSlug, actor.ID)
	}
	if err != nil {
		if errors.Is(err, ErrNotFound) && actor.Role == "member" {
			return auth.User{}, environmentRecord{}, ErrForbidden
		}
		return auth.User{}, environmentRecord{}, err
	}
	if !permissions.Allowed(actor.Role, environment.Grant, action) {
		return auth.User{}, environmentRecord{}, ErrForbidden
	}
	return actor, environment, nil
}

func (s *Service) readyActor(actor auth.User) error {
	if s == nil || s.repository == nil || s.repository.store == nil || s.now == nil {
		return errors.New("revision service has no database store")
	}
	if !actor.Enabled || actor.ID == "" {
		return ErrForbidden
	}
	return nil
}

func mergeMachineMutation(current []Entry, input MachineMutationInput) ([]Entry, string, error) {
	if err := ValidateStoredEntries(current); err != nil {
		return nil, "", err
	}
	fields := make(map[string]string)
	switch input.Operation.Type {
	case "set":
		if input.Operation.Value == nil {
			fields["operation.value"] = "is required for set"
		}
	case "unset":
		if input.Operation.Value != nil {
			fields["operation.value"] = "must be omitted for unset"
		}
		if input.Operation.Service != nil {
			fields["operation.service"] = "must be omitted for unset"
		}
	default:
		fields["operation.type"] = "must be set or unset"
	}
	if len(fields) > 0 {
		return nil, "", &ValidationError{Fields: fields}
	}

	key := strings.TrimSpace(input.Operation.Key)
	entries := append([]Entry(nil), current...)
	index := -1
	for entryIndex := range entries {
		if entries[entryIndex].Key == key {
			index = entryIndex
			break
		}
	}
	if input.Operation.Type == "unset" {
		if index >= 0 {
			entries = append(entries[:index], entries[index+1:]...)
		}
	} else {
		service := ""
		if index >= 0 {
			service = entries[index].Service
		}
		if input.Operation.Service != nil {
			service = strings.TrimSpace(*input.Operation.Service)
		}
		entry := Entry{Key: key, Value: *input.Operation.Value, Service: service}
		if index >= 0 {
			entries[index] = entry
		} else {
			entries = append(entries, entry)
		}
	}

	normalized, err := validateReplaceInput(ReplaceInput{BaseRevision: input.BaseRevision, Message: input.Message, Entries: entries})
	if err != nil {
		return nil, "", err
	}
	return normalized.Entries, normalized.Message, nil
}

func entriesEqual(left, right []Entry) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func validateReplaceInput(input ReplaceInput) (ReplaceInput, error) {
	fields := make(map[string]string)
	if input.BaseRevision < 0 {
		fields["base_revision"] = "must be zero or a positive revision"
	}
	message, messageErr := validateMessage(input.Message)
	if messageErr != nil {
		fields["message"] = "must be valid UTF-8 and at most 1024 bytes"
	}
	if len(input.Entries) > MaxEntryCount {
		fields["entries"] = fmt.Sprintf("must contain at most %d entries", MaxEntryCount)
	}
	if len(fields) > 0 {
		return ReplaceInput{}, &ValidationError{Fields: fields}
	}
	entries := make([]Entry, 0, len(input.Entries))
	seen := make(map[string]struct{}, len(input.Entries))
	remainingBytes := int64(MaxSnapshotBytes)
	for index, inputEntry := range input.Entries {
		entry := Entry{Key: strings.TrimSpace(inputEntry.Key), Value: inputEntry.Value, Service: strings.TrimSpace(inputEntry.Service)}
		keyField := fmt.Sprintf("entries[%d].key", index)
		valueField := fmt.Sprintf("entries[%d].value", index)
		serviceField := fmt.Sprintf("entries[%d].service", index)
		if !utf8.ValidString(entry.Key) || len(entry.Key) > MaxKeyBytes || !keyPattern.MatchString(entry.Key) {
			fields[keyField] = fmt.Sprintf("must be a valid environment key of at most %d bytes", MaxKeyBytes)
		} else if _, exists := seen[entry.Key]; exists {
			fields[keyField] = "must be unique within the snapshot"
		} else {
			seen[entry.Key] = struct{}{}
		}
		if !utf8.ValidString(entry.Value) || len(entry.Value) > MaxValueBytes {
			fields[valueField] = fmt.Sprintf("must be valid UTF-8 and at most %d bytes", MaxValueBytes)
		}
		if !utf8.ValidString(entry.Service) || len(entry.Service) > MaxServiceBytes {
			fields[serviceField] = fmt.Sprintf("must be valid UTF-8 and at most %d bytes", MaxServiceBytes)
		}
		for _, byteCount := range []int{len(entry.Key), len(entry.Value), len(entry.Service)} {
			var ok bool
			remainingBytes, ok = consumeSnapshotBudget(remainingBytes, byteCount)
			if !ok {
				fields["entries"] = fmt.Sprintf("combined entry content must be at most %d bytes", MaxSnapshotBytes)
				break
			}
		}
		entries = append(entries, entry)
	}
	if len(fields) > 0 {
		return ReplaceInput{}, &ValidationError{Fields: fields}
	}
	sort.Slice(entries, func(left, right int) bool { return entries[left].Key < entries[right].Key })
	return ReplaceInput{BaseRevision: input.BaseRevision, Message: message, Entries: entries}, nil
}

// ValidateStoredEntries verifies that persisted snapshot entries still satisfy
// the canonical limits enforced when a revision is created. It intentionally
// returns only a generic integrity error so stored configuration cannot leak
// through diagnostics.
func ValidateStoredEntries(entries []Entry) error {
	if len(entries) > MaxEntryCount {
		return ErrDataIntegrity
	}
	seen := make(map[string]struct{}, len(entries))
	remainingBytes := int64(MaxSnapshotBytes)
	for _, entry := range entries {
		if !utf8.ValidString(entry.Key) || len(entry.Key) > MaxKeyBytes || strings.TrimSpace(entry.Key) != entry.Key || !keyPattern.MatchString(entry.Key) {
			return ErrDataIntegrity
		}
		if _, exists := seen[entry.Key]; exists {
			return ErrDataIntegrity
		}
		seen[entry.Key] = struct{}{}
		if !utf8.ValidString(entry.Value) || len(entry.Value) > MaxValueBytes {
			return ErrDataIntegrity
		}
		if !utf8.ValidString(entry.Service) || len(entry.Service) > MaxServiceBytes || strings.TrimSpace(entry.Service) != entry.Service {
			return ErrDataIntegrity
		}
		for _, byteCount := range []int{len(entry.Key), len(entry.Value), len(entry.Service)} {
			var ok bool
			remainingBytes, ok = consumeSnapshotBudget(remainingBytes, byteCount)
			if !ok {
				return ErrDataIntegrity
			}
		}
	}
	return nil
}

func consumeSnapshotBudget(remaining int64, byteCount int) (int64, bool) {
	if byteCount < 0 || int64(byteCount) > remaining {
		return remaining, false
	}
	return remaining - int64(byteCount), true
}

func validateMessage(message string) (string, error) {
	normalized := strings.TrimSpace(message)
	if !utf8.ValidString(normalized) || len(normalized) > MaxMessageBytes {
		return "", &ValidationError{Fields: map[string]string{"message": fmt.Sprintf("must be valid UTF-8 and at most %d bytes", MaxMessageBytes)}}
	}
	return normalized, nil
}

func validateServiceFilter(service string) error {
	if !utf8.ValidString(service) || len(service) > MaxServiceBytes {
		return &ValidationError{Fields: map[string]string{"service": fmt.Sprintf("must be valid UTF-8 and at most %d bytes", MaxServiceBytes)}}
	}
	return nil
}

func validateVersion(version int64) error {
	if version <= 0 {
		return &ValidationError{Fields: map[string]string{"version": "must be a positive revision"}}
	}
	return nil
}

func diffEntries(before, after []Entry) []Change {
	beforeByKey := make(map[string]Entry, len(before))
	afterByKey := make(map[string]Entry, len(after))
	keys := make(map[string]struct{}, len(before)+len(after))
	for _, entry := range before {
		beforeByKey[entry.Key] = entry
		keys[entry.Key] = struct{}{}
	}
	for _, entry := range after {
		afterByKey[entry.Key] = entry
		keys[entry.Key] = struct{}{}
	}
	orderedKeys := make([]string, 0, len(keys))
	for key := range keys {
		orderedKeys = append(orderedKeys, key)
	}
	sort.Strings(orderedKeys)
	changes := make([]Change, 0)
	for _, key := range orderedKeys {
		beforeEntry, hadBefore := beforeByKey[key]
		afterEntry, hasAfter := afterByKey[key]
		switch {
		case !hadBefore:
			changes = append(changes, Change{Key: key, Kind: "added", After: afterEntry.Value, AfterService: afterEntry.Service})
		case !hasAfter:
			changes = append(changes, Change{Key: key, Kind: "deleted", Before: beforeEntry.Value, BeforeService: beforeEntry.Service})
		case beforeEntry.Value != afterEntry.Value || beforeEntry.Service != afterEntry.Service:
			changes = append(changes, Change{Key: key, Kind: "changed", Before: beforeEntry.Value, After: afterEntry.Value, BeforeService: beforeEntry.Service, AfterService: afterEntry.Service})
		}
	}
	return changes
}
