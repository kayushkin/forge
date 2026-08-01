package forge

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The defect these tests pin: a workspace lived in the memory of the process
// that created it and as a directory of worktrees, and nowhere else. Which
// repository was primary and which branch held the work were decided by
// CreateWorkspace and never written down, so once that process restarted the
// work on disk could not be merged, pushed or reopened by anything.

func TestAWorkspaceCanBeReadBackAfterTheProcessThatCreatedItIsGone(t *testing.T) {
	f, tmp := setupForge(t)
	ResetWorkspaceReservations()

	first := filepath.Join(tmp, "repos", "first")
	second := filepath.Join(tmp, "repos", "second")
	initBareRepo(t, first)
	initBareRepo(t, second)
	// Registered second-first so that the primary is NOT the alphabetically
	// first name: the reconstruction this replaces would have picked "first".
	registerProject(t, f, "second", second, 3)
	registerProject(t, f, "first", first, 3)

	created, err := f.CreateWorkspace("brigid", []string{"second", "first"})
	if err != nil {
		t.Fatal(err)
	}
	defer f.Cleanup(created)

	// Nothing of the in-memory value is used from here on: this is what a
	// restarted caller has, an id out of its own transcript.
	read, err := f.GetWorkspace(created.ID)
	if err != nil {
		t.Fatalf("GetWorkspace(%q): %v", created.ID, err)
	}

	if read.Primary != "second" {
		t.Errorf("the workspace came back with %q as its primary repository, and it was created for %q", read.Primary, "second")
	}
	if read.Branch != created.Branch {
		t.Errorf("the workspace came back on branch %q, and its work is on %q", read.Branch, created.Branch)
	}
	if len(read.Repos) != len(created.Repos) {
		t.Fatalf("the workspace came back holding %d repositories, and forge checked out %d", len(read.Repos), len(created.Repos))
	}
	for name, path := range created.Repos {
		if read.Repos[name] != path {
			t.Errorf("repository %s came back at %q, and its worktree is at %q", name, read.Repos[name], path)
		}
	}
	if read.Status != created.Status {
		t.Errorf("the workspace came back with status %q, and it is %q", read.Status, created.Status)
	}
}

func TestAWorkspaceStatusSurvivesTheProcessThatChangedIt(t *testing.T) {
	f, tmp := setupForge(t)
	ResetWorkspaceReservations()

	repo := filepath.Join(tmp, "repos", "proj")
	initBareRepo(t, repo)
	registerProject(t, f, "proj", repo, 3)

	ws, err := f.CreateWorkspace("brigid", []string{"proj"})
	if err != nil {
		t.Fatal(err)
	}
	defer f.Cleanup(ws)

	// A commit moves the workspace on, and ReopenWorkspace's refusals are read
	// off the status — so a status only the running process knows is a refusal
	// the next process cannot make.
	if _, err := f.CommitAll(ws, "nothing to commit"); err != nil {
		t.Fatalf("CommitAll: %v", err)
	}
	read, err := f.GetWorkspace(ws.ID)
	if err != nil {
		t.Fatal(err)
	}
	if read.Status != "done" {
		t.Errorf("after a commit the recorded status is %q, want done", read.Status)
	}

	if err := f.ReopenWorkspace(read); err != nil {
		t.Fatalf("ReopenWorkspace: %v", err)
	}
	reread, err := f.GetWorkspace(ws.ID)
	if err != nil {
		t.Fatal(err)
	}
	if reread.Status != "working" {
		t.Errorf("after a reopen the recorded status is %q, want working", reread.Status)
	}
}

func TestAWorkspaceWithNoRecordIsRefusedRatherThanGuessedAt(t *testing.T) {
	f, tmp := setupForge(t)
	ResetWorkspaceReservations()

	repo := filepath.Join(tmp, "repos", "proj")
	initBareRepo(t, repo)
	registerProject(t, f, "proj", repo, 3)

	ws, err := f.CreateWorkspace("brigid", []string{"proj"})
	if err != nil {
		t.Fatal(err)
	}
	defer f.Cleanup(ws)

	// A workspace created by a forge that kept no records: worktrees on disk,
	// nothing saying which of them is primary.
	if err := os.Remove(workspaceRecordPath(ws.BaseDir)); err != nil {
		t.Fatal(err)
	}

	if _, err := f.GetWorkspace(ws.ID); err == nil {
		t.Fatal("a workspace with no record was returned, so its primary repository was guessed")
	}

	// And it does not hide the workspaces that do have records.
	other, err := f.CreateWorkspace("oisin", []string{"proj"})
	if err != nil {
		t.Fatal(err)
	}
	defer f.Cleanup(other)

	list, err := f.ListWorkspaces()
	if err == nil {
		t.Error("the unreadable workspace was not reported")
	} else if !strings.Contains(err.Error(), ws.BaseDir) {
		t.Errorf("the report does not name the unreadable workspace: %v", err)
	}
	found := false
	for _, w := range list {
		if w.ID == other.ID {
			found = true
		}
	}
	if !found {
		t.Error("one unreadable workspace hid every readable one")
	}
}

func TestAWorkspaceThatCannotNameItsPrimaryRepositoryIsRefused(t *testing.T) {
	f, tmp := setupForge(t)
	ResetWorkspaceReservations()

	repo := filepath.Join(tmp, "repos", "proj")
	initBareRepo(t, repo)
	registerProject(t, f, "proj", repo, 3)

	ws, err := f.CreateWorkspace("brigid", []string{"proj"})
	if err != nil {
		t.Fatal(err)
	}
	defer f.Cleanup(ws)

	// Reading a record whose primary names no repository would hand back a
	// workspace whose primary worktree path is the empty string, and an empty
	// root is how a caller ends up writing into its own process directory.
	damaged := *ws
	damaged.Primary = "a-repository-that-is-not-here"
	encoded, err := json.MarshalIndent(damaged, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(workspaceRecordPath(ws.BaseDir), encoded, 0644); err != nil {
		t.Fatal(err)
	}

	if _, err := f.GetWorkspace(ws.ID); err == nil {
		t.Fatal("a workspace whose primary repository is not one of its repositories was returned")
	}
}

func TestAFailedRecordIsReportedRatherThanPassedOver(t *testing.T) {
	f, tmp := setupForge(t)
	ResetWorkspaceReservations()

	repo := filepath.Join(tmp, "repos", "proj")
	initBareRepo(t, repo)
	registerProject(t, f, "proj", repo, 3)

	ws, err := f.CreateWorkspace("brigid", []string{"proj"})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		os.Chmod(ws.BaseDir, 0o755)
		f.Cleanup(ws)
	}()

	// A workspace directory that cannot be written into. The status change still
	// happens in memory — the commit it describes has already been made — but the
	// caller is told the record no longer matches it, because silently keeping a
	// record that says "created" over work that is finished is how a later reopen
	// gets its refusal wrong.
	if err := os.Chmod(ws.BaseDir, 0o555); err != nil {
		t.Fatal(err)
	}
	if _, err := f.CommitAll(ws, "nothing to commit"); err == nil {
		t.Fatal("a status that could not be recorded was reported as recorded")
	}
	if ws.Status != "done" {
		t.Errorf("the in-memory status is %q, and the commit it describes has happened", ws.Status)
	}
}
