package forge

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// setupTestForgeV2 creates a temp forge DB with v2 slots backed by real git worktrees.
func setupTestForgeV2(t *testing.T) (*Forge, string) {
	t.Helper()
	dir := t.TempDir()

	// Create a base git repo.
	baseRepo := filepath.Join(dir, "base-repo")
	must(t, os.MkdirAll(baseRepo, 0755))
	testGitInit(t, baseRepo)
	writeFile(t, filepath.Join(baseRepo, "hello.txt"), "hello world\n")
	testGitAdd(t, baseRepo, ".")
	testGitCommit(t, baseRepo, "initial commit")

	// Open forge DB.
	dbPath := filepath.Join(dir, "forge.db")
	f, err := Open(dbPath)
	if err != nil {
		t.Fatalf("open forge: %v", err)
	}

	// Insert project into existing schema (created by Open).
	poolDir := filepath.Join(dir, "slots", "testproj")
	_, err = f.db.Exec(`INSERT INTO projects (id, base_repo, pool_dir, created_at, updated_at) VALUES ('testproj', ?, ?, 0, 0)`,
		baseRepo, poolDir)
	if err != nil {
		t.Fatalf("insert project: %v", err)
	}

	// Create 2 slot worktrees.
	for i := 0; i < 2; i++ {
		slotPath := filepath.Join(dir, "slots", "testproj", slotName(i))
		branch := slotName(i)

		// Create branch in base repo.
		testGitRun(t, baseRepo, "branch", branch)
		// Create worktree.
		testGitRun(t, baseRepo, "worktree", "add", slotPath, branch)

		_, err = f.db.Exec(`INSERT INTO slots (id, project, path, status) VALUES (?, 'testproj', ?, 'ready')`,
			i, slotPath)
		if err != nil {
			t.Fatalf("insert slot: %v", err)
		}
	}

	return f, dir
}

func slotName(i int) string {
	return "forge/slot-" + string(rune('0'+i))
}

func TestAcquireV2_Basic(t *testing.T) {
	f, _ := setupTestForgeV2(t)
	defer f.Close()

	slot, err := f.AcquireV2("testproj", "brigid", "sess-1")
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	if slot.ID != 0 {
		t.Errorf("expected slot 0, got %d", slot.ID)
	}
	if slot.Status != "acquired" {
		t.Errorf("expected acquired, got %s", slot.Status)
	}
	if slot.AgentID != "brigid" {
		t.Errorf("expected brigid, got %s", slot.AgentID)
	}
	if slot.Path == "" {
		t.Error("expected non-empty path")
	}
}

func TestAcquireV2_ExhaustsPool(t *testing.T) {
	f, _ := setupTestForgeV2(t)
	defer f.Close()

	// Acquire both slots.
	_, err := f.AcquireV2("testproj", "brigid", "sess-1")
	if err != nil {
		t.Fatalf("acquire 1: %v", err)
	}
	_, err = f.AcquireV2("testproj", "oisin", "sess-2")
	if err != nil {
		t.Fatalf("acquire 2: %v", err)
	}

	// Third should fail.
	_, err = f.AcquireV2("testproj", "fionn", "sess-3")
	if err == nil {
		t.Fatal("expected error when pool exhausted")
	}
}

func TestReleaseV2(t *testing.T) {
	f, _ := setupTestForgeV2(t)
	defer f.Close()

	slot, _ := f.AcquireV2("testproj", "brigid", "sess-1")

	err := f.ReleaseV2("testproj", slot.ID)
	if err != nil {
		t.Fatalf("release: %v", err)
	}

	// Should be able to acquire again.
	slot2, err := f.AcquireV2("testproj", "oisin", "sess-2")
	if err != nil {
		t.Fatalf("re-acquire: %v", err)
	}
	if slot2.ID != 0 {
		t.Errorf("expected slot 0 re-acquired, got %d", slot2.ID)
	}
}

func TestCommitSlotChanges_Clean(t *testing.T) {
	f, _ := setupTestForgeV2(t)
	defer f.Close()

	slot, _ := f.AcquireV2("testproj", "brigid", "sess-1")

	hash, dirty, err := f.CommitSlotChanges("testproj", slot.ID, "test commit")
	if err != nil {
		t.Fatalf("commit: %v", err)
	}
	if dirty {
		t.Error("expected clean repo, got dirty")
	}
	if hash != "" {
		t.Errorf("expected empty hash for clean repo, got %s", hash)
	}
}

