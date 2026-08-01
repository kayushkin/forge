package forge

import (
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Workspace represents an ephemeral multi-repo workspace for an agent.
type Workspace struct {
	ID      string            // e.g. "brigid-1710512345"
	Repos   map[string]string // project name → worktree path
	Primary string            // primary repo name (first in list)
	BaseDir string            // ~/forge/work/<id>/
	Branch  string            // spawn/<id>
	Status  string            // created|working|done|staged|merged|rejected|expired
}

// CommitResult is the per-repo result of CommitAll.
type CommitResult struct {
	Hash  string
	Dirty bool
	Error string
}

// MergeResult is the per-repo result of MergeToMain.
type MergeResult struct {
	Status    string   // "ok", "conflict", "error"
	Conflicts []string
	Error     string
}

// workspaceSemaphores tracks in-memory concurrency limits per project.
var (
	wsMu       sync.Mutex
	wsSem      = map[string]int{} // project → current count
	wsLimits   = map[string]int{} // project → max (from pool_size)
)

// workDir returns the base directory for all workspaces.
func workDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "forge", "work")
}

// CreateWorkspace creates ephemeral worktrees for the given projects.
func (f *Forge) CreateWorkspace(agent string, projects []string) (*Workspace, error) {
	if len(projects) == 0 {
		return nil, fmt.Errorf("no projects specified")
	}

	ts := time.Now().Unix()
	id := fmt.Sprintf("%s-%d", agent, ts)
	branch := fmt.Sprintf("spawn/%s", id)
	baseDir := filepath.Join(workDir(), id)

	// Look up projects and check concurrency
	type projInfo struct {
		name string
		repo string
		defBranch string
	}
	var infos []projInfo

	wsMu.Lock()
	for _, name := range projects {
		p, err := f.GetProject(name)
		if err != nil {
			wsMu.Unlock()
			return nil, fmt.Errorf("project %q: %w", name, err)
		}

		limit := p.PoolSize
		if limit == 0 {
			limit = 3
		}
		wsLimits[name] = limit

		if wsSem[name] >= limit {
			wsMu.Unlock()
			return nil, fmt.Errorf("concurrency limit reached for project %q (%d/%d)", name, wsSem[name], limit)
		}

		defBranch := p.DefaultBranch
		if defBranch == "" {
			defBranch = "main"
		}
		infos = append(infos, projInfo{name: name, repo: expandHome(p.BaseRepo), defBranch: defBranch})
	}
	// Reserve slots
	for _, name := range projects {
		wsSem[name]++
	}
	wsMu.Unlock()

	// Rollback helper
	rollback := func(created []string) {
		for _, wt := range created {
			exec.Command("git", "-C", wt, "worktree", "remove", "--force", wt).Run()
		}
		os.RemoveAll(baseDir)
		wsMu.Lock()
		for _, name := range projects {
			wsSem[name]--
		}
		wsMu.Unlock()
	}

	if err := os.MkdirAll(baseDir, 0755); err != nil {
		wsMu.Lock()
		for _, name := range projects {
			wsSem[name]--
		}
		wsMu.Unlock()
		return nil, fmt.Errorf("create workspace dir: %w", err)
	}

	repos := make(map[string]string)
	var created []string

	for _, info := range infos {
		wtPath := filepath.Join(baseDir, info.name)

		// Create branch at current HEAD of default branch
		cmd := exec.Command("git", "-C", info.repo, "worktree", "add", "-b", branch, wtPath, info.defBranch)
		if out, err := cmd.CombinedOutput(); err != nil {
			rollback(created)
			return nil, fmt.Errorf("worktree add for %s: %s: %w", info.name, string(out), err)
		}
		repos[info.name] = wtPath
		created = append(created, wtPath)
	}

	ws := &Workspace{
		ID:      id,
		Repos:   repos,
		Primary: projects[0],
		BaseDir: baseDir,
		Branch:  branch,
		Status:  "created",
	}

	// The record is written before the workspace is handed out, and a workspace
	// that cannot record itself is rolled back rather than returned. Everything
	// decided above — which repository is primary, which branch the work goes
	// on — exists only in this value until it is written down, and a caller that
	// keeps it in memory loses all of it at its next restart while the worktrees
	// stay on disk holding the work.
	if err := saveWorkspaceRecord(ws); err != nil {
		rollback(created)
		return nil, err
	}
	return ws, nil
}

