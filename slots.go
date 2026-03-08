package forge

import (
	"database/sql"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// Slot represents an isolated workspace
type Slot struct {
	ID           int
	Project      string
	Path         string
	Branch       string
	Status       string // ready, acquired, building, previewing, dirty
	AgentID      string
	SessionID    string
	Orchestrator string
	AcquiredAt   *time.Time
	ReleasedAt   *time.Time
}

// AcquireOpts configures a slot acquisition
type AcquireOpts struct {
	AgentID      string
	SessionID    string
	Orchestrator string
}

// ErrNoSlots is returned when all slots are busy
var ErrNoSlots = fmt.Errorf("no available slots")

// InitSlots creates worktree slots for a registered project
func (f *Forge) InitSlots(projectID string) error {
	p, err := f.GetProject(projectID)
	if err != nil {
		return err
	}

	baseRepo := expandPath(p.BaseRepo)
	poolDir := expandPath(p.PoolDir)

	// Verify git repo
	if _, err := os.Stat(filepath.Join(baseRepo, ".git")); err != nil {
		return fmt.Errorf("%s is not a git repo: %w", baseRepo, err)
	}

	if err := os.MkdirAll(poolDir, 0755); err != nil {
		return fmt.Errorf("create pool dir: %w", err)
	}

	for i := 0; i < p.PoolSize; i++ {
		slotPath := filepath.Join(poolDir, fmt.Sprintf("slot-%d", i))
		branch := fmt.Sprintf("pool/%s/slot-%d", projectID, i)

		_, err := f.db.Exec(`
			INSERT OR IGNORE INTO slots (id, project, path, branch, status)
			VALUES (?, ?, ?, ?, 'ready')
		`, i, projectID, slotPath, branch)
		if err != nil {
			return fmt.Errorf("insert slot %d: %w", i, err)
		}

		// Create worktree if it doesn't exist
		if _, err := os.Stat(slotPath); err == nil {
			continue
		}

		cmd := exec.Command("git", "worktree", "add", slotPath, "-b", branch)
		cmd.Dir = baseRepo
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("create worktree %d: %w\n%s", i, err, out)
		}
	}

	return nil
}

// Acquire leases the first available slot
func (f *Forge) Acquire(projectID string, opts AcquireOpts) (*Slot, error) {
	var id int
	var path, branch string
	err := f.db.QueryRow(`
		SELECT id, path, branch FROM slots
		WHERE project = ? AND status = 'ready'
		ORDER BY id LIMIT 1
	`, projectID).Scan(&id, &path, &branch)
	if err == sql.ErrNoRows {
		return nil, ErrNoSlots
	}
	if err != nil {
		return nil, err
	}

	ts := now()
	_, err = f.db.Exec(`
		UPDATE slots SET status = 'acquired', agent_id = ?, session_id = ?, orchestrator = ?, acquired_at = ?, released_at = NULL
		WHERE project = ? AND id = ?
	`, opts.AgentID, opts.SessionID, opts.Orchestrator, ts, projectID, id)
	if err != nil {
		return nil, err
	}

	acq := time.Unix(ts, 0)
	return &Slot{
		ID:           id,
		Project:      projectID,
		Path:         path,
		Branch:       branch,
		Status:       "acquired",
		AgentID:      opts.AgentID,
		SessionID:    opts.SessionID,
		Orchestrator: opts.Orchestrator,
		AcquiredAt:   &acq,
	}, nil
}

// Release returns a slot to the pool
func (f *Forge) Release(projectID string, slotID int) error {
	// Reset the worktree
	var path string
	err := f.db.QueryRow(`SELECT path FROM slots WHERE project = ? AND id = ?`, projectID, slotID).Scan(&path)
	if err != nil {
		return err
	}

	// Stop any running preview
	f.StopPreview(projectID, slotID)

	// Reset worktree to default branch
	resetWorktree(path)

	_, err = f.db.Exec(`
		UPDATE slots SET status = 'ready', agent_id = NULL, session_id = NULL, orchestrator = NULL, released_at = ?
		WHERE project = ? AND id = ?
	`, now(), projectID, slotID)
	return err
}

