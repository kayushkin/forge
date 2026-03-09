package forge

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// Changeset groups PRs across repos in an environment
type Changeset struct {
	ID          string
	EnvironmentID int
	Title       string
	Description string
	Status      string // open, merged, closed
	CreatedAt   time.Time
	MergedAt    *time.Time
	PRs         []PullRequest
}

// PullRequest is an individual PR in a changeset
type PullRequest struct {
	ID          int64
	ChangesetID string
	ProjectID   string
	PRURL       string
	PRNumber    int
	Status      string
	CommitHash  string
}

// CreateChangeset starts a new changeset for an environment
func (f *Forge) CreateChangeset(envID int, title, description string) (*Changeset, error) {
	id := uuid.New().String()
	_, err := f.db.Exec(`
		INSERT INTO changesets (id, environment_id, title, description, status, created_at)
		VALUES (?, ?, ?, ?, 'open', ?)
	`, id, envID, title, description, time.Now().Unix())
	if err != nil {
		return nil, err
	}

	return &Changeset{
		ID:            id,
		EnvironmentID: envID,
		Title:         title,
		Description:   description,
		Status:        "open",
		CreatedAt:     time.Now(),
	}, nil
}

// GetChangeset fetches a changeset by ID
func (f *Forge) GetChangeset(id string) (*Changeset, error) {
	var cs Changeset
	var mergedAt sql.NullInt64
	err := f.db.QueryRow(`
		SELECT id, environment_id, title, description, status, created_at, merged_at
		FROM changesets WHERE id = ?
	`, id).Scan(&cs.ID, &cs.EnvironmentID, &cs.Title, &cs.Description, &cs.Status, &cs.CreatedAt, &mergedAt)
	if err != nil {
		return nil, err
	}
	if mergedAt.Valid {
		t := time.Unix(mergedAt.Int64, 0)
		cs.MergedAt = &t
	}

	// Load PRs
	prs, err := f.GetChangesetPRs(id)
	if err == nil {
		cs.PRs = prs
	}

	return &cs, nil
}

// GetChangesetPRs fetches all PRs in a changeset
func (f *Forge) GetChangesetPRs(changesetID string) ([]PullRequest, error) {
	rows, err := f.db.Query(`
		SELECT id, changeset_id, project_id, pr_url, pr_number, status, commit_hash
		FROM pull_requests WHERE changeset_id = ?
	`, changesetID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var prs []PullRequest
	for rows.Next() {
		var pr PullRequest
		var prURL sql.NullString
		var prNumber sql.NullInt64
		if err := rows.Scan(&pr.ID, &pr.ChangesetID, &pr.ProjectID, &prURL, &prNumber, &pr.Status, &pr.CommitHash); err != nil {
			return nil, err
		}
		if prURL.Valid {
			pr.PRURL = prURL.String
		}
		if prNumber.Valid {
			pr.PRNumber = int(prNumber.Int64)
		}
		prs = append(prs, pr)
	}
	return prs, nil
}

// AddPR adds a PR to a changeset
func (f *Forge) AddPR(changesetID, projectID, prURL string, prNumber int, commitHash string) error {
	_, err := f.db.Exec(`
		INSERT INTO pull_requests (changeset_id, project_id, pr_url, pr_number, status, commit_hash)
		VALUES (?, ?, ?, ?, 'open', ?)
	`, changesetID, projectID, prURL, prNumber, commitHash)
	return err
}

// UpdatePRStatus updates a PR's status
func (f *Forge) UpdatePRStatus(changesetID, projectID, status string) error {
	_, err := f.db.Exec(`
		UPDATE pull_requests SET status = ? WHERE changeset_id = ? AND project_id = ?
	`, status, changesetID, projectID)
	return err
}

// MergeChangeset marks all PRs as merged
func (f *Forge) MergeChangeset(id string) error {
	_, err := f.db.Exec(`
		UPDATE changesets SET status = 'merged', merged_at = ? WHERE id = ?
	`, time.Now().Unix(), id)
	if err != nil {
		return err
	}

	_, err = f.db.Exec(`UPDATE pull_requests SET status = 'merged' WHERE changeset_id = ?`, id)
	return err
}

// CloseChangeset marks a changeset as closed (not merged)
func (f *Forge) CloseChangeset(id string) error {
	_, err := f.db.Exec(`UPDATE changesets SET status = 'closed' WHERE id = ?`, id)
	return err
}

// GetEnvironmentChangesets returns all changesets for an environment
func (f *Forge) GetEnvironmentChangesets(envID int) ([]Changeset, error) {
	rows, err := f.db.Query(`
		SELECT id, environment_id, title, description, status, created_at, merged_at
		FROM changesets WHERE environment_id = ? ORDER BY created_at DESC
	`, envID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var changesets []Changeset
	for rows.Next() {
		var cs Changeset
		var mergedAt sql.NullInt64
		var createdAt int64
		if err := rows.Scan(&cs.ID, &cs.EnvironmentID, &cs.Title, &cs.Description, &cs.Status, &createdAt, &mergedAt); err != nil {
			return nil, err
		}
		cs.CreatedAt = time.Unix(createdAt, 0)
		if mergedAt.Valid {
			t := time.Unix(mergedAt.Int64, 0)
			cs.MergedAt = &t
		}
		changesets = append(changesets, cs)
	}
	return changesets, nil
}

// GetActiveChangeset returns the current open changeset for an environment
func (f *Forge) GetActiveChangeset(envID int) (*Changeset, error) {
	var cs Changeset
	var mergedAt sql.NullInt64
	var createdAt int64
	err := f.db.QueryRow(`
		SELECT id, environment_id, title, description, status, created_at, merged_at
		FROM changesets WHERE environment_id = ? AND status = 'open'
		ORDER BY created_at DESC LIMIT 1
	`, envID).Scan(&cs.ID, &cs.EnvironmentID, &cs.Title, &cs.Description, &cs.Status, &createdAt, &mergedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	cs.CreatedAt = time.Unix(createdAt, 0)
	if mergedAt.Valid {
		t := time.Unix(mergedAt.Int64, 0)
		cs.MergedAt = &t
	}
	return &cs, nil
}

// Summary returns a human-readable summary of the changeset
func (cs *Changeset) Summary() string {
	status := cs.Status
	if status == "open" && len(cs.PRs) > 0 {
		merged := 0
		for _, pr := range cs.PRs {
			if pr.Status == "merged" {
				merged++
			}
		}
		status = fmt.Sprintf("open (%d/%d merged)", merged, len(cs.PRs))
	}
	return fmt.Sprintf("%s [%s] - %s", cs.ID[:8], status, cs.Title)
}
