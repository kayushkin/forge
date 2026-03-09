// Package forge manages deployment environments for agents,
// with multi-repo workspaces, changeset grouping, and preview deployment.
package forge

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
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

// Forge is the main handle
type Forge struct {
	db *sql.DB
}

// Open opens or creates the forge database
func Open(path string) (*Forge, error) {
	if path == "" {
		path = DefaultPath()
	}

	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return nil, fmt.Errorf("create config dir: %w", err)
	}

	db, err := sql.Open("sqlite3", path)
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}

	if _, err := db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		db.Close()
		return nil, fmt.Errorf("enable WAL: %w", err)
	}

	if _, err := db.Exec("PRAGMA foreign_keys=ON"); err != nil {
		db.Close()
		return nil, fmt.Errorf("enable foreign keys: %w", err)
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

	return &Forge{db: db}, nil
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
