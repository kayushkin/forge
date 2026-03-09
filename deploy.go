package forge

import (
	"database/sql"
	"fmt"
	"time"
)

// Deploy represents a deployment record.
type Deploy struct {
	ID            int64  `json:"id"`
	Project       string `json:"project"`
	Target        string `json:"target"` // "prod", "dev-0", "dev-1", etc.
	CommitHash    string `json:"commit_hash"`
	CommitMessage string `json:"commit_message"`
	Branch        string `json:"branch"`
	Status        string `json:"status"` // pending, running, success, failed
	Error         string `json:"error,omitempty"`
	TriggeredBy   string `json:"triggered_by,omitempty"`
	StartedAt     int64  `json:"started_at"`
	FinishedAt    int64  `json:"finished_at,omitempty"`
	RevertedAt    int64  `json:"reverted_at,omitempty"`
}

// RecordDeploy creates a new deploy record and returns its ID.
func (f *Forge) RecordDeploy(project, target, commitHash, commitMessage, branch, triggeredBy string) (int64, error) {
	res, err := f.db.Exec(`
		INSERT INTO deploys (project, target, commit_hash, commit_message, branch, status, triggered_by, started_at)
		VALUES (?, ?, ?, ?, ?, 'running', ?, ?)
	`, project, target, commitHash, commitMessage, branch, triggeredBy, now())
	if err != nil {
		return 0, fmt.Errorf("record deploy: %w", err)
	}
	return res.LastInsertId()
}

// FinishDeploy marks a deploy as success or failed.
func (f *Forge) FinishDeploy(id int64, success bool, errMsg string) error {
	status := "success"
	if !success {
		status = "failed"
	}
	_, err := f.db.Exec(`
		UPDATE deploys SET status = ?, error = ?, finished_at = ?
		WHERE id = ?
	`, status, errMsg, now(), id)
	return err
}

// MarkReverted marks a deploy as having been reverted.
func (f *Forge) MarkReverted(id int64) error {
	_, err := f.db.Exec(`UPDATE deploys SET reverted_at = ? WHERE id = ?`, now(), id)
	return err
}

// ListDeploys returns recent deploys for a project+target, newest first.
func (f *Forge) ListDeploys(project, target string, limit int) ([]Deploy, error) {
	if limit <= 0 {
		limit = 20
	}
	query := `SELECT id, project, target, commit_hash, commit_message, branch, status, error, triggered_by, started_at, finished_at, reverted_at
		FROM deploys WHERE project = ?`
	args := []interface{}{project}
	if target != "" {
		query += ` AND target = ?`
		args = append(args, target)
	}
	query += ` ORDER BY id DESC LIMIT ?`
	args = append(args, limit)

	rows, err := f.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var deploys []Deploy
	for rows.Next() {
		var d Deploy
		var errMsg, triggeredBy, branch sql.NullString
		var finishedAt, revertedAt sql.NullInt64
		if err := rows.Scan(&d.ID, &d.Project, &d.Target, &d.CommitHash, &d.CommitMessage, &branch,
			&d.Status, &errMsg, &triggeredBy, &d.StartedAt, &finishedAt, &revertedAt); err != nil {
			return nil, err
		}
		if errMsg.Valid {
			d.Error = errMsg.String
		}
		if triggeredBy.Valid {
			d.TriggeredBy = triggeredBy.String
		}
		if branch.Valid {
			d.Branch = branch.String
		}
		if finishedAt.Valid {
			d.FinishedAt = finishedAt.Int64
		}
		if revertedAt.Valid {
			d.RevertedAt = revertedAt.Int64
		}
		deploys = append(deploys, d)
	}
	return deploys, nil
}

// AllDeploys returns recent deploys across all projects.
func (f *Forge) AllDeploys(limit int) ([]Deploy, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := f.db.Query(`
		SELECT id, project, target, commit_hash, commit_message, branch, status, error, triggered_by, started_at, finished_at, reverted_at
		FROM deploys ORDER BY id DESC LIMIT ?
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var deploys []Deploy
	for rows.Next() {
		var d Deploy
		var errMsg, triggeredBy, branch sql.NullString
		var finishedAt, revertedAt sql.NullInt64
		if err := rows.Scan(&d.ID, &d.Project, &d.Target, &d.CommitHash, &d.CommitMessage, &branch,
			&d.Status, &errMsg, &triggeredBy, &d.StartedAt, &finishedAt, &revertedAt); err != nil {
			return nil, err
		}
		if errMsg.Valid {
			d.Error = errMsg.String
		}
		if triggeredBy.Valid {
			d.TriggeredBy = triggeredBy.String
		}
		if branch.Valid {
			d.Branch = branch.String
		}
		if finishedAt.Valid {
			d.FinishedAt = finishedAt.Int64
		}
		if revertedAt.Valid {
			d.RevertedAt = revertedAt.Int64
		}
		deploys = append(deploys, d)
	}
	return deploys, nil
}

// GetLatestDeploy returns the most recent successful deploy for a target.
func (f *Forge) GetLatestDeploy(project, target string) (*Deploy, error) {
	deploys, err := f.ListDeploys(project, target, 1)
	if err != nil {
		return nil, err
	}
	if len(deploys) == 0 {
		return nil, nil
	}
	return &deploys[0], nil
}

// now helper
func init() {
	// Ensure deploys table exists on older DBs
	_ = time.Now
}