// CommitAll commits all dirty repos in the workspace.
func (f *Forge) CommitAll(ws *Workspace, message string) (map[string]CommitResult, error) {
	results := make(map[string]CommitResult)

	for name, path := range ws.Repos {
		dirty, err := gitIsDirty(path)
		if err != nil {
			results[name] = CommitResult{Error: err.Error()}
			continue
		}
		if !dirty {
			results[name] = CommitResult{Dirty: false}
			continue
		}

		// git add -A && git commit
		if err := gitCommit(path, message); err != nil {
			results[name] = CommitResult{Dirty: true, Error: err.Error()}
			continue
		}

		hash, err := gitHead(path)
		if err != nil {
			results[name] = CommitResult{Dirty: true, Error: err.Error()}
			continue
		}
		results[name] = CommitResult{Hash: hash, Dirty: true}
	}

	// The results are returned even when the new status cannot be written: the
	// commits named in them have already happened, and a caller that is handed
	// nothing but an error would have no way to learn about them.
	if err := recordWorkspaceStatus(ws, "done"); err != nil {
		return results, err
	}
	return results, nil
}

// MergeToMain rebases and fast-forward merges each repo to main.
func (f *Forge) MergeToMain(ws *Workspace) map[string]MergeResult {
	results := make(map[string]MergeResult)

	for name, wtPath := range ws.Repos {
		p, err := f.GetProject(name)
		if err != nil {
			results[name] = MergeResult{Status: "error", Error: err.Error()}
			continue
		}
		baseRepo := expandHome(p.BaseRepo)
		defBranch := p.DefaultBranch
		if defBranch == "" {
			defBranch = "main"
		}

		// 1. Fetch origin main in worktree
		cmd := exec.Command("git", "-C", wtPath, "fetch", "origin", defBranch)
		if out, err := cmd.CombinedOutput(); err != nil {
			// If no remote, skip fetch (local-only repo)
			_ = out
		}

		// 2. Rebase onto origin/main (or local main)
		rebaseTarget := fmt.Sprintf("origin/%s", defBranch)
		cmd = exec.Command("git", "-C", wtPath, "rebase", rebaseTarget)
		out, err := cmd.CombinedOutput()
		if err != nil {
			// Try local main if no origin
			cmd = exec.Command("git", "-C", wtPath, "rebase", defBranch)
			out, err = cmd.CombinedOutput()
		}
		if err != nil {
			// Abort rebase
			exec.Command("git", "-C", wtPath, "rebase", "--abort").Run()
			conflicts := parseConflicts(string(out))
			results[name] = MergeResult{Status: "conflict", Conflicts: conflicts, Error: string(out)}
			continue
		}

		// 3. FF merge into base repo main
		cmd = exec.Command("git", "-C", baseRepo, "merge", ws.Branch, "--ff-only")
		out, err = cmd.CombinedOutput()
		if err != nil {
			results[name] = MergeResult{Status: "error", Error: fmt.Sprintf("ff-merge: %s", string(out))}
			continue
		}

		results[name] = MergeResult{Status: "ok"}
	}

	// MergeToMain reports per-repository results and has nowhere to put an error
	// about the workspace as a whole, so a record that cannot be updated is
	// logged here rather than dropped. It is not fatal: the merge itself has
	// already happened, and the caller cleans a merged workspace up.
	if err := recordWorkspaceStatus(ws, "merged"); err != nil {
		log.Printf("[forge] %v", err)
	}
	return results
}

// PushAll pushes main for each repo.
func (f *Forge) PushAll(ws *Workspace) map[string]error {
	results := make(map[string]error)

	for name := range ws.Repos {
		p, err := f.GetProject(name)
		if err != nil {
			results[name] = err
			continue
		}
		baseRepo := expandHome(p.BaseRepo)
		defBranch := p.DefaultBranch
		if defBranch == "" {
			defBranch = "main"
		}

		if err := gitPush(baseRepo, defBranch); err != nil {
			results[name] = err
		}
	}
	return results
}

