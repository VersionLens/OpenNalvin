package knowledge

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"sort"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

func Migrate(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS app_migrations (
			name TEXT PRIMARY KEY,
			applied_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
		)`); err != nil {
		return fmt.Errorf("create app_migrations: %w", err)
	}

	entries, err := fs.Glob(migrationFS, "migrations/*.sql")
	if err != nil {
		return fmt.Errorf("glob app migrations: %w", err)
	}
	sort.Strings(entries)

	for _, path := range entries {
		var exists int
		if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM app_migrations WHERE name = ?`, path).Scan(&exists); err != nil {
			return fmt.Errorf("check migration %s: %w", path, err)
		}
		if exists > 0 {
			continue
		}

		sqlBytes, err := fs.ReadFile(migrationFS, path)
		if err != nil {
			return fmt.Errorf("read migration %s: %w", path, err)
		}

		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("begin migration %s: %w", path, err)
		}
		if _, err := tx.ExecContext(ctx, string(sqlBytes)); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("apply migration %s: %w", path, err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO app_migrations (name) VALUES (?)`, path); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("record migration %s: %w", path, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit migration %s: %w", path, err)
		}
	}

	return nil
}