// SlotStatus returns all slots for a project
func (f *Forge) SlotStatus(projectID string) ([]Slot, error) {
	rows, err := f.db.Query(`
		SELECT id, project, path, branch, status, agent_id, session_id, orchestrator, acquired_at, released_at
		FROM slots WHERE project = ? ORDER BY id
	`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []Slot
	for rows.Next() {
		var s Slot
		var agentID, sessionID, orchestrator sql.NullString
		var acquiredAt, releasedAt sql.NullInt64
		if err := rows.Scan(&s.ID, &s.Project, &s.Path, &s.Branch, &s.Status, &agentID, &sessionID, &orchestrator, &acquiredAt, &releasedAt); err != nil {
			return nil, err
		}
		if agentID.Valid {
			s.AgentID = agentID.String
		}
		if sessionID.Valid {
			s.SessionID = sessionID.String
		}
		if orchestrator.Valid {
			s.Orchestrator = orchestrator.String
		}
		if acquiredAt.Valid {
			t := time.Unix(acquiredAt.Int64, 0)
			s.AcquiredAt = &t
		}
		if releasedAt.Valid {
			t := time.Unix(releasedAt.Int64, 0)
			s.ReleasedAt = &t
		}
		results = append(results, s)
	}
	return results, nil
}

// AllSlots returns slots across all projects
func (f *Forge) AllSlots() ([]Slot, error) {
	rows, err := f.db.Query(`
		SELECT id, project, path, branch, status, agent_id, session_id, orchestrator, acquired_at, released_at
		FROM slots ORDER BY project, id
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []Slot
	for rows.Next() {
		var s Slot
		var agentID, sessionID, orchestrator sql.NullString
		var acquiredAt, releasedAt sql.NullInt64
		if err := rows.Scan(&s.ID, &s.Project, &s.Path, &s.Branch, &s.Status, &agentID, &sessionID, &orchestrator, &acquiredAt, &releasedAt); err != nil {
			return nil, err
		}
		if agentID.Valid {
			s.AgentID = agentID.String
		}
		if sessionID.Valid {
			s.SessionID = sessionID.String
		}
		if orchestrator.Valid {
			s.Orchestrator = orchestrator.String
		}
		if acquiredAt.Valid {
			t := time.Unix(acquiredAt.Int64, 0)
			s.AcquiredAt = &t
		}
		if releasedAt.Valid {
			t := time.Unix(releasedAt.Int64, 0)
			s.ReleasedAt = &t
		}
		results = append(results, s)
	}
	return results, nil
}

// Git helpers for slots

// SlotPull fetches and merges the default branch into a slot's worktree
func (f *Forge) SlotPull(projectID string, slotID int) error {
	var path string
	if err := f.db.QueryRow(`SELECT path FROM slots WHERE project = ? AND id = ?`, projectID, slotID).Scan(&path); err != nil {
		return err
	}
	return gitPull(path)
}

// SlotCommit commits all changes in a slot
func (f *Forge) SlotCommit(projectID string, slotID int, msg string) error {
	var path string
	if err := f.db.QueryRow(`SELECT path FROM slots WHERE project = ? AND id = ?`, projectID, slotID).Scan(&path); err != nil {
		return err
	}
	return gitCommit(path, msg)
}

// SlotPush pushes the slot's branch to origin
func (f *Forge) SlotPush(projectID string, slotID int) error {
	var path, branch string
	if err := f.db.QueryRow(`SELECT path, branch FROM slots WHERE project = ? AND id = ?`, projectID, slotID).Scan(&path, &branch); err != nil {
		return err
	}
	return gitPush(path, branch)
}

// SlotDiff returns the diff of the slot against the default branch
func (f *Forge) SlotDiff(projectID string, slotID int) (string, error) {
	var path string
	if err := f.db.QueryRow(`SELECT path FROM slots WHERE project = ? AND id = ?`, projectID, slotID).Scan(&path); err != nil {
		return "", err
	}
	return gitDiff(path)
}
