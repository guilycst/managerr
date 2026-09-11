// Package storage owns Managerr's local SQLite journal. It contains only
// durable local state; adapters remain responsible for upstream authority.
package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/golang-migrate/migrate/v4"
	migratedb "github.com/golang-migrate/migrate/v4/database/sqlite"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/guilycst/managerr/internal/storage/sqlc"
	"github.com/guilycst/managerr/migrations"
	_ "modernc.org/sqlite"
)

const (
	defaultBusyTimeout = 5 * time.Second
	defaultLockSuffix  = ".lock"
)

// Options controls one database owner. Path must identify one persistent
// SQLite database. MigrationsFS is injectable for isolated migration tests.
type Options struct {
	Path         string
	LockPath     string
	BusyTimeout  time.Duration
	MigrationsFS fs.FS
}

// Store is a single-process handle to the local journal.
type Store struct {
	db          *sql.DB
	queries     *sqlc.Queries
	lock        *processLock
	dsn         string
	busyTimeout time.Duration
	closeOnce   sync.Once
	closeErr    error
}

// Open opens, migrates and locks a SQLite journal.
func Open(path string) (*Store, error) {
	return OpenWithOptions(Options{Path: path})
}

// OpenWithOptions opens, migrates and locks a SQLite journal.
func OpenWithOptions(options Options) (*Store, error) {
	path, err := normalizeDatabasePath(options.Path)
	if err != nil {
		return nil, err
	}
	lockPath := options.LockPath
	if strings.TrimSpace(lockPath) == "" {
		lockPath = path + defaultLockSuffix
	} else if path != ":memory:" {
		// A persistent database has one executor identity. Allowing callers to
		// pick a different lock file would let two processes open one journal
		// concurrently, even when both paths are individually canonical.
		requestedLockPath, err := normalizeLockPath(lockPath)
		if err != nil {
			return nil, err
		}
		derivedLockPath, err := normalizeLockPath(path + defaultLockSuffix)
		if err != nil {
			return nil, err
		}
		if requestedLockPath != derivedLockPath {
			return nil, fmt.Errorf("custom storage lock path must equal the canonical database lock path %q", derivedLockPath)
		}
		lockPath = derivedLockPath
	}
	lockPath, err = normalizeLockPath(lockPath)
	if err != nil {
		return nil, err
	}
	lock, err := acquireProcessLock(lockPath)
	if err != nil {
		return nil, err
	}
	// A database or its parent can be replaced through a symlink between path
	// normalization and opening the file. Re-resolve both identities while the
	// process lock is held and fail closed if either spelling changed.
	if path != ":memory:" {
		verifiedPath, err := canonicalizePath(path)
		if err != nil || verifiedPath != path {
			_ = lock.close()
			if err != nil {
				return nil, err
			}
			return nil, fmt.Errorf("storage database path changed while acquiring lock")
		}
	}
	verifiedLockPath, err := canonicalizePath(lockPath)
	if err != nil || verifiedLockPath != lockPath {
		_ = lock.close()
		if err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("storage lock path changed while acquiring lock")
	}

	busyTimeout := options.BusyTimeout
	if busyTimeout <= 0 {
		busyTimeout = defaultBusyTimeout
	}
	dsn := sqliteDSN(path, busyTimeout)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		_ = lock.close()
		return nil, fmt.Errorf("open sqlite database: %w", err)
	}
	// SQLite is deliberately single-writer; one pooled connection keeps the
	// per-connection safety pragmas effective and gives deterministic writes.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxIdleTime(0)
	if err := configureSQLite(db, busyTimeout); err != nil {
		_ = db.Close()
		_ = lock.close()
		return nil, err
	}

	store := &Store{db: db, queries: sqlc.New(db), lock: lock, dsn: dsn, busyTimeout: busyTimeout}
	if err := store.migrate(options.MigrationsFS); err != nil {
		_ = store.closeResources()
		return nil, err
	}
	return store, nil
}

// DB exposes the database for transaction-scoped sqlc queries and backup
// procedures. Callers must not change connection limits or pragmas.
func (store *Store) DB() *sql.DB {
	if store == nil {
		return nil
	}
	return store.db
}

// Queries exposes generated sqlc access for repositories and transaction
// boundaries. Use Queries.WithTx for work that must commit atomically.
func (store *Store) Queries() *sqlc.Queries {
	if store == nil {
		return nil
	}
	return store.queries
}

// WithTx runs generated queries in one short SQLite transaction. Callers use
// this for boundaries such as approval, action enqueue and audit insertion;
// external network or filesystem work must happen after the callback returns.
func (store *Store) WithTx(ctx context.Context, fn func(*sqlc.Queries) error) error {
	if store == nil || store.db == nil {
		return errors.New("storage store is nil")
	}
	if fn == nil {
		return errors.New("storage transaction callback is required")
	}
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin storage transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := fn(store.queries.WithTx(tx)); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit storage transaction: %w", err)
	}
	return nil
}

