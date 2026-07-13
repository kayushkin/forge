package forge

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
)

// execer is satisfied by both *sql.DB (which picks an arbitrary connection out
// of the pool) and a single checked-out *sql.Conn.
type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// openTestForge opens a forge database in a temp dir.
func openTestForge(t *testing.T) *Forge {
	t.Helper()
	f, err := Open(filepath.Join(t.TempDir(), "forge.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { f.Close() })
	return f
}

// seedProject inserts a v3 project, the parent of most of the schema's cascades.
func seedProject(t *testing.T, f *Forge, id string) {
	t.Helper()
	if _, err := f.db.Exec(
		`INSERT INTO projects_v3 (id, name, base_port, created_at, updated_at) VALUES (?, ?, ?, ?, ?)`,
		id, id, 9000, now(), now(),
	); err != nil {
		t.Fatalf("seed project: %v", err)
	}
}

// insertSlot adds a slot to a project, returning the error so callers can
// assert on rejection as well as on success.
func insertSlot(ctx context.Context, db execer, slotNum int, projectID string) error {
	_, err := db.ExecContext(ctx,
		`INSERT INTO slots_v3 (project_id, slot_num, container_name, base_port, created_at)
		 VALUES (?, ?, ?, ?, ?)`,
		projectID, slotNum, fmt.Sprintf("%s-%d", projectID, slotNum), 9000+slotNum, now())
	return err
}

// TestForeignKeysEnforcedOnEveryPooledConnection is the regression test for the
// bug where forge enabled foreign_keys with a one-shot db.Exec on a *sql.DB.
// That reaches a single connection out of the pool; every other connection kept
// SQLite's default of OFF, so whether a constraint fired depended on which
// connection the pool happened to hand you.
//
// It asserts the constraint actually FIRES rather than reading the pragma back,
// because a rejected write is the contract we care about — and the pragma
// readout proved to be an unreliable proxy for it.
func TestForeignKeysEnforcedOnEveryPooledConnection(t *testing.T) {
	f := openTestForge(t)
	ctx := context.Background()

	// Force the pool to hand out several distinct connections at once, and have
	// each attempt a write the schema forbids: a slot whose project row does not
	// exist. Under the old one-shot PRAGMA these inserts were accepted.
	const connections = 8
	gate := make(chan struct{})

	var wg sync.WaitGroup
	accepted := make([]bool, connections)
	for i := 0; i < connections; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			conn, err := f.db.Conn(ctx)
			if err != nil {
				t.Errorf("checkout connection %d: %v", i, err)
				return
			}
			defer conn.Close()

			// Stay checked out until every goroutine holds one, so the pool is
			// obliged to open a distinct connection for each of us.
			<-gate
			accepted[i] = insertSlot(ctx, conn, i, "NO_SUCH_PROJECT") == nil
		}(i)
	}
	close(gate)
	wg.Wait()

	for i, ok := range accepted {
		if ok {
			t.Errorf("connection %d accepted a slot referencing a nonexistent project: "+
				"foreign_keys is OFF on this pooled connection", i)
		}
	}
}

