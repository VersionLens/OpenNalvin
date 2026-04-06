package database

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	sqlite_vec "github.com/asg017/sqlite-vec-go-bindings/cgo"
	"github.com/riverqueue/river/riverdriver/riversqlite"
	"github.com/riverqueue/river/rivermigrate"

	"github.com/versionlens/OpenNalvin/internal/knowledge"

	_ "github.com/mattn/go-sqlite3"
)

var registerVec sync.Once

func Open(ctx context.Context, path string) (*sql.DB, error) {
	if path == "" {
		return nil, fmt.Errorf("database path is required")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create database directory: %w", err)
	}

	registerVec.Do(sqlite_vec.Auto)

	dsn := fmt.Sprintf("file:%s?_busy_timeout=5000&_foreign_keys=on&_journal_mode=WAL", filepath.Clean(path))
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite database: %w", err)
	}

	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("ping sqlite database: %w", err)
	}

	if err := applyPragmas(ctx, db); err != nil {
		db.Close()
		return nil, err
	}
	if err := validateVec(ctx, db); err != nil {
		db.Close()
		return nil, err
	}
	if err := knowledge.Migrate(ctx, db); err != nil {
		db.Close()
		return nil, fmt.Errorf("run app migrations: %w", err)
	}
	if err := migrateRiver(ctx, db); err != nil {
		db.Close()
		return nil, fmt.Errorf("run river migrations: %w", err)
	}

	return db, nil
}

func applyPragmas(ctx context.Context, db *sql.DB) error {
	pragmas := []string{
		"PRAGMA foreign_keys = ON",
		"PRAGMA journal_mode = WAL",
		"PRAGMA busy_timeout = 5000",
	}
	for _, pragma := range pragmas {
		if _, err := db.ExecContext(ctx, pragma); err != nil {
			return fmt.Errorf("apply %q: %w", pragma, err)
		}
	}
	return nil
}

func validateVec(ctx context.Context, db *sql.DB) error {
	var version string
	if err := db.QueryRowContext(ctx, "SELECT vec_version()").Scan(&version); err != nil {
		return fmt.Errorf("validate sqlite-vec: %w", err)
	}
	return nil
}

func migrateRiver(ctx context.Context, db *sql.DB) error {
	driver := riversqlite.New(db)
	migrator, err := rivermigrate.New(driver, nil)
	if err != nil {
		return fmt.Errorf("create river migrator: %w", err)
	}
	if _, err := migrator.Migrate(ctx, rivermigrate.DirectionUp, nil); err != nil {
		return fmt.Errorf("migrate river schema: %w", err)
	}
	return nil
}
