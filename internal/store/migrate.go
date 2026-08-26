package store

import (
	"database/sql"
	"embed"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// migrationsFS embeds the SQL migration files shipped with the service. They
// are applied in filename order and tracked with SQLite's user_version pragma
// so the schema is migrated idempotently across restarts.
//
//go:embed migrations/*.sql
var migrationsFS embed.FS

// applyMigrations brings the database up to the latest schema version. Each
// migration runs in its own transaction together with the user_version bump.
func applyMigrations(db *sql.DB) error {
	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		return fmt.Errorf("store: read migrations: %w", err)
	}

	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".sql") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)

	var current int
	if err := db.QueryRow("PRAGMA user_version").Scan(&current); err != nil {
		return fmt.Errorf("store: read schema version: %w", err)
	}

	for _, name := range names {
		version, err := migrationVersion(name)
		if err != nil {
			return err
		}
		if version <= current {
			continue
		}

		sqlBytes, err := migrationsFS.ReadFile("migrations/" + name)
		if err != nil {
			return fmt.Errorf("store: read migration %s: %w", name, err)
		}

		tx, err := db.Begin()
		if err != nil {
			return fmt.Errorf("store: begin migration %s: %w", name, err)
		}
		if _, err := tx.Exec(string(sqlBytes)); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("store: apply migration %s: %w", name, err)
		}
		// PRAGMA user_version cannot be parameterized; version is a parsed,
		// non-negative integer derived from a controlled filename.
		if _, err := tx.Exec(fmt.Sprintf("PRAGMA user_version = %d", version)); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("store: record migration %s: %w", name, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("store: commit migration %s: %w", name, err)
		}
		current = version
	}
	return nil
}

// migrationVersion parses the leading integer of a migration filename such as
// "0001_init.sql" into its numeric version.
func migrationVersion(name string) (int, error) {
	base := name
	if i := strings.IndexByte(base, '_'); i >= 0 {
		base = base[:i]
	}
	if i := strings.IndexByte(base, '.'); i >= 0 {
		base = base[:i]
	}
	v, err := strconv.Atoi(base)
	if err != nil || v < 0 {
		return 0, fmt.Errorf("store: invalid migration filename %q", name)
	}
	return v, nil
}