// Cleanup removes worktrees, deletes branches, and releases semaphore.
func (f *Forge) Cleanup(ws *Workspace) error {
	var errs []string

	for name, wtPath := range ws.Repos {
		p, _ := f.GetProject(name)
		var baseRepo string
		if p != nil {
			baseRepo = expandHome(p.BaseRepo)
		}

		// Remove worktree
		cmd := exec.Command("git", "-C", wtPath, "worktree", "remove", "--force", wtPath)
		if baseRepo != "" {
			cmd = exec.Command("git", "-C", baseRepo, "worktree", "remove", "--force", wtPath)
		}
		if out, err := cmd.CombinedOutput(); err != nil {
			errs = append(errs, fmt.Sprintf("remove worktree %s: %s", name, string(out)))
		}

		// Delete branch
		if baseRepo != "" {
			cmd = exec.Command("git", "-C", baseRepo, "branch", "-D", ws.Branch)
			cmd.CombinedOutput() // best effort
		}
	}

	// Remove workspace dir
	os.RemoveAll(ws.BaseDir)

	// Release semaphore
	wsMu.Lock()
	for name := range ws.Repos {
		if wsSem[name] > 0 {
			wsSem[name]--
		}
	}
	wsMu.Unlock()

	ws.Status = "expired"

	if len(errs) > 0 {
		return fmt.Errorf("cleanup errors: %s", strings.Join(errs, "; "))
	}
	return nil
}

// ReopenWorkspace sets the workspace status back to working for fix/retry.
func (f *Forge) ReopenWorkspace(ws *Workspace) error {
	if ws.Status == "merged" {
		return fmt.Errorf("cannot reopen merged workspace")
	}
	if ws.Status == "expired" {
		return fmt.Errorf("cannot reopen expired workspace")
	}
	return recordWorkspaceStatus(ws, "working")
}

// ListWorkspaces returns every workspace under ~/forge/work/, read from the
// record each one wrote for itself.
//
// It used to reconstruct each workspace from the shape of its directory, which
// cannot answer the two questions a workspace is asked: it took the primary
// repository to be whichever name os.ReadDir returned first — alphabetical
// order, not the project the workspace was created for — and filled the status
// in with a constant. A caller acting on either was acting on a guess.
//
// A directory that cannot be read is reported and does not stop the listing:
// one workspace created by an older forge, or half-deleted, must not hide every
// other workspace on the host from a caller looking for work to merge. Callers
// get both halves and are expected to say so.
func (f *Forge) ListWorkspaces() ([]*Workspace, error) {
	base := workDir()
	entries, err := os.ReadDir(base)
	if errors.Is(err, fs.ErrNotExist) {
		// No workspace has ever been created on this host, which is not a fault.
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read workspaces in %s: %w", base, err)
	}

	var result []*Workspace
	var unreadable []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		ws, err := readWorkspaceRecord(filepath.Join(base, e.Name()))
		if err != nil {
			unreadable = append(unreadable, err.Error())
			continue
		}
		result = append(result, ws)
	}
	if len(unreadable) > 0 {
		return result, fmt.Errorf("%d workspace directories could not be read: %s", len(unreadable), strings.Join(unreadable, "; "))
	}
	return result, nil
}

// parseConflicts extracts conflicting file names from rebase output.
func parseConflicts(output string) []string {
	var conflicts []string
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "CONFLICT") {
			// Extract filename if possible
			if idx := strings.LastIndex(line, " in "); idx >= 0 {
				conflicts = append(conflicts, strings.TrimSpace(line[idx+4:]))
			} else {
				conflicts = append(conflicts, line)
			}
		}
	}
	return conflicts
}

// ResetWorkspaceSemaphores clears all semaphore state. Used for testing.
func ResetWorkspaceSemaphores() {
	wsMu.Lock()
	wsSem = map[string]int{}
	wsLimits = map[string]int{}
	wsMu.Unlock()
}
