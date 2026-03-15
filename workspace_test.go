package forge

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// initBareRepo creates a git repo at path with an initial commit on "main".
func initBareRepo(t *testing.T, path string) {
	t.Helper()
	os.MkdirAll(path, 0755)
	run(t, path, "git", "init", "-b", "main")
	run(t, path, "git", "config", "user.email", "test@test.com")
	run(t, path, "git", "config", "user.name", "Test")
	os.WriteFile(filepath.Join(path, "README.md"), []byte("# test\n"), 0644)
	run(t, path, "git", "add", "-A")
	run(t, path, "git", "commit", "-m", "initial")
}

func run(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%v failed: %s\n%s", args, err, out)
	}
}

func setupForge(t *testing.T) (*Forge, string) {
	t.Helper()
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "forge.db")
	f, err := Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.Close() })

	// Override workDir for tests
	return f, tmp
}

func registerProject(t *testing.T, f *Forge, name, repoPath string, poolSize int) {
	t.Helper()
	err := f.RegisterProject(Project{
		ID:            name,
		BaseRepo:      repoPath,
		PoolSize:      poolSize,
		DefaultBranch: "main",
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestCreateWorkspace_Single(t *testing.T) {
	f, tmp := setupForge(t)
	ResetWorkspaceSemaphores()

	repo := filepath.Join(tmp, "repos", "myproject")
	initBareRepo(t, repo)

	registerProject(t, f, "myproject", repo, 3)

	ws, err := f.CreateWorkspace("agent1", []string{"myproject"})
	if err != nil {
		t.Fatal(err)
	}
	defer f.Cleanup(ws)

	if ws.Primary != "myproject" {
		t.Errorf("primary = %q, want myproject", ws.Primary)
	}
	if ws.Status != "created" {
		t.Errorf("status = %q, want created", ws.Status)
	}
	if _, ok := ws.Repos["myproject"]; !ok {
		t.Error("missing myproject in repos")
	}
	// Worktree dir should exist
	if _, err := os.Stat(ws.Repos["myproject"]); err != nil {
		t.Errorf("worktree not found: %v", err)
	}
}

func TestCreateWorkspace_Multi(t *testing.T) {
	f, tmp := setupForge(t)
	ResetWorkspaceSemaphores()

	repo1 := filepath.Join(tmp, "repos", "proj1")
	repo2 := filepath.Join(tmp, "repos", "proj2")
	initBareRepo(t, repo1)
	initBareRepo(t, repo2)

	registerProject(t, f, "proj1", repo1, 3)
	registerProject(t, f, "proj2", repo2, 3)

	ws, err := f.CreateWorkspace("agent1", []string{"proj1", "proj2"})
	if err != nil {
		t.Fatal(err)
	}
	defer f.Cleanup(ws)

	if len(ws.Repos) != 2 {
		t.Errorf("got %d repos, want 2", len(ws.Repos))
	}
	if ws.Primary != "proj1" {
		t.Errorf("primary = %q, want proj1", ws.Primary)
	}
}

func TestCommitAll_DirtyAndClean(t *testing.T) {
	f, tmp := setupForge(t)
	ResetWorkspaceSemaphores()

	repo := filepath.Join(tmp, "repos", "proj")
	initBareRepo(t, repo)
	registerProject(t, f, "proj", repo, 3)

	ws, err := f.CreateWorkspace("agent1", []string{"proj"})
	if err != nil {
		t.Fatal(err)
	}
	defer f.Cleanup(ws)

	// Commit clean repo
	results, err := f.CommitAll(ws, "clean commit")
	if err != nil {
		t.Fatal(err)
	}
	if results["proj"].Dirty {
		t.Error("expected clean, got dirty")
	}

	// Make dirty
	os.WriteFile(filepath.Join(ws.Repos["proj"], "newfile.txt"), []byte("hello"), 0644)

	results, err = f.CommitAll(ws, "dirty commit")
	if err != nil {
		t.Fatal(err)
	}
	if !results["proj"].Dirty {
		t.Error("expected dirty")
	}
	if results["proj"].Hash == "" {
		t.Error("expected hash")
	}
	if results["proj"].Error != "" {
		t.Errorf("unexpected error: %s", results["proj"].Error)
	}
}

func TestMergeToMain_CleanFF(t *testing.T) {
	f, tmp := setupForge(t)
	ResetWorkspaceSemaphores()

	repo := filepath.Join(tmp, "repos", "proj")
	initBareRepo(t, repo)
	registerProject(t, f, "proj", repo, 3)

	ws, err := f.CreateWorkspace("agent1", []string{"proj"})
	if err != nil {
		t.Fatal(err)
	}
	defer f.Cleanup(ws)

	// Add a file and commit
	os.WriteFile(filepath.Join(ws.Repos["proj"], "feature.txt"), []byte("feature"), 0644)
	_, err = f.CommitAll(ws, "add feature")
	if err != nil {
		t.Fatal(err)
	}

	// Merge
	results := f.MergeToMain(ws)
	if results["proj"].Status != "ok" {
		t.Errorf("merge status = %q, want ok; error: %s", results["proj"].Status, results["proj"].Error)
	}

	// Verify file exists in base repo on main
	if _, err := os.Stat(filepath.Join(repo, "feature.txt")); err != nil {
		t.Error("feature.txt not in base repo after merge")
	}
}

func TestMergeToMain_Conflict(t *testing.T) {
	f, tmp := setupForge(t)
	ResetWorkspaceSemaphores()

	repo := filepath.Join(tmp, "repos", "proj")
	initBareRepo(t, repo)
	registerProject(t, f, "proj", repo, 3)

	ws, err := f.CreateWorkspace("agent1", []string{"proj"})
	if err != nil {
		t.Fatal(err)
	}
	defer f.Cleanup(ws)

	// Make a change in worktree
	os.WriteFile(filepath.Join(ws.Repos["proj"], "README.md"), []byte("worktree change\n"), 0644)
	f.CommitAll(ws, "worktree change")

	// Make a conflicting change in base repo
	os.WriteFile(filepath.Join(repo, "README.md"), []byte("base change\n"), 0644)
	run(t, repo, "git", "add", "-A")
	run(t, repo, "git", "commit", "-m", "base conflict")

	// Merge should report conflict
	results := f.MergeToMain(ws)
	r := results["proj"]
	if r.Status != "conflict" && r.Status != "error" {
		t.Errorf("expected conflict or error, got %q", r.Status)
	}
}

func TestCleanup(t *testing.T) {
	f, tmp := setupForge(t)
	ResetWorkspaceSemaphores()

	repo := filepath.Join(tmp, "repos", "proj")
	initBareRepo(t, repo)
	registerProject(t, f, "proj", repo, 3)

	ws, err := f.CreateWorkspace("agent1", []string{"proj"})
	if err != nil {
		t.Fatal(err)
	}

	wtPath := ws.Repos["proj"]

	err = f.Cleanup(ws)
	if err != nil {
		t.Fatal(err)
	}

	// Worktree dir should be gone
	if _, err := os.Stat(wtPath); !os.IsNotExist(err) {
		t.Error("worktree dir still exists after cleanup")
	}

	// Base dir should be gone
	if _, err := os.Stat(ws.BaseDir); !os.IsNotExist(err) {
		t.Error("base dir still exists after cleanup")
	}

	// Branch should be gone
	cmd := exec.Command("git", "-C", repo, "branch", "--list", ws.Branch)
	out, _ := cmd.Output()
	if len(out) > 0 {
		t.Error("branch still exists after cleanup")
	}
}

func TestConcurrencyLimit(t *testing.T) {
	f, tmp := setupForge(t)
	ResetWorkspaceSemaphores()

	repo := filepath.Join(tmp, "repos", "proj")
	initBareRepo(t, repo)
	registerProject(t, f, "proj", repo, 1) // pool_size = 1

	ws1, err := f.CreateWorkspace("agent1", []string{"proj"})
	if err != nil {
		t.Fatal(err)
	}

	// Second workspace should fail
	_, err = f.CreateWorkspace("agent2", []string{"proj"})
	if err == nil {
		t.Fatal("expected concurrency error, got nil")
	}

	// Cleanup first, then second should work
	f.Cleanup(ws1)

	ws2, err := f.CreateWorkspace("agent3", []string{"proj"})
	if err != nil {
		t.Fatalf("after cleanup, create failed: %v", err)
	}
	f.Cleanup(ws2)
}

func TestReopenWorkspace(t *testing.T) {
	f, tmp := setupForge(t)
	ResetWorkspaceSemaphores()

	repo := filepath.Join(tmp, "repos", "proj")
	initBareRepo(t, repo)
	registerProject(t, f, "proj", repo, 3)

	ws, err := f.CreateWorkspace("agent1", []string{"proj"})
	if err != nil {
		t.Fatal(err)
	}
	defer f.Cleanup(ws)

	// Should work from created
	if err := f.ReopenWorkspace(ws); err != nil {
		t.Fatal(err)
	}
	if ws.Status != "working" {
		t.Errorf("status = %q, want working", ws.Status)
	}

	// Set to done, reopen again
	ws.Status = "done"
	if err := f.ReopenWorkspace(ws); err != nil {
		t.Fatal(err)
	}

	// Merged should fail
	ws.Status = "merged"
	if err := f.ReopenWorkspace(ws); err == nil {
		t.Error("expected error for reopening merged workspace")
	}

	// Expired should fail
	ws.Status = "expired"
	if err := f.ReopenWorkspace(ws); err == nil {
		t.Error("expected error for reopening expired workspace")
	}
}

func TestListWorkspaces(t *testing.T) {
	f, tmp := setupForge(t)
	ResetWorkspaceSemaphores()

	repo := filepath.Join(tmp, "repos", "proj")
	initBareRepo(t, repo)
	registerProject(t, f, "proj", repo, 3)

	ws, err := f.CreateWorkspace("agent1", []string{"proj"})
	if err != nil {
		t.Fatal(err)
	}

	list := f.ListWorkspaces()
	found := false
	for _, w := range list {
		if w.ID == ws.ID {
			found = true
		}
	}
	if !found {
		t.Error("workspace not found in list")
	}

	f.Cleanup(ws)

	list = f.ListWorkspaces()
	for _, w := range list {
		if w.ID == ws.ID {
			t.Error("workspace still in list after cleanup")
		}
	}
}