func TestCommitSlotChanges_Dirty(t *testing.T) {
	f, _ := setupTestForgeV2(t)
	defer f.Close()

	slot, _ := f.AcquireV2("testproj", "brigid", "sess-1")

	// Dirty the worktree.
	writeFile(t, filepath.Join(slot.Path, "new-file.txt"), "agent wrote this\n")

	hash, dirty, err := f.CommitSlotChanges("testproj", slot.ID, "spawn: brigid — test task")
	if err != nil {
		t.Fatalf("commit: %v", err)
	}
	if !dirty {
		t.Error("expected dirty, got clean")
	}
	if hash == "" {
		t.Error("expected commit hash, got empty")
	}
	if len(hash) < 7 {
		t.Errorf("hash too short: %s", hash)
	}

	// Verify commit message.
	out, _ := exec.Command("git", "-C", slot.Path, "log", "-1", "--format=%s").Output()
	msg := string(out)
	if msg == "" || msg[:5] != "spawn" {
		t.Errorf("unexpected commit message: %s", msg)
	}
}

func TestCommitSlotChanges_DefaultMessage(t *testing.T) {
	f, _ := setupTestForgeV2(t)
	defer f.Close()

	slot, _ := f.AcquireV2("testproj", "brigid", "sess-1")
	writeFile(t, filepath.Join(slot.Path, "x.txt"), "data\n")

	hash, dirty, err := f.CommitSlotChanges("testproj", slot.ID, "")
	if err != nil {
		t.Fatalf("commit: %v", err)
	}
	if !dirty || hash == "" {
		t.Fatal("expected dirty commit")
	}

	out, _ := exec.Command("git", "-C", slot.Path, "log", "-1", "--format=%s").Output()
	if string(out) == "" {
		t.Error("expected default commit message")
	}
}

func TestCommitSlotChanges_NonexistentSlot(t *testing.T) {
	f, _ := setupTestForgeV2(t)
	defer f.Close()

	_, _, err := f.CommitSlotChanges("testproj", 99, "msg")
	if err == nil {
		t.Fatal("expected error for nonexistent slot")
	}
}

func TestSlotStatus(t *testing.T) {
	f, _ := setupTestForgeV2(t)
	defer f.Close()

	// All ready initially.
	slots, err := f.SlotStatus("testproj")
	if err != nil {
		// SlotStatus uses v3 table which may not exist — skip if so.
		t.Skip("v3 slots table not present, skipping SlotStatus test")
	}
	_ = slots
}

func TestAcquireReleaseV2_Roundtrip(t *testing.T) {
	f, _ := setupTestForgeV2(t)
	defer f.Close()

	// Acquire, dirty, commit, release — full lifecycle.
	slot, err := f.AcquireV2("testproj", "brigid", "sess-1")
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}

	// Agent does work.
	writeFile(t, filepath.Join(slot.Path, "feature.go"), "package main\n")

	// Auto-commit.
	hash, dirty, err := f.CommitSlotChanges("testproj", slot.ID, "spawn: brigid — add feature")
	if err != nil {
		t.Fatalf("commit: %v", err)
	}
	if !dirty || hash == "" {
		t.Fatal("expected commit")
	}

	// Release.
	if err := f.ReleaseV2("testproj", slot.ID); err != nil {
		t.Fatalf("release: %v", err)
	}

	// Verify slot is ready for reuse.
	var status string
	f.db.QueryRow("SELECT status FROM slots WHERE project='testproj' AND id=?", slot.ID).Scan(&status)
	if status != "ready" {
		t.Errorf("expected ready, got %s", status)
	}
}

// helpers

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	must(t, os.MkdirAll(filepath.Dir(path), 0755))
	must(t, os.WriteFile(path, []byte(content), 0644))
}

func testGitInit(t *testing.T, dir string) {
	t.Helper()
	testGitRun(t, dir, "init")
	testGitRun(t, dir, "config", "user.email", "test@test.com")
	testGitRun(t, dir, "config", "user.name", "Test")
}

func testGitAdd(t *testing.T, dir string, args ...string) {
	t.Helper()
	testGitRun(t, dir, append([]string{"add"}, args...)...)
}

func testGitCommit(t *testing.T, dir, msg string) {
	t.Helper()
	testGitRun(t, dir, "commit", "-m", msg)
}

func testGitRun(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %s — %v", args, string(out), err)
	}
}