// Close releases the process lock after closing the database. It is safe to
// call more than once.
func (store *Store) Close() error {
	if store == nil {
		return nil
	}
	store.closeOnce.Do(func() { store.closeErr = store.closeResources() })
	return store.closeErr
}

func (store *Store) closeResources() error {
	var errs []error
	if store.db != nil {
		if err := store.db.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	if store.lock != nil {
		if err := store.lock.close(); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (store *Store) migrate(migrationFS fs.FS) error {
	if migrationFS == nil {
		migrationFS = migrations.FS
	}
	// The migrate database driver closes its *sql.DB when its runner closes.
	// Use a separate handle so closing the runner cannot invalidate Store.DB.
	migrationDB, err := sql.Open("sqlite", store.dsn)
	if err != nil {
		return fmt.Errorf("open migration sqlite database: %w", err)
	}
	migrationDB.SetMaxOpenConns(1)
	migrationDB.SetMaxIdleConns(1)
	if err := configureSQLite(migrationDB, store.busyTimeout); err != nil {
		_ = migrationDB.Close()
		return err
	}
	source, err := iofs.New(migrationFS, ".")
	if err != nil {
		_ = migrationDB.Close()
		return fmt.Errorf("create migration source: %w", err)
	}
	// The correction migration rebuilds SQLite tables with foreign-key checks
	// temporarily disabled. It manages that narrow migration boundary itself;
	// failed runs remain dirty and therefore cannot serve traffic.
	driver, err := migratedb.WithInstance(migrationDB, &migratedb.Config{NoTxWrap: true})
	if err != nil {
		_ = migrationDB.Close()
		return fmt.Errorf("create sqlite migration driver: %w", err)
	}
	runner, err := migrate.NewWithInstance("iofs", source, "sqlite", driver)
	if err != nil {
		_ = driver.Close()
		return fmt.Errorf("create migration runner: %w", err)
	}
	if err := runner.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		_, _ = runner.Close()
		return fmt.Errorf("run sqlite migrations: %w", err)
	}
	sourceErr, databaseErr := runner.Close()
	if sourceErr != nil || databaseErr != nil {
		return errors.Join(sourceErr, databaseErr)
	}
	return nil
}

func normalizeDatabasePath(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", errors.New("storage database path is required")
	}
	if path == ":memory:" {
		return path, nil
	}
	abs, err := filepath.Abs(filepath.Clean(path))
	if err != nil {
		return "", fmt.Errorf("resolve storage database path: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o700); err != nil {
		return "", fmt.Errorf("create storage database directory: %w", err)
	}
	canonical, err := canonicalizePath(abs)
	if err != nil {
		return "", fmt.Errorf("resolve storage database identity: %w", err)
	}
	return canonical, nil
}

func normalizeLockPath(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", errors.New("storage lock path is required")
	}
	abs, err := filepath.Abs(filepath.Clean(path))
	if err != nil {
		return "", fmt.Errorf("resolve storage lock path: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o700); err != nil {
		return "", fmt.Errorf("create storage lock directory: %w", err)
	}
	canonical, err := canonicalizePath(abs)
	if err != nil {
		return "", fmt.Errorf("resolve storage lock identity: %w", err)
	}
	return canonical, nil
}

// canonicalizePath resolves an existing file or directory and resolves the
// nearest existing parent for a path that will be created. Dangling symlinks
// are rejected so an alias cannot acquire a lock for a different future file.
func canonicalizePath(path string) (string, error) {
	canonical, err := filepath.EvalSymlinks(path)
	if err == nil {
		return filepath.Clean(canonical), nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return "", err
	}
	if info, lstatErr := os.Lstat(path); lstatErr == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("dangling symlink %q", path)
		}
		return filepath.Clean(path), nil
	} else if !errors.Is(lstatErr, fs.ErrNotExist) {
		return "", lstatErr
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(path))
	if err != nil {
		return "", err
	}
	return filepath.Clean(filepath.Join(parent, filepath.Base(path))), nil
}

func sqliteDSN(path string, busyTimeout time.Duration) string {
	if path == ":memory:" {
		return fmt.Sprintf("file:managerr-memory?mode=memory&cache=shared&_pragma=busy_timeout(%d)&_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=synchronous(FULL)", busyTimeout.Milliseconds())
	}
	return fmt.Sprintf("file:%s?_pragma=busy_timeout(%d)&_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=synchronous(FULL)", filepath.ToSlash(path), busyTimeout.Milliseconds())
}

func configureSQLite(db *sql.DB, busyTimeout time.Duration) error {
	pragmas := []string{
		"PRAGMA foreign_keys = ON",
		"PRAGMA journal_mode = WAL",
		"PRAGMA synchronous = FULL",
		fmt.Sprintf("PRAGMA busy_timeout = %d", busyTimeout.Milliseconds()),
	}
	for _, pragma := range pragmas {
		if _, err := db.Exec(pragma); err != nil {
			return fmt.Errorf("configure sqlite (%s): %w", pragma, err)
		}
	}
	return nil
}