// TestDeleteProjectCascadesToSlots pins the behaviour the schema declares, and
// the one this bug actually cost us: slots_v3.project_id is ON DELETE CASCADE,
// but a cascade only fires when foreign_keys is ON for the connection running
// the DELETE. With the pragma applied to a single pooled connection, deleting a
// project left its slots behind as orphans — and reported no error.
//
// The delete is driven once per pooled connection rather than once against the
// *sql.DB. Going through the pool picks an arbitrary connection, which under the
// old code was usually one that happened to have foreign_keys ON — so the bug
// hid, and a single-delete version of this test passed against it every time.
func TestDeleteProjectCascadesToSlots(t *testing.T) {
	f := openTestForge(t)
	ctx := context.Background()

	const connections = 8
	gate := make(chan struct{})

	var wg sync.WaitGroup
	orphans := make([]int, connections)
	for i := 0; i < connections; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			conn, err := f.db.Conn(ctx)
			if err != nil {
				t.Errorf("checkout connection %d: %v", i, err)
				return
			}
			defer conn.Close()

			// Hold every connection open at once, so the pool must open a
			// distinct one per goroutine and each cascade runs on its own.
			<-gate

			project := fmt.Sprintf("proj-%d", i)
			if _, err := conn.ExecContext(ctx,
				`INSERT INTO projects_v3 (id, name, base_port, created_at, updated_at) VALUES (?, ?, ?, ?, ?)`,
				project, project, 9000+i*10, now(), now()); err != nil {
				t.Errorf("seed project on connection %d: %v", i, err)
				return
			}
			if err := insertSlot(ctx, conn, i, project); err != nil {
				t.Errorf("seed slot on connection %d: %v", i, err)
				return
			}
			if _, err := conn.ExecContext(ctx, `DELETE FROM projects_v3 WHERE id = ?`, project); err != nil {
				t.Errorf("delete project on connection %d: %v", i, err)
				return
			}
			if err := conn.QueryRowContext(ctx,
				`SELECT COUNT(*) FROM slots_v3 WHERE project_id = ?`, project).Scan(&orphans[i]); err != nil {
				t.Errorf("count slots on connection %d: %v", i, err)
			}
		}(i)
	}
	close(gate)
	wg.Wait()

	for i, left := range orphans {
		if left != 0 {
			t.Errorf("ON DELETE CASCADE did not fire on connection %d: "+
				"%d slot(s) outlived their deleted project", i, left)
		}
	}
}

// TestOpenRejectsADatabaseWithoutForeignKeyEnforcement guards the DSN itself.
// The driver silently ignores an unrecognised connection-string key, so a typo
// in dataSourceName would switch enforcement back off without any error. Open
// verifies the pragma took effect; this proves the silent-typo hazard it guards
// against is real.
func TestOpenRejectsADatabaseWithoutForeignKeyEnforcement(t *testing.T) {
	db, err := sql.Open("sqlite3", "file:"+filepath.Join(t.TempDir(), "forge.db")+"?_foriegn_keys=on")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	var enabled bool
	if err := db.QueryRow("PRAGMA foreign_keys").Scan(&enabled); err != nil {
		t.Fatalf("read pragma: %v", err)
	}
	if enabled {
		t.Fatal("expected the driver to ignore the misspelled DSN key and leave foreign_keys OFF; " +
			"if this fails the driver now validates keys, and Open's check could be simplified")
	}
}

// TestOpenAcceptsThePathShapesCallersActuallyPass covers the fact that a DSN is
// a URI, not a filename. Two shapes have to survive the conversion:
//
//   - a relative path — SQLite parses whatever follows "file://" as an
//     authority, so "./forge.db" naively encoded becomes the authority "."
//     and the open is rejected outright;
//   - a path needing percent-escaping, which must round-trip rather than
//     truncate the database name at the first space.
func TestOpenAcceptsThePathShapesCallersActuallyPass(t *testing.T) {
	for _, tc := range []struct {
		name string
		path func(dir string) string
	}{
		{"absolute", func(dir string) string { return filepath.Join(dir, "forge.db") }},
		{"with spaces", func(dir string) string { return filepath.Join(dir, "a dir with spaces", "forge.db") }},
		{"relative", func(dir string) string { return "./forge.db" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if tc.name == "relative" {
				t.Chdir(dir) // so "./forge.db" lands in the temp dir, not the repo
			}

			f, err := Open(tc.path(dir))
			if err != nil {
				t.Fatalf("Open(%s): %v", tc.name, err)
			}
			defer f.Close()

			// Prove it opened the database we asked for and can write to it.
			seedProject(t, f, "proj")
			var count int
			if err := f.db.QueryRow(`SELECT COUNT(*) FROM projects_v3`).Scan(&count); err != nil {
				t.Fatalf("query: %v", err)
			}
			if count != 1 {
				t.Errorf("projects_v3 count = %d, want 1", count)
			}
		})
	}
}
