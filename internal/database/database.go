package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"

	"modernc.org/sqlite"
)

const driverName = "sqlite"

var ErrBusy = errors.New("database temporarily unavailable")

// ClassifyError adds a stable availability sentinel for SQLite BUSY/LOCKED
// errors while retaining the original driver error in the unwrap chain.
func ClassifyError(err error) error {
	if err == nil {
		return nil
	}
	var sqliteErr *sqlite.Error
	if errors.As(err, &sqliteErr) {
		switch sqliteErr.Code() & 0xff {
		case 5, 6:
			return errors.Join(ErrBusy, err)
		}
	}
	return err
}

// Store owns the database connection pool.
type Store struct {
	db *sql.DB
}

// Open opens a SQLite database, configures each connection for safe concurrent
// access, and applies all embedded migrations.
func Open(path string) (*Store, error) {
	if path == "" {
		return nil, errors.New("database path is empty")
	}

	absPath, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolve database path: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(absPath), 0o700); err != nil {
		return nil, fmt.Errorf("create database directory: %w", err)
	}
	if err := ensurePrivateDatabaseFile(absPath); err != nil {
		return nil, err
	}
	if err := hardenDatabaseFiles(absPath); err != nil {
		return nil, err
	}

	db, err := sql.Open(driverName, sqliteDSN(absPath))
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	store := &Store{db: db}
	if err := store.Ready(context.Background()); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("initialize database connection: %w", err)
	}
	if err := applyMigrations(context.Background(), db); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := hardenDatabaseFiles(absPath); err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

func ensurePrivateDatabaseFile(path string) error {
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return fmt.Errorf("create database file: %w", err)
	}
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return fmt.Errorf("restrict database file permissions: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close database file: %w", err)
	}
	return nil
}

func hardenDatabaseFiles(path string) error {
	for _, file := range []struct {
		path string
		kind string
	}{
		{path: path, kind: "database"},
		{path: path + "-wal", kind: "database WAL"},
		{path: path + "-shm", kind: "database SHM"},
	} {
		if err := os.Chmod(file.path, 0o600); err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return fmt.Errorf("restrict %s permissions: %w", file.kind, err)
		}
	}
	return nil
}

func sqliteDSN(path string) string {
	u := &url.URL{Scheme: "file", Path: path}
	query := url.Values{}
	query.Add("_pragma", "foreign_keys(ON)")
	query.Add("_pragma", "journal_mode(WAL)")
	query.Add("_pragma", "busy_timeout(5000)")
	query.Set("_txlock", "immediate")
	u.RawQuery = query.Encode()
	return u.String()
}

// DB returns the underlying connection pool for database-specific operations.
func (s *Store) DB() *sql.DB {
	return s.db
}

// Close closes the database connection pool.
func (s *Store) Close() error {
	return s.db.Close()
}

// Ready verifies that the database accepts queries.
func (s *Store) Ready(ctx context.Context) error {
	if err := s.db.PingContext(ctx); err != nil {
		return fmt.Errorf("ping database: %w", err)
	}
	var one int
	if err := s.db.QueryRowContext(ctx, "SELECT 1").Scan(&one); err != nil {
		return fmt.Errorf("query database readiness: %w", err)
	}
	return nil
}

// InTx executes fn in an immediate SQLite transaction. Callback errors are
// returned after rollback; a successful callback is committed.
func (s *Store) InTx(ctx context.Context, fn func(*sql.Tx) error) (err error) {
	if fn == nil {
		return errors.New("transaction callback is nil")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ClassifyError(fmt.Errorf("begin transaction: %w", err))
	}

	defer func() {
		if recovered := recover(); recovered != nil {
			_ = tx.Rollback()
			panic(recovered)
		}
		if err != nil {
			if rollbackErr := tx.Rollback(); rollbackErr != nil && !errors.Is(rollbackErr, sql.ErrTxDone) {
				err = fmt.Errorf("%w (rollback transaction: %v)", err, rollbackErr)
			}
		} else if commitErr := tx.Commit(); commitErr != nil {
			err = fmt.Errorf("commit transaction: %w", commitErr)
		}
		err = ClassifyError(err)
	}()

	if callbackErr := fn(tx); callbackErr != nil {
		return callbackErr
	}
	return nil
}
