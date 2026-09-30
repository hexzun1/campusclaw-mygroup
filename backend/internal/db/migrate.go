package db

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"
	"log"
	"sort"
)

// schemaMigrationsDDL records which migration files have already been applied.
// The version is the file name, so applying a migration and recording it are
// unambiguous even if the file is later edited.
const schemaMigrationsDDL = `CREATE TABLE IF NOT EXISTS schema_migrations (
    version VARCHAR(255) NOT NULL PRIMARY KEY,
    applied_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`

// Migrate applies every *.sql file in files that schema_migrations does not
// record yet, in filename order (design.md Decision 2).
//
// It opens its own connection with multiStatements enabled, because one
// migration file holds several DDL statements. MySQL commits DDL implicitly,
// so a file that fails midway can leave partial state; every existing
// migration is written to be re-runnable (IF NOT EXISTS) for that reason.
func Migrate(ctx context.Context, host, port, name, user, password string, files fs.FS) error {
	conn, err := sql.Open("mysql", dsn(host, port, name, user, password, true))
	if err != nil {
		return fmt.Errorf("open migration connection: %w", err)
	}
	defer conn.Close()

	if err := conn.PingContext(ctx); err != nil {
		return fmt.Errorf("ping for migration: %w", err)
	}
	if _, err := conn.ExecContext(ctx, schemaMigrationsDDL); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}

	applied, err := appliedVersions(ctx, conn)
	if err != nil {
		return err
	}

	versions, err := migrationFiles(files)
	if err != nil {
		return err
	}

	for _, version := range versions {
		if applied[version] {
			continue
		}
		body, err := fs.ReadFile(files, version)
		if err != nil {
			return fmt.Errorf("read migration %s: %w", version, err)
		}
		if _, err := conn.ExecContext(ctx, string(body)); err != nil {
			return fmt.Errorf("apply migration %s: %w", version, err)
		}
		if _, err := conn.ExecContext(ctx,
			`INSERT INTO schema_migrations (version) VALUES (?)`, version); err != nil {
			return fmt.Errorf("record migration %s: %w", version, err)
		}
		log.Printf("migration applied: %s", version)
	}
	return nil
}

func appliedVersions(ctx context.Context, conn *sql.DB) (map[string]bool, error) {
	rows, err := conn.QueryContext(ctx, `SELECT version FROM schema_migrations`)
	if err != nil {
		return nil, fmt.Errorf("read schema_migrations: %w", err)
	}
	defer rows.Close()

	applied := map[string]bool{}
	for rows.Next() {
		var version string
		if err := rows.Scan(&version); err != nil {
			return nil, fmt.Errorf("scan schema_migrations: %w", err)
		}
		applied[version] = true
	}
	return applied, rows.Err()
}

// migrationFiles lists the *.sql files in filename order.
func migrationFiles(files fs.FS) ([]string, error) {
	versions, err := fs.Glob(files, "*.sql")
	if err != nil {
		return nil, fmt.Errorf("list migrations: %w", err)
	}
	sort.Strings(versions)
	return versions, nil
}
