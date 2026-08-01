package forge

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// restartForge stands in for the one event the old limit could not survive: the
// process that counted the workspaces going away while the workspaces stay on
// disk. Nothing else is touched — same HOME, same ~/forge/work, same worktrees.
func restartForge() {
	ResetWorkspaceReservations()
}

func TestConcurrencyLimitSurvivesRestart(t *testing.T) {
	f, tmp := setupForge(t)
	ResetWorkspaceReservations()

	repo := filepath.Join(tmp, "repos", "proj")
	initBareRepo(t, repo)
	registerProject(t, f, "proj", repo, 1) // pool_size = 1

	ws, err := f.CreateWorkspace("agent1", []string{"proj"})
	if err != nil {
		t.Fatal(err)
	}
	defer f.Cleanup(ws)

	restartForge()

	// The one slot is still held by a worktree that is still checked out.
	second, err := f.CreateWorkspace("agent2", []string{"proj"})
	if err == nil {
		f.Cleanup(second)
		t.Fatal("a restart handed the pool back: the second workspace was created while the first still held the only slot")
	}
	if !strings.Contains(err.Error(), "concurrency limit") {
		t.Fatalf("wanted a concurrency-limit refusal, got: %v", err)
	}
}

func TestCleanupAfterRestartDoesNotFreeALiveWorkspacesSlot(t *testing.T) {
	f, tmp := setupForge(t)
	ResetWorkspaceReservations()

	repo := filepath.Join(tmp, "repos", "proj")
	initBareRepo(t, repo)
	registerProject(t, f, "proj", repo, 2) // pool_size = 2

	beforeRestart, err := f.CreateWorkspace("agent1", []string{"proj"})
	if err != nil {
		t.Fatal(err)
	}

	restartForge()

	afterRestart, err := f.CreateWorkspace("agent2", []string{"proj"})
	if err != nil {
		t.Fatalf("both slots were free to the new process, so the second workspace should have been created: %v", err)
	}
	defer f.Cleanup(afterRestart)

	// Cleaning up a workspace this process never counted must return that
	// workspace's slot and no other. The live one keeps its own.
	if err := f.Cleanup(beforeRestart); err != nil {
		t.Fatal(err)
	}

	third, err := f.CreateWorkspace("agent3", []string{"proj"})
	if err != nil {
		t.Fatalf("one slot was freed by the cleanup, so a third workspace should fit: %v", err)
	}
	defer f.Cleanup(third)

	fourth, err := f.CreateWorkspace("agent4", []string{"proj"})
	if err == nil {
		f.Cleanup(fourth)
		t.Fatal("the pre-restart cleanup released a slot belonging to a workspace that is still checked out: a fourth workspace was created against a pool of 2")
	}
	if !strings.Contains(err.Error(), "concurrency limit") {
		t.Fatalf("wanted a concurrency-limit refusal, got: %v", err)
	}
}

func TestWorkspaceWithoutARecordStillHoldsItsSlot(t *testing.T) {
	f, tmp := setupForge(t)
	ResetWorkspaceReservations()

	repo := filepath.Join(tmp, "repos", "proj")
	initBareRepo(t, repo)
	registerProject(t, f, "proj", repo, 1) // pool_size = 1

	// A workspace an older forge created: worktrees on disk, no record beside
	// them. It cannot be merged or reopened, and it is still occupying a slot.
	stranded := filepath.Join(workDir(), "olderforge-1700000000", "proj")
	if err := os.MkdirAll(stranded, 0755); err != nil {
		t.Fatal(err)
	}

	ws, err := f.CreateWorkspace("agent1", []string{"proj"})
	if err == nil {
		f.Cleanup(ws)
		t.Fatal("a workspace with no record was counted against nothing, so its slot was handed out a second time")
	}
	if !strings.Contains(err.Error(), "concurrency limit") {
		t.Fatalf("wanted a concurrency-limit refusal, got: %v", err)
	}
}

func TestOneRequestCannotTakeAProjectsSlotTwice(t *testing.T) {
	f, tmp := setupForge(t)
	ResetWorkspaceReservations()

	repo := filepath.Join(tmp, "repos", "proj")
	initBareRepo(t, repo)
	registerProject(t, f, "proj", repo, 1) // pool_size = 1

	// Naming a project twice asks for two of its slots. Checking every project
	// before taking any slot lets both checks pass against the same free one.
	ws, err := f.CreateWorkspace("agent1", []string{"proj", "proj"})
	if err == nil {
		f.Cleanup(ws)
		t.Fatal("one request took the same project's single slot twice")
	}
	if !strings.Contains(err.Error(), "concurrency limit") {
		t.Fatalf("wanted a concurrency-limit refusal, got: %v", err)
	}
}

func TestAWorkspaceBeingCreatedTakesOneSlotNotTwo(t *testing.T) {
	f, tmp := setupForge(t)
	ResetWorkspaceReservations()

	repo := filepath.Join(tmp, "repos", "proj")
	initBareRepo(t, repo)
	registerProject(t, f, "proj", repo, 2) // pool_size = 2

	// The state a workspace is in between "git worktree add" and writing its
	// record: its directories are on disk, so the scan can see them, and its
	// reservation is still held, because the record it will be counted by is not
	// written yet. It must be counted once.
	first := filepath.Join(workDir(), "agent1-1700000000")
	if err := reserveWorkspaceSlots(first, []string{"proj"}, map[string]int{"proj": 2}); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(first, "proj"), 0755); err != nil {
		t.Fatal(err)
	}

	second, err := f.CreateWorkspace("agent2", []string{"proj"})
	if err != nil {
		t.Fatalf("a workspace still being created was counted twice, so the second of two slots was refused: %v", err)
	}
	defer f.Cleanup(second)

	// And it really is holding the other slot: nothing else fits.
	third, err := f.CreateWorkspace("agent3", []string{"proj"})
	if err == nil {
		f.Cleanup(third)
		t.Fatal("a workspace still being created held no slot at all: a third workspace was created against a pool of 2")
	}
	if !strings.Contains(err.Error(), "concurrency limit") {
		t.Fatalf("wanted a concurrency-limit refusal, got: %v", err)
	}

	releaseWorkspaceSlots(first, []string{"proj"})
}

func TestFailedCreationReturnsItsReservation(t *testing.T) {
	f, tmp := setupForge(t)
	ResetWorkspaceReservations()

	good := filepath.Join(tmp, "repos", "good")
	initBareRepo(t, good)
	registerProject(t, f, "good", good, 1) // pool_size = 1

	// A project registered against a directory that is not a git repository, so
	// "git worktree add" fails and the whole workspace is rolled back.
	registerProject(t, f, "broken", filepath.Join(tmp, "repos", "nothinghere"), 1)

	if _, err := f.CreateWorkspace("agent1", []string{"good", "broken"}); err == nil {
		t.Fatal("expected the workspace to fail, since one of its projects has no repository")
	}

	// The rolled-back workspace left nothing on disk, so it must hold no slot.
	ws, err := f.CreateWorkspace("agent2", []string{"good"})
	if err != nil {
		t.Fatalf("a failed creation kept hold of its reservation: %v", err)
	}
	f.Cleanup(ws)
}
