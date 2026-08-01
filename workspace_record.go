package forge

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// workspaceRecordName is the file every workspace keeps beside its worktrees,
// holding what forge decided when it created that workspace.
//
// A workspace used to exist in exactly two places: the memory of the process
// that called CreateWorkspace, and a directory of git worktrees. That is enough
// to see that a workspace is there, and not enough to use one. Which repository
// is primary, and which branch the work sits on, are choices CreateWorkspace
// made and nothing wrote down — so ListWorkspaces guessed the primary (the
// first name os.ReadDir handed back, which is alphabetical order and not the
// project the workspace was made for) and hardcoded the status, with a comment
// admitting the real one could not be known from disk. A restart of the calling
// process therefore turned every live workspace into a directory full of work
// that nothing could merge, push or reopen.
//
// The record is a file rather than a directory so that the repository scan,
// which counts every subdirectory of a workspace as a checked-out repository,
// passes over it.
const workspaceRecordName = "workspace.json"

// workspaceRecordPath is where the workspace rooted at baseDir records itself.
func workspaceRecordPath(baseDir string) string {
	return filepath.Join(baseDir, workspaceRecordName)
}

// saveWorkspaceRecord writes a workspace's description of itself into its own
// directory, replacing any earlier one.
//
// The write goes to a temporary file and is renamed over the record, so a
// reader either sees the previous record or the new one. A half-written record
// is the one outcome that would be worse than no record at all: every field in
// it is a path or a name that some later caller will act on.
func saveWorkspaceRecord(ws *Workspace) error {
	if ws == nil {
		return fmt.Errorf("no workspace to record")
	}
	if ws.BaseDir == "" {
		return fmt.Errorf("workspace %q has no directory to record itself in", ws.ID)
	}
	encoded, err := json.MarshalIndent(ws, "", "  ")
	if err != nil {
		return fmt.Errorf("record workspace %s: %w", ws.ID, err)
	}
	path := workspaceRecordPath(ws.BaseDir)
	temporary := path + ".writing"
	if err := os.WriteFile(temporary, append(encoded, '\n'), 0644); err != nil {
		return fmt.Errorf("record workspace %s: %w", ws.ID, err)
	}
	if err := os.Rename(temporary, path); err != nil {
		os.Remove(temporary)
		return fmt.Errorf("record workspace %s: %w", ws.ID, err)
	}
	return nil
}

// recordWorkspaceStatus moves a workspace to a new status and writes that down.
//
// The two happen together because a status the record does not carry is a
// status the next process to open this workspace will not see: ReopenWorkspace
// refuses a merged or expired workspace, and it can only refuse what it can
// read.
func recordWorkspaceStatus(ws *Workspace, status string) error {
	if ws == nil {
		return fmt.Errorf("no workspace to record")
	}
	ws.Status = status
	return saveWorkspaceRecord(ws)
}

// GetWorkspace returns the workspace with this id, as the workspace itself
// recorded it.
//
// A workspace whose directory is gone, and one whose directory is there without
// a record, are different answers and are reported as such. The second is a
// workspace created before forge wrote records: its worktrees and its work are
// real, and the primary repository and branch that would be needed to merge them
// are not recoverable from the directory tree. Guessing them here is what the
// record exists to stop.
func (f *Forge) GetWorkspace(id string) (*Workspace, error) {
	if id == "" {
		return nil, fmt.Errorf("no workspace id")
	}
	return readWorkspaceRecord(filepath.Join(workDir(), id))
}

// readWorkspaceRecord reads the record of the workspace rooted at baseDir and
// checks that it still describes something usable.
func readWorkspaceRecord(baseDir string) (*Workspace, error) {
	data, err := os.ReadFile(workspaceRecordPath(baseDir))
	if errors.Is(err, fs.ErrNotExist) {
		if _, statErr := os.Stat(baseDir); statErr != nil {
			return nil, fmt.Errorf("no workspace at %s: %w", baseDir, statErr)
		}
		return nil, fmt.Errorf("the workspace at %s kept no %s, so which repository is primary and which branch holds the work were never recorded", baseDir, workspaceRecordName)
	}
	if err != nil {
		return nil, fmt.Errorf("read workspace record in %s: %w", baseDir, err)
	}

	var ws Workspace
	if err := json.Unmarshal(data, &ws); err != nil {
		return nil, fmt.Errorf("read workspace record in %s: %w", baseDir, err)
	}
	if ws.ID == "" {
		return nil, fmt.Errorf("the workspace record in %s names no workspace", baseDir)
	}
	if ws.BaseDir != baseDir {
		return nil, fmt.Errorf("the workspace record in %s was written for %s, so this workspace has been moved and every worktree path it carries points at the old place", baseDir, ws.BaseDir)
	}
	if len(ws.Repos) == 0 {
		return nil, fmt.Errorf("the workspace record for %s holds no repositories", ws.ID)
	}
	if _, isARepository := ws.Repos[ws.Primary]; !isARepository {
		names := make([]string, 0, len(ws.Repos))
		for name := range ws.Repos {
			names = append(names, name)
		}
		sort.Strings(names)
		return nil, fmt.Errorf("the workspace record for %s names %q as its primary repository, and holds no repository by that name (it holds: %s)",
			ws.ID, ws.Primary, strings.Join(names, ", "))
	}
	return &ws, nil
}
