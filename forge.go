// Package forge manages deployment environments for agents,
// with multi-repo workspaces, changeset grouping, and preview deployment.
package forge

import (
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// DefaultPath returns the default database path
func DefaultPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	return filepath.Join(home, ".config", "forge", "forge.db")
}

// dataSourceName builds the sqlite3 DSN for a database path.
//
// foreign_keys MUST be set here rather than with a PRAGMA statement after
// Open: it is a per-connection setting, and *sql.DB is a pool. A one-shot
// db.Exec("PRAGMA foreign_keys=ON") reaches only whichever connection the
// pool happened to hand out, so every other connection keeps SQLite's
// default of OFF and the ON DELETE CASCADE clauses in our schema silently
// do not fire. Putting it in the DSN applies it to every connection the
// pool opens. (journal_mode=WAL needs no such care: it is a property of the
// database file, so setting it once is permanent.)
//
// The path is made absolute because a DSN is a URI, not a filename: SQLite
// reads the segment after "file://" as an authority, so a relative path like
// "./forge.db" would otherwise arrive as the authority "." and be rejected.
func dataSourceName(path string) string {
	absolutePath, err := filepath.Abs(path)
	if err != nil {
		absolutePath = path
	}
	dsn := url.URL{
		Scheme:   "file",
		Path:     absolutePath,
		OmitHost: true,
		RawQuery: url.Values{"_foreign_keys": {"on"}}.Encode(),
	}
	return dsn.String()
}

// Forge is the main handle
type Forge struct {
	db         *sql.DB
	containers *ContainerManager
}

// Open opens or creates the forge database
func Open(path string) (*Forge, error) {
	if path == "" {
		path = DefaultPath()
	}

	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return nil, fmt.Errorf("create config dir: %w", err)
	}

	db, err := sql.Open("sqlite3", dataSourceName(path))
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}

	if _, err := db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		db.Close()
		return nil, fmt.Errorf("enable WAL: %w", err)
	}

	// A misspelled DSN key is silently ignored by the driver, so prove the
	// setting actually took effect instead of trusting the connection string.
	var foreignKeysEnabled bool
	if err := db.QueryRow("PRAGMA foreign_keys").Scan(&foreignKeysEnabled); err != nil {
		db.Close()
		return nil, fmt.Errorf("read foreign_keys pragma: %w", err)
	}
	if !foreignKeysEnabled {
		db.Close()
		return nil, fmt.Errorf("foreign key enforcement is off: the %q DSN did not enable it", dataSourceName(path))
	}

	// Create v1 schema first (for migration compatibility)
	if _, err := db.Exec(schemaSQL); err != nil {
		db.Close()
		return nil, fmt.Errorf("init v1 schema: %w", err)
	}

	// Apply v2 schema (environments, changesets)
	if _, err := db.Exec(schemaV2SQL); err != nil {
		db.Close()
		return nil, fmt.Errorf("init v2 schema: %w", err)
	}

	// Apply v3 schema (containers)
	if _, err := db.Exec(schemaV3SQL); err != nil {
		db.Close()
		return nil, fmt.Errorf("init v3 schema: %w", err)
	}

	// Migrate existing tables with new columns
	f := &Forge{db: db}
	if err := f.MigrateV2(); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate v2: %w", err)
	}

	// Migrate to v3 (container-based)
	if err := f.MigrateV3(); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate v3: %w", err)
	}

	// Initialize slot schema
	if err := f.InitSlotSchema(); err != nil {
		db.Close()
		return nil, fmt.Errorf("init slot schema: %w", err)
	}

	// Initialize container manager
	f.containers = NewContainerManager(f)

	return f, nil
}

// Containers returns the container manager
func (f *Forge) Containers() *ContainerManager {
	return f.containers
}

// Close closes the database
func (f *Forge) Close() error {
	return f.db.Close()
}

// DB exposes the underlying database for advanced queries
func (f *Forge) DB() *sql.DB {
	return f.db
}

// now returns current unix timestamp
func now() int64 {
	return time.Now().Unix()
}

// expandHome expands ~ to home directory.
func expandHome(path string) string {
	if strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return path
		}
		return filepath.Join(home, path[2:])
	}
	return path
}

// SlotCommit is a v2 stub (deprecated).
func (f *Forge) SlotCommit(project string, slotID int, msg string) error {
	return nil
}
